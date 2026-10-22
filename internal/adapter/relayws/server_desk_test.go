package relayws

import (
	"testing"

	"github.com/kxn/codex-remote-feishu/internal/core/agentproto"
)

func TestDeskExitCannotBeSentToReplacementConnection(t *testing.T) {
	server := NewServer(ServerCallbacks{})
	// A nil socket deliberately makes any accidental write to the replacement fail.
	server.conns["instance"] = &serverConn{id: 2}
	if err := server.SendCommandOnConnection("instance", 1, agentproto.Command{Kind: agentproto.CommandProcessExit}); err == nil {
		t.Fatal("stale connection must not receive an exit request")
	}
	if err := server.SendCommandOnConnection("instance", 0, agentproto.Command{Kind: agentproto.CommandProcessExit}); err == nil {
		t.Fatal("missing connection identity must not fall back to the current socket")
	}
}
