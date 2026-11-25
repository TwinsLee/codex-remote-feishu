package daemon

import (
	"context"
	"testing"

	"github.com/kxn/codex-remote-feishu/internal/core/agentproto"
	"github.com/kxn/codex-remote-feishu/internal/core/control"
	"github.com/kxn/codex-remote-feishu/internal/core/state"
)

func TestFastCallbackReplacesSpeedCard(t *testing.T) {
	gateway := &recordingGateway{}
	app := New(":0", ":0", gateway, agentproto.ServerIdentity{})
	const sid = "feishu:app-1:user:user-1"
	const instanceID = "inst-1"
	const threadID = "thread-1"
	root := evalSymlinkForTest(t, t.TempDir())
	app.service.MaterializeSurfaceResume(sid, "app-1", "chat-1", "user-1", state.ProductModeNormal, agentproto.BackendCodex, "", state.SurfaceVerbosityNormal, "")
	app.service.UpsertInstance(&state.InstanceRecord{
		InstanceID: instanceID, Online: true, Backend: agentproto.BackendCodex, Source: "headless", Managed: true, WorkspaceRoot: root,
		Threads: map[string]*state.ThreadRecord{threadID: {ThreadID: threadID, CWD: root, Loaded: true}},
	})
	app.service.ApplySurfaceAction(control.Action{Kind: control.ActionAttachInstance, SurfaceSessionID: sid, ActorUserID: "user-1", InstanceID: instanceID})
	app.service.ApplySurfaceAction(control.Action{Kind: control.ActionUseThread, SurfaceSessionID: sid, ActorUserID: "user-1", ThreadID: threadID})

	resolved, ok := control.ResolveFeishuTextCommand(control.CatalogContext{Backend: agentproto.BackendCodex}, "/fast on")
	if !ok {
		t.Fatal("Fast command missing from the Codex catalog")
	}
	action := control.Action{
		Kind: control.ActionFastCommand, Text: "/fast on", SurfaceSessionID: sid, GatewayID: "app-1", ChatID: "chat-1", ActorUserID: "user-1",
		MessageID: "om-fast", ThreadID: threadID, InstanceID: instanceID,
		CatalogFamilyID: resolved.FamilyID, CatalogVariantID: resolved.VariantID, CatalogBackend: resolved.Backend,
		Inbound: &control.ActionInboundMeta{CardCallback: true, CardDaemonLifecycleID: app.daemonLifecycleID},
	}
	result := app.HandleGatewayAction(context.Background(), action)
	if result == nil || result.ReplaceCurrentCard == nil || result.ReplaceCurrentCard.CardTitle != "速度档位" {
		t.Fatalf("expected a synchronous speed card replacement, got %#v", result)
	}
	if len(gateway.operations) != 0 {
		t.Fatalf("speed card also emitted %d asynchronous operations", len(gateway.operations))
	}
	snapshot := app.service.SurfaceSnapshot(sid)
	if snapshot == nil || snapshot.Attachment.PendingCodexServiceTier != "fast" {
		t.Fatalf("the selected session did not queue Fast: %#v", snapshot)
	}
}
