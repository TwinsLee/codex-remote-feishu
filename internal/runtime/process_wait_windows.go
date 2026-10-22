//go:build windows

package relayruntime

import (
	"errors"
	"fmt"
	"time"

	"golang.org/x/sys/windows"
)

func waitForProcessExit(pid int, timeout time.Duration) error {
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return nil // Windows reports a nonexistent PID with this error.
	}
	if err != nil {
		return fmt.Errorf("open process %d for exit observation: %w", pid, err)
	}
	defer windows.CloseHandle(handle)
	result, err := windows.WaitForSingleObject(handle, waitTimeoutMillis(timeout))
	if err != nil {
		return err
	}
	if result != uint32(windows.WAIT_OBJECT_0) {
		return fmt.Errorf("process %d exit unconfirmed: wait result %d", pid, result)
	}
	return nil
}
