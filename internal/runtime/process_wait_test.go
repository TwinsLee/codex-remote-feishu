package relayruntime

import (
	"io"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestWaitForProcessExitHelper(t *testing.T) {
	if os.Getenv("REMOTE_WAIT_EXIT_HELPER") != "1" {
		return
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

func TestWaitForProcessExitDoesNotStopALiveProcess(t *testing.T) {
	if err := WaitForProcessExit(os.Getpid(), 25*time.Millisecond); err == nil {
		t.Fatal("a live process must time out, without being signalled")
	}
	if err := WaitForProcessExit(0, time.Second); err == nil {
		t.Fatal("unknown pid must not count as confirmed exit")
	}
}

func TestWaitForProcessExitWaitsForActualExit(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestWaitForProcessExitHelper$")
	cmd.Env = append(os.Environ(), "REMOTE_WAIT_EXIT_HELPER=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close(); _ = cmd.Process.Kill() }()
	go func() { _ = cmd.Wait() }()
	done := make(chan error, 1)
	go func() { done <- WaitForProcessExit(cmd.Process.Pid, 5*time.Second) }()
	select {
	case err := <-done:
		t.Fatalf("reported exit while process was alive: %v", err)
	case <-time.After(40 * time.Millisecond):
	}
	_ = stdin.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("exit was not detected")
	}
}
