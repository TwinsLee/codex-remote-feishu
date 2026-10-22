package install

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestLaunchdStopWaitsUntilServiceIsUnloaded(t *testing.T) {
	defer withDarwinGOOS(t)()
	prints := 0
	defer withMockLaunchctl(t, func(_ context.Context, args ...string) (string, error) {
		switch args[0] {
		case "bootout":
			return "", nil
		case "print":
			prints++
			switch prints {
			case 1:
				return "state = SIGTERM sent\n pid = 55525\n", nil
			case 2:
				return "state = exited\n", nil
			default:
				return "", fmt.Errorf("Could not find service")
			}
		default:
			t.Fatalf("unexpected service operation: %v", args)
			return "", nil
		}
	})()
	if err := launchdUserStopAndWait(context.Background(), InstallState{}, time.Second, time.Microsecond); err != nil {
		t.Fatal(err)
	}
	if prints != 3 {
		t.Fatalf("must wait for launchd to remove the service, observed only %d status checks", prints)
	}
}

func TestLaunchdStopTimesOutWhileServiceIsStillRegistered(t *testing.T) {
	defer withDarwinGOOS(t)()
	defer withMockLaunchctl(t, func(_ context.Context, args ...string) (string, error) {
		return "state = SIGTERM sent\n pid = 55525\n", nil
	})()
	if err := launchdUserStopAndWait(context.Background(), InstallState{}, 0, time.Microsecond); err == nil {
		t.Fatal("must reject binary replacement while launchd is still unloading the old service")
	}
}
