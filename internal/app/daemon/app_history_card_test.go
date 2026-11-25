package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/kxn/codex-remote-feishu/internal/adapter/feishu"
	"github.com/kxn/codex-remote-feishu/internal/core/agentproto"
	"github.com/kxn/codex-remote-feishu/internal/core/control"
	"github.com/kxn/codex-remote-feishu/internal/core/state"
)

func TestHandleGatewayActionPatchesHistoryCardBeforeQuery(t *testing.T) {
	gateway := &recordingGateway{}
	app := New(":0", ":0", gateway, agentproto.ServerIdentity{
		PID:       42,
		StartedAt: time.Date(2026, 4, 14, 12, 0, 0, 0, time.UTC),
	})
	var sent []agentproto.Command
	app.sendAgentCommand = func(_ string, command agentproto.Command) error {
		sent = append(sent, command)
		return nil
	}

	app.service.UpsertInstance(&state.InstanceRecord{
		InstanceID:    "inst-1",
		WorkspaceRoot: "/data/dl/droid",
		WorkspaceKey:  "/data/dl/droid",
		ShortName:     "droid",
		Online:        true,
		Threads: map[string]*state.ThreadRecord{
			"thread-1": {ThreadID: "thread-1", Name: "修复登录流程", CWD: "/data/dl/droid", Loaded: true},
		},
	})
	app.service.MaterializeSurface("surface-1", "app-1", "chat-1", "user-1")
	app.service.ApplySurfaceAction(control.Action{
		Kind:             control.ActionAttachInstance,
		GatewayID:        "app-1",
		SurfaceSessionID: "surface-1",
		ChatID:           "chat-1",
		ActorUserID:      "user-1",
		InstanceID:       "inst-1",
	})
	surface := app.service.SurfaceSnapshot("surface-1")
	if surface == nil {
		t.Fatal("expected materialized surface snapshot")
	}
	app.service.ApplySurfaceAction(control.Action{
		Kind:             control.ActionUseThread,
		GatewayID:        "app-1",
		SurfaceSessionID: "surface-1",
		ChatID:           "chat-1",
		ActorUserID:      "user-1",
		ThreadID:         "thread-1",
	})

	result := handleGatewayActionForTest(context.Background(), app, control.Action{
		Kind:             control.ActionShowHistory,
		GatewayID:        "app-1",
		SurfaceSessionID: "surface-1",
		ChatID:           "chat-1",
		ActorUserID:      "user-1",
		MessageID:        "om-card-1",
		Text:             "/history",
		Inbound: &control.ActionInboundMeta{
			CardDaemonLifecycleID: app.daemonLifecycleID,
		},
	})

	if result != nil && result.ReplaceCurrentCard != nil {
		t.Fatalf("callback must not replace the history card: %#v", result)
	}
	if len(gateway.operations) != 1 || gateway.operations[0].Kind != feishu.OperationUpdateCard ||
		gateway.operations[0].MessageID != "om-card-1" || gateway.operations[0].CardTitle != "历史记录" {
		t.Fatalf("expected one patch on the original card, got %#v", gateway.operations)
	}
	if len(sent) != 1 || sent[0].Kind != agentproto.CommandThreadHistoryRead || sent[0].Target.ThreadID != "thread-1" {
		t.Fatalf("expected history query to continue after inline replacement, got %#v", sent)
	}
	if len(app.pendingThreadHistoryReads) != 1 {
		t.Fatalf("expected pending history query to be tracked, got %#v", app.pendingThreadHistoryReads)
	}
}
