package daemon

import (
	"context"
	"testing"

	"github.com/kxn/codex-remote-feishu/internal/core/agentproto"
	"github.com/kxn/codex-remote-feishu/internal/core/control"
)

func TestDeskDaemonDisconnectWithoutExitTimesOutWithoutSuccess(t *testing.T) {
	app, inst := deskDaemonForTest(t)
	command := deskRequestForTest(t, app)
	app.sendAgentCommand = func(_ string, c agentproto.Command) error {
		if c.Kind != agentproto.CommandProcessExit {
			t.Errorf("unexpected command: %s", c.Kind)
		}
		app.mu.Lock()
		app.service.ApplyInstanceDisconnected(inst.InstanceID)
		app.mu.Unlock()
		return nil // The process remains alive despite the disconnected transport.
	}
	events := app.handleDaemonCommand(command)
	if !eventsContainNoticeCode(events, "desk_failed") || eventsContainNoticeCode(events, "desk_released") {
		t.Fatalf("disconnect counted as successful exit: %#v", events)
	}
	if app.service.Surface("desk-surface").Abandoning {
		t.Fatal("exit timeout left session permanently blocked")
	}
	if app.service.Instance(inst.InstanceID) == nil {
		t.Fatal("unconfirmed process was removed")
	}
}

func TestDeskDoesNotChangeOrdinaryDetachOrBusyRejection(t *testing.T) {
	for _, busy := range []bool{false, true} {
		app, inst := deskDaemonForTest(t)
		app.sendAgentCommand = func(_ string, c agentproto.Command) error {
			if c.Kind == agentproto.CommandProcessExit {
				t.Error("ordinary detach or rejected desk attempted process exit")
			}
			return nil
		}
		action := deskDaemonAction()
		if busy {
			inst.ActiveTurnID = "running-turn"
		} else {
			action.Kind = control.ActionDetach
			action.Text = "/detach"
		}
		app.HandleAction(context.Background(), action)
		if app.service.Instance(inst.InstanceID) == nil {
			t.Fatal("untargeted process removed")
		}
		if busy && app.service.Surface("desk-surface").Abandoning {
			t.Fatal("busy desk interrupted execution")
		}
	}
}
