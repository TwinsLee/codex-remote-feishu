//go:build !windows

package relayruntime

import (
	"errors"
	"fmt"
	"syscall"
	"time"
)

func waitForProcessExit(pid int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("check process %d exit: %w", pid, err)
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("process %d has not exited before timeout", pid)
		}
		time.Sleep(min(25*time.Millisecond, remaining))
	}
}
