package relayruntime

import (
	"fmt"
	"time"
)

func TerminateProcess(pid int, grace time.Duration) error {
	return terminateProcess(pid, grace)
}

// WaitForProcessExit only observes a known process. It never sends a stop signal.
func WaitForProcessExit(pid int, timeout time.Duration) error {
	if pid <= 0 {
		return fmt.Errorf("cannot confirm exit without a process id")
	}
	return waitForProcessExit(pid, timeout)
}
