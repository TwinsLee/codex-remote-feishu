//go:build !windows

package wrapper

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestDeskNativeWriterHelper(t *testing.T) {
	if os.Getenv("REMOTE_DESK_NATIVE_HELPER") != "1" {
		return
	}
	lock, err := os.OpenFile(os.Getenv("REMOTE_DESK_NATIVE_LOCK"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		os.Exit(2)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		os.Exit(3)
	}
	if err := os.WriteFile(os.Getenv("REMOTE_DESK_NATIVE_READY"), []byte("ready"), 0600); err != nil {
		os.Exit(4)
	}
	for {
		time.Sleep(time.Hour)
	}
}

func TestDeskNormalChildStopReleasesNativeWriterLock(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "writer.lock")
	readyPath := lockPath + ".ready"
	cmd := exec.Command(os.Args[0], "-test.run=^TestDeskNativeWriterHelper$")
	cmd.Env = append(os.Environ(), "REMOTE_DESK_NATIVE_HELPER=1", "REMOTE_DESK_NATIVE_LOCK="+lockPath, "REMOTE_DESK_NATIVE_READY="+readyPath)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill() }()
	session := &childSession{cmd: cmd, waitErr: make(chan error, 1)}
	go func() { session.waitErr <- cmd.Wait() }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(readyPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("native child never acquired writer lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	lock, err := os.OpenFile(lockPath, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
		t.Fatal("fixture did not hold writer lock")
	}
	// process.exit uses this exact cleanup before the wrapper returns.
	stopChildSession(session, t.Logf)
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("native writer lock still held after normal cleanup: %v", err)
	}
	_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
}
