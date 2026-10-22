package daemon

import (
	"fmt"
	"time"

	"github.com/kxn/codex-remote-feishu/internal/core/agentproto"
)

type desktopExitWaiter struct {
	instanceID   string
	connectionID uint64
	ready        chan error
}

func (a *App) noteDesktopExitConfirmationLocked(instanceID string, event agentproto.Event) bool {
	if event.Kind != agentproto.EventProcessExitReady {
		return false
	}
	waiter := a.desktopExitWaiters[event.CommandID]
	if waiter == nil || waiter.instanceID != instanceID || a.currentRelayConnection(instanceID) != waiter.connectionID {
		return true
	}
	delete(a.desktopExitWaiters, event.CommandID)
	var err error
	if event.Status != "succeeded" || event.Problem != nil {
		err = fmt.Errorf("native child exit was not confirmed")
	}
	waiter.ready <- err
	return true
}

func waitForDesktopChildExit(ready <-chan error, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-ready:
		return err
	case <-timer.C:
		return fmt.Errorf("native child exit confirmation timed out")
	}
}
