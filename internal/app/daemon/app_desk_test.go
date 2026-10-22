package daemon

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/kxn/codex-remote-feishu/internal/app/daemon/surfaceresume"
	"github.com/kxn/codex-remote-feishu/internal/core/agentproto"
	"github.com/kxn/codex-remote-feishu/internal/core/control"
	"github.com/kxn/codex-remote-feishu/internal/core/state"
)

func TestDeskProcessHelper(t *testing.T) {
	if os.Getenv("REMOTE_DESK_TEST_PROCESS") != "1" {
		return
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

func deskDaemonForTest(t *testing.T) (*App, *state.InstanceRecord) {
	t.Helper()
	app := newRestoreHintTestApp(t.TempDir())
	workspace := t.TempDir()
	inst := &state.InstanceRecord{
		InstanceID: "desk-instance", WorkspaceRoot: workspace, WorkspaceKey: workspace,
		Backend: agentproto.BackendCodex, Source: "headless", Managed: true, Online: true, PID: os.Getpid(),
		Threads: map[string]*state.ThreadRecord{"desk-thread": {ThreadID: "desk-thread", Name: "原会话", CWD: workspace, Loaded: true}},
	}
	app.service.UpsertInstance(inst)
	app.observeManagedHeadless(inst)
	app.rememberRelayConnectionWithPID(inst.InstanceID, 1, inst.PID)
	app.sendAgentCommandOnConnection = func(id string, connectionID uint64, c agentproto.Command) error {
		if connectionID != app.currentRelayConnection(id) {
			return errors.New("stale connection")
		}
		err := app.sendAgentCommand(id, c)
		if err == nil {
			app.onEvents(context.Background(), id, []agentproto.Event{{Kind: agentproto.EventProcessExitReady, CommandID: c.CommandID, Status: "succeeded"}})
		}
		return err
	}
	app.service.ApplySurfaceAction(control.Action{Kind: control.ActionAttachInstance, SurfaceSessionID: "desk-surface", GatewayID: "desk-bot", ChatID: "desk-chat", ActorUserID: "desk-user", InstanceID: inst.InstanceID})
	app.service.ApplySurfaceAction(control.Action{Kind: control.ActionUseThread, SurfaceSessionID: "desk-surface", ThreadID: "desk-thread"})
	app.syncSurfaceResumeStateLocked(nil)
	if app.service.AttachedInstanceID("desk-surface") != inst.InstanceID {
		t.Fatal("fixture did not attach")
	}
	app.stopProcess = func(int, time.Duration) error {
		t.Error("desk must never use forced process stop")
		return errors.New("unexpected stop")
	}
	return app, inst
}

func deskDaemonAction() control.Action {
	return control.Action{Kind: control.ActionDesk, SurfaceSessionID: "desk-surface", GatewayID: "desk-bot", ChatID: "desk-chat", ActorUserID: "desk-user", Text: "/desk"}
}

func deskRequestForTest(t *testing.T, app *App) control.DaemonCommand {
	t.Helper()
	events := app.service.ApplySurfaceAction(deskDaemonAction())
	if len(events) != 1 || events[0].DaemonCommand == nil {
		t.Fatalf("expected handoff request: %#v", events)
	}
	return *events[0].DaemonCommand
}

func TestDeskDaemonSendFailureKeepsSessionAndResumeTarget(t *testing.T) {
	app, inst := deskDaemonForTest(t)
	calls := 0
	app.sendAgentCommand = func(id string, c agentproto.Command) error {
		calls++
		if id != inst.InstanceID || c.Kind != agentproto.CommandProcessExit {
			t.Errorf("wrong exit request: %s %#v", id, c)
		}
		return errors.New("transport unavailable")
	}
	app.HandleAction(context.Background(), deskDaemonAction())
	if calls != 1 {
		t.Fatalf("exit requests = %d, want 1", calls)
	}
	surface := app.service.Surface("desk-surface")
	if surface.Abandoning || surface.AttachedInstanceID != inst.InstanceID {
		t.Fatalf("failure stranded session: %#v", surface)
	}
	entry := app.SurfaceResumeState("desk-surface")
	if entry == nil || entry.ResumeThreadID != "desk-thread" {
		t.Fatalf("failure lost resume target: %#v", entry)
	}
}

func TestDeskDaemonWaitsWithoutBlockingOtherBotsAndClearsRecovery(t *testing.T) {
	app, inst := deskDaemonForTest(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestDeskProcessHelper$")
	cmd.Env = append(os.Environ(), "REMOTE_DESK_TEST_PROCESS=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close(); _ = cmd.Process.Kill() }()
	go func() { _ = cmd.Wait() }()
	inst.PID = cmd.Process.Pid
	app.managedHeadlessRuntime.Processes[inst.InstanceID].PID = inst.PID
	app.rememberRelayConnectionWithPID(inst.InstanceID, 1, inst.PID)
	other := &state.InstanceRecord{InstanceID: "other-bot-instance", Source: "headless", Managed: true, Online: true, PID: os.Getpid(), Threads: map[string]*state.ThreadRecord{}}
	app.service.UpsertInstance(other)
	app.observeManagedHeadless(other)
	sent := make(chan struct{})
	app.sendAgentCommand = func(id string, c agentproto.Command) error {
		if id != inst.InstanceID || c.Kind != agentproto.CommandProcessExit {
			t.Errorf("wrong exit request: %s %#v", id, c)
		}
		close(sent)
		return nil
	}
	done := make(chan struct{})
	go func() { app.HandleAction(context.Background(), deskDaemonAction()); close(done) }()
	waitForTestSignal(t, sent, "desktop exit request")
	unlock := lockAppForTest(t, app)
	if !app.service.Surface("desk-surface").Abandoning {
		t.Error("transport request must not mean released")
	}
	app.syncSurfaceResumeStateLocked(nil) // A concurrent persistence tick must not restore the target.
	store, err := surfaceresume.LoadStore(app.surfaceResumeRuntime.path)
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := store.Get("desk-surface")
	if entry.ResumeThreadID != "" || entry.ResumeHeadless {
		t.Errorf("recovery could steal the desktop session: %#v", entry)
	}
	if app.service.Instance(other.InstanceID) == nil {
		t.Error("other bot was removed")
	}
	unlock()
	select {
	case <-done:
		t.Fatal("success reported before real process exit")
	case <-time.After(50 * time.Millisecond):
	}
	_ = stdin.Close()
	waitForTestSignal(t, done, "desktop handoff completion")
	if app.service.AttachedInstanceID("desk-surface") != "" || app.service.Instance(inst.InstanceID) != nil {
		t.Fatal("exited instance was not detached and removed")
	}
	if _, ok := app.managedHeadlessRuntime.Processes[inst.InstanceID]; ok {
		t.Fatal("exited process retained in managed pool")
	}
	if app.service.Instance(other.InstanceID) != other {
		t.Fatal("other bot changed")
	}
	restarted := newRestoreHintTestApp(app.headlessRuntime.Paths.StateDir)
	if _, exists := restarted.surfaceResumeRuntime.recovery["desk-surface"]; exists {
		t.Fatal("restart would reacquire released session")
	}
}

func TestDeskDaemonRejectsMismatchedProcessMetadata(t *testing.T) {
	app, inst := deskDaemonForTest(t)
	command := deskRequestForTest(t, app)
	app.managedHeadlessRuntime.Processes[inst.InstanceID].PID++
	app.sendAgentCommand = func(string, agentproto.Command) error { t.Error("must not exit a mismatched instance"); return nil }
	events := app.handleDaemonCommand(command)
	if !eventsContainNoticeCode(events, "desk_failed") {
		t.Fatalf("missing visible failure: %#v", events)
	}
	if app.service.Surface("desk-surface").Abandoning {
		t.Fatal("failed handoff left session blocked")
	}
}

func TestDeskDaemonRefusesWhenResumeStateCannotBeSaved(t *testing.T) {
	app, _ := deskDaemonForTest(t)
	command := deskRequestForTest(t, app)
	path := app.surfaceResumeRuntime.path
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	app.sendAgentCommand = func(string, agentproto.Command) error {
		t.Error("must not exit without clearing recovery durably")
		return nil
	}
	events := app.handleDaemonCommand(command)
	if !eventsContainNoticeCode(events, "desk_failed") || app.service.Surface("desk-surface").Abandoning {
		t.Fatalf("persistence failure stranded session: %#v", events)
	}
}
