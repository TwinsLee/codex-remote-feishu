package daemon

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/kxn/codex-remote-feishu/internal/app/daemon/headlessruntime"
	"github.com/kxn/codex-remote-feishu/internal/core/agentproto"
	"github.com/kxn/codex-remote-feishu/internal/core/control"
	"github.com/kxn/codex-remote-feishu/internal/core/eventcontract"
	relayruntime "github.com/kxn/codex-remote-feishu/internal/runtime"
)

const desktopHandoffExitTimeout = 15 * time.Second

// handoffToDesktopLocked retains the instance claim while its wrapper exits.
// The caller owns a.mu; network IO and process observation must release it.
func (a *App) handoffToDesktopLocked(command control.DaemonCommand) []eventcontract.Event {
	if !a.service.DesktopHandoffPending(command.SurfaceSessionID, command.InstanceID) {
		return nil
	}
	fail := func(err error) []eventcontract.Event {
		log.Printf("desktop handoff failed: surface=%s instance=%s err=%v", command.SurfaceSessionID, command.InstanceID, err)
		events := a.service.FinishDesktopHandoff(command.SurfaceSessionID, command.InstanceID, false)
		a.syncSurfaceResumeStateLocked(nil)
		return events
	}
	inst := a.service.Instance(command.InstanceID)
	managed := a.managedHeadlessRuntime.Processes[command.InstanceID]
	if !headlessruntime.IsManagedInstance(inst) || !inst.Online || inst.Backend != agentproto.BackendCodex || managed == nil || inst.PID <= 0 || managed.PID != inst.PID || managed.Status == headlessruntime.StatusStopping {
		return fail(fmt.Errorf("managed process metadata no longer matches"))
	}
	connection := a.snapshotRelayConnections()[command.InstanceID]
	if connection.CurrentConnectionID == 0 || connection.PID != inst.PID {
		return fail(fmt.Errorf("relay connection no longer matches the original process"))
	}
	if !a.surfaceResumeRuntime.writable() || a.surfaceResumeRuntime.store == nil || strings.TrimSpace(a.surfaceResumeRuntime.store.Path()) == "" {
		return fail(fmt.Errorf("resume state is not writable"))
	}
	surface := a.service.Surface(command.SurfaceSessionID)
	entry, ok := a.currentSurfaceResumeEntryLocked(surface, true)
	if !ok {
		return fail(fmt.Errorf("surface resume metadata unavailable"))
	}
	entry.UpdatedAt = time.Now().UTC()
	if err := a.surfaceResumeRuntime.store.Put(entry); err != nil {
		return fail(fmt.Errorf("persist cleared resume target: %w", err))
	}
	a.syncSurfaceResumeRecoveryStateLocked()
	pid := inst.PID
	previousStatus := managed.Status
	managed.Status = headlessruntime.StatusStopping
	managed.RefreshInFlight = false
	managed.RefreshCommandID = ""
	exitCommand := agentproto.Command{CommandID: a.nextCommandID(), Kind: agentproto.CommandProcessExit, RequireChildExitConfirmation: true}
	waiter := &desktopExitWaiter{instanceID: command.InstanceID, connectionID: connection.CurrentConnectionID, ready: make(chan error, 1)}
	if a.desktopExitWaiters == nil {
		a.desktopExitWaiters = make(map[string]*desktopExitWaiter)
	}
	a.desktopExitWaiters[exitCommand.CommandID] = waiter
	a.mu.Unlock()
	err := a.sendAgentCommandOnConnection(command.InstanceID, connection.CurrentConnectionID, exitCommand)
	if err == nil {
		deadline := time.Now().Add(desktopHandoffExitTimeout)
		err = waitForDesktopChildExit(waiter.ready, time.Until(deadline))
		if err == nil {
			err = relayruntime.WaitForProcessExit(pid, time.Until(deadline))
		}
	}
	a.mu.Lock()
	delete(a.desktopExitWaiters, exitCommand.CommandID)
	// Never remove a replacement that connected while the original was exiting.
	if err == nil && (a.service.Instance(command.InstanceID) != inst || inst.PID != pid || a.managedHeadlessRuntime.Processes[command.InstanceID] != managed) {
		err = fmt.Errorf("instance changed during exit observation")
	}
	if err != nil {
		if current := a.managedHeadlessRuntime.Processes[command.InstanceID]; current == managed {
			current.Status = previousStatus
			current.LastError = fmt.Sprintf("交给桌面失败：%v", err)
		}
		events := fail(err)
		a.syncManagedHeadlessLocked(time.Now().UTC())
		return events
	}
	events := a.service.FinishDesktopHandoff(command.SurfaceSessionID, command.InstanceID, true)
	delete(a.managedHeadlessRuntime.Processes, command.InstanceID)
	a.service.RemoveInstance(command.InstanceID)
	a.syncSurfaceResumeStateLocked(map[string]bool{command.SurfaceSessionID: true})
	log.Printf("desktop handoff completed: surface=%s instance=%s pid=%d", command.SurfaceSessionID, command.InstanceID, pid)
	return events
}
