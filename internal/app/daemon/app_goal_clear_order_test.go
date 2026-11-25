package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/kxn/codex-remote-feishu/internal/adapter/codex"
	"github.com/kxn/codex-remote-feishu/internal/adapter/feishu"
	"github.com/kxn/codex-remote-feishu/internal/core/agentproto"
	"github.com/kxn/codex-remote-feishu/internal/core/control"
	"github.com/kxn/codex-remote-feishu/internal/core/frontstagecontract"
	"github.com/kxn/codex-remote-feishu/internal/core/state"
)

func TestGoalClearFastResultRemainsOnOriginalCard(t *testing.T) {
	gateway := newLifecycleGateway()
	app := New(":0", ":0", gateway, agentproto.ServerIdentity{})
	ctx := context.Background()
	const surfaceID = "feishu:app-1:user:user-1"
	const instanceID = "inst-1"
	const threadID = "thread-1"
	workspace := evalSymlinkForTest(t, t.TempDir())
	app.service.MaterializeSurfaceResume(surfaceID, "app-1", "chat-1", "user-1", state.ProductModeNormal, agentproto.BackendCodex, "", state.SurfaceVerbosityNormal, "")
	app.service.UpsertInstance(&state.InstanceRecord{
		InstanceID: instanceID, Online: true, Backend: agentproto.BackendCodex, Source: "headless", Managed: true, WorkspaceRoot: workspace,
		Threads: map[string]*state.ThreadRecord{threadID: {ThreadID: threadID, CWD: workspace, Loaded: true}},
	})
	app.service.ApplySurfaceAction(control.Action{Kind: control.ActionAttachInstance, SurfaceSessionID: surfaceID, ActorUserID: "user-1", InstanceID: instanceID})
	app.service.ApplySurfaceAction(control.Action{Kind: control.ActionUseThread, SurfaceSessionID: surfaceID, ActorUserID: "user-1", ThreadID: threadID})

	translator := codex.NewTranslator(instanceID)
	clearCommands := 0
	app.sendAgentCommand = func(_ string, command agentproto.Command) error {
		frames, err := translator.TranslateCommand(command)
		if err != nil {
			t.Fatal(err)
		}
		var req struct{ ID json.RawMessage }
		if err := json.Unmarshal(frames[0], &req); err != nil {
			t.Fatal(err)
		}
		var payload string
		switch command.Kind {
		case agentproto.CommandThreadGoalGet:
			payload = `{"goal":{"threadId":"thread-1","objective":"temporary goal","status":"paused","updatedAt":100,"createdAt":99}}`
		case agentproto.CommandThreadGoalClear:
			clearCommands++
			payload = `{"cleared":true}`
		default:
			t.Fatalf("unexpected command: %s", command.Kind)
		}
		result, err := translator.ObserveServer([]byte(fmt.Sprintf(`{"id":%s,"result":%s}`, req.ID, payload)))
		if err != nil {
			t.Fatal(err)
		}
		// The relay can report a quick app-server result before sendAgentCommand returns.
		app.onEvents(ctx, instanceID, result.Events)
		return nil
	}

	action := control.Action{Kind: control.ActionGoalCommand, SurfaceSessionID: surfaceID, GatewayID: "app-1", ChatID: "chat-1", ActorUserID: "user-1",
		MessageID: "om-user-text", Text: "/goal"}
	app.HandleGatewayAction(ctx, action)
	var status feishu.Operation
	for _, op := range gateway.snapshotOperations() {
		if op.CardTitle == "Goal" && strings.Contains(operationCardText(op), "temporary goal") {
			status = op
		}
	}
	if status.MessageID == "" {
		t.Fatal("missing Goal status card")
	}

	click := func(card feishu.Operation, arg string) *feishu.ActionResult {
		t.Helper()
		for _, button := range operationCardButtons(card) {
			value := cardButtonPayload(button)
			if value[frontstagecontract.CardActionPayloadKeyActionArg] != arg {
				continue
			}
			action.MessageID = status.MessageID
			action.Text = "/goal " + arg
			action.ThreadID, _ = value[frontstagecontract.CardActionPayloadKeyThreadID].(string)
			action.InstanceID, _ = value[frontstagecontract.CardActionPayloadKeyInstanceID].(string)
			action.GoalOperationID, _ = value[frontstagecontract.CardActionPayloadKeyGoalOperationID].(string)
			action.CatalogFamilyID, _ = value[frontstagecontract.CardActionPayloadKeyCatalogFamilyID].(string)
			action.CatalogVariantID, _ = value[frontstagecontract.CardActionPayloadKeyCatalogVariantID].(string)
			action.CatalogBackend = agentproto.BackendCodex
			action.Inbound = &control.ActionInboundMeta{CardCallback: true, CardDaemonLifecycleID: app.daemonLifecycleID}
			return app.HandleGatewayAction(ctx, action)
		}
		t.Fatalf("card %q is missing action %q", card.CardTitle, arg)
		return nil
	}

	confirmation := click(status, "clear")
	if confirmation == nil || confirmation.ReplaceCurrentCard == nil || confirmation.ReplaceCurrentCard.CardTitle != "清除 Goal" {
		t.Fatalf("the clear button did not open its confirmation on the original card: %#v", confirmation)
	}
	if clearCommands != 0 {
		t.Fatal("clear dispatched before confirmation")
	}
	click(*confirmation.ReplaceCurrentCard, "clear --confirm")
	var titles []string
	for _, op := range gateway.snapshotOperations() {
		if op.Kind == feishu.OperationUpdateCard && op.MessageID == status.MessageID {
			titles = append(titles, op.CardTitle)
		}
	}
	if clearCommands != 1 || len(titles) == 0 || titles[len(titles)-1] != "Goal 已清除" {
		t.Fatalf("want one clear and the confirmed result to remain on the original card, got commands=%d, patches=%v", clearCommands, titles)
	}
}
