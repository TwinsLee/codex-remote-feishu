package daemon

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/kxn/codex-remote-feishu/internal/app/daemon/headlessruntime"
	"github.com/kxn/codex-remote-feishu/internal/core/agentproto"
)

func TestDeskDaemonRejectsFailedChildExitAndRestoresReplacedProcess(t *testing.T) {
	for _, replaced := range []bool{false, true} {
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
		request := deskRequestForTest(t, app)
		app.sendAgentCommand = func(id string, c agentproto.Command) error {
			app.mu.Lock()
			if replaced {
				inst.PID++ // A replacement Hello updates the same managed record.
				app.observeManagedHeadless(inst)
			}
			app.mu.Unlock()
			if !replaced {
				app.onEvents(context.Background(), id, []agentproto.Event{{Kind: agentproto.EventProcessExitReady, CommandID: c.CommandID, Status: "failed"}})
			}
			_ = stdin.Close()
			return nil
		}
		events := app.handleDaemonCommand(request)
		if !eventsContainNoticeCode(events, "desk_failed") || eventsContainNoticeCode(events, "desk_released") {
			t.Errorf("unconfirmed handoff reported success (replaced=%t): %#v", replaced, events)
		}
		if p := app.managedHeadlessRuntime.Processes[inst.InstanceID]; p == nil || p.Status == headlessruntime.StatusStopping {
			t.Errorf("failure stranded a replacement in stopping: %#v", p)
		}
	}
}

func TestDeskChildConfirmationHasBoundedWait(t *testing.T) {
	if err := waitForDesktopChildExit(make(chan error), 20*time.Millisecond); err == nil {
		t.Fatal("missing confirmation must time out")
	}
}
