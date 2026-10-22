package wrapper

import (
	"context"
	"github.com/kxn/codex-remote-feishu/internal/adapter/relayws"
	"github.com/kxn/codex-remote-feishu/internal/core/agentproto"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDeskWrapperSendsChildExitConfirmation(t *testing.T) {
	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoRoot = filepath.Clean(filepath.Join(repoRoot, "..", "..", ".."))

	helloCh := make(chan agentproto.Hello, 1)
	ackCh := make(chan agentproto.CommandAck, 8)
	eventsCh := make(chan []agentproto.Event, 8)
	server := relayws.NewServer(relayws.ServerCallbacks{
		OnEvents: func(_ context.Context, _ relayws.ConnectionMeta, _ string, events []agentproto.Event) {
			eventsCh <- events
		},
		OnHello: func(_ context.Context, _ relayws.ConnectionMeta, hello agentproto.Hello) {
			helloCh <- hello
		},
		OnCommandAck: func(_ context.Context, _ relayws.ConnectionMeta, _ string, ack agentproto.CommandAck) {
			ackCh <- ack
		},
	})
	server.SetServerIdentity(agentproto.ServerIdentity{
		BinaryIdentity: agentproto.BinaryIdentity{
			Product:          "codex-remote",
			Version:          "test",
			BuildFingerprint: "fp-test",
			BinaryPath:       "/test/codex-remote",
		},
		PID: 1,
	})
	defer server.Close()

	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		server.ServeHTTP(w, r)
	}))
	defer httpServer.Close()
	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")

	var stdout testBuffer
	var stderr testBuffer

	cfg := Config{
		RelayServerURL:   wsURL,
		CodexRealBinary:  "go",
		Args:             []string{"run", "./testkit/mockcodex/cmd/mockcodex", "--no-auto-complete"},
		InstanceID:       "inst-wrapper-exit",
		DisplayName:      "codex-remote",
		WorkspaceRoot:    repoRoot,
		WorkspaceKey:     repoRoot,
		ShortName:        filepath.Base(repoRoot),
		Version:          "test",
		BuildFingerprint: "fp-test",
		BinaryPath:       "/test/codex-remote",
		DaemonBinaryPath: "/test/codex-remote",
	}
	app := New(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := app.Run(ctx, strings.NewReader(""), &stdout, &stderr)
		done <- err
	}()

	waitForHello(t, helloCh, "inst-wrapper-exit")

	if err := server.SendCommand("inst-wrapper-exit", agentproto.Command{
		CommandID:                    "cmd-exit",
		Kind:                         agentproto.CommandProcessExit,
		RequireChildExitConfirmation: true,
	}); err != nil {
		t.Fatalf("send process exit: %v", err)
	}

	waitForAck(t, ackCh, 5*time.Second, func(ack agentproto.CommandAck) bool {
		return ack.CommandID == "cmd-exit" && ack.Accepted
	}, &stdout, &stderr, done)

	waitForEvent(t, eventsCh, 5*time.Second, func(events []agentproto.Event) bool {
		for _, e := range events {
			if e.Kind == agentproto.EventProcessExitReady && e.CommandID == "cmd-exit" && e.Status == "succeeded" {
				return true
			}
		}
		return false
	}, &stdout, &stderr, nil)

	select {
	case err := <-done:
		if err != nil && err != context.Canceled {
			t.Fatalf("wrapper run failed: %v\nstderr:\n%s", err, stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for wrapper shutdown")
	}
}
