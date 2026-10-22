package wrapper

import (
	"os"
	"os/exec"
	"testing"

	"github.com/kxn/codex-remote-feishu/internal/core/agentproto"
)

func TestDeskConfirmationFailsWhileNativeChildStillExists(t *testing.T) {
	session := &childSession{cmd: &exec.Cmd{Process: &os.Process{Pid: os.Getpid()}}}
	event := confirmedChildExitEvent("desk-exit", session)
	if event.Kind != agentproto.EventProcessExitReady || event.Status != "failed" || event.Problem == nil {
		t.Fatalf("live native child must never be confirmed released: %#v", event)
	}
}

func TestDeskConfirmationAllowsAnAlreadyAbsentChild(t *testing.T) {
	event := confirmedChildExitEvent("desk-exit", nil)
	if event.Status != "succeeded" || event.CommandID != "desk-exit" {
		t.Fatalf("missing success correlation: %#v", event)
	}
}
