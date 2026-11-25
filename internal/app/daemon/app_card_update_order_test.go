package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kxn/codex-remote-feishu/internal/adapter/feishu"
	"github.com/kxn/codex-remote-feishu/internal/core/agentproto"
	"github.com/kxn/codex-remote-feishu/internal/core/control"
	"github.com/kxn/codex-remote-feishu/internal/core/state"
)

func newCardOrderTestApp(t *testing.T) (*App, *lifecycleGateway) {
	t.Helper()
	gateway := newLifecycleGateway()
	app := New(":0", ":0", gateway, agentproto.ServerIdentity{PID: 42})
	app.service.UpsertInstance(&state.InstanceRecord{
		InstanceID: "inst-1", WorkspaceRoot: "/data/test", WorkspaceKey: "/data/test", Online: true,
		ObservedFocusedThreadID: "thread-1",
		Threads:                 map[string]*state.ThreadRecord{"thread-1": {ThreadID: "thread-1", CWD: "/data/test", Loaded: true}},
	})
	app.service.MaterializeSurface("surface-1", "app-1", "chat-1", "user-1")
	app.service.ApplySurfaceAction(control.Action{Kind: control.ActionAttachInstance, SurfaceSessionID: "surface-1", InstanceID: "inst-1"})
	app.service.ApplySurfaceAction(control.Action{Kind: control.ActionUseThread, SurfaceSessionID: "surface-1", ThreadID: "thread-1"})
	return app, gateway
}

func cardOrderTestAction(app *App, kind control.ActionKind, messageID, text string) control.Action {
	return control.Action{Kind: kind, GatewayID: "app-1", SurfaceSessionID: "surface-1", ChatID: "chat-1",
		ActorUserID: "user-1", MessageID: messageID, Text: text,
		Inbound: &control.ActionInboundMeta{CardDaemonLifecycleID: app.daemonLifecycleID}}
}

func cardOrderTestHistoryReply(command agentproto.Command) agentproto.Event {
	return agentproto.Event{Kind: agentproto.EventThreadHistoryRead, CommandID: command.CommandID, ThreadID: "thread-1",
		ThreadHistory: &agentproto.ThreadHistoryRecord{Thread: agentproto.ThreadSnapshotRecord{ThreadID: "thread-1"},
			Turns: []agentproto.ThreadHistoryTurnRecord{{TurnID: "turn-1", Status: "completed"}}}}
}

func TestHistoryDispatchFailureRemainsOnOriginalCard(t *testing.T) {
	app, gateway := newCardOrderTestApp(t)
	app.sendAgentCommand = func(_ string, command agentproto.Command) error {
		if command.Kind != agentproto.CommandThreadHistoryRead {
			t.Fatalf("unexpected command: %s", command.Kind)
		}
		return errors.New("relay unavailable")
	}
	result := handleGatewayActionForTest(context.Background(), app, cardOrderTestAction(app, control.ActionShowHistory, "om-history", "/history"))
	ops := gateway.snapshotOperations()
	if len(ops) == 0 || ops[len(ops)-1].Kind != feishu.OperationUpdateCard || ops[len(ops)-1].MessageID != "om-history" ||
		!strings.Contains(operationCardText(ops[len(ops)-1]), "未成功发送") {
		t.Fatalf("expected final failure patch on original card, got %#v", ops)
	}
	if result != nil && result.ReplaceCurrentCard != nil {
		t.Fatalf("callback must not overwrite the failure patch: %#v", result)
	}
}

func TestHistoryFastReplyUpdatesOriginalCard(t *testing.T) {
	app, gateway := newCardOrderTestApp(t)
	var commandID string
	app.sendAgentCommand = func(instanceID string, command agentproto.Command) error {
		if command.Kind != agentproto.CommandThreadHistoryRead {
			t.Fatalf("unexpected command: %s", command.Kind)
		}
		commandID = command.CommandID
		app.onCommandAck(context.Background(), instanceID, agentproto.CommandAck{CommandID: commandID, Accepted: true})
		app.onEvents(context.Background(), instanceID, []agentproto.Event{cardOrderTestHistoryReply(command)})
		return nil
	}
	result := handleGatewayActionForTest(context.Background(), app, cardOrderTestAction(app, control.ActionShowHistory, "om-history", "/history"))
	_, pending := app.pendingThreadHistoryReads[commandID]
	history := app.service.SurfaceThreadHistory("surface-1")
	ops := gateway.snapshotOperations()
	if commandID == "" || pending || history == nil || len(history.Turns) != 1 {
		t.Fatalf("fast result was lost: id=%s pending=%v history=%#v", commandID, pending, history)
	}
	if len(ops) == 0 || ops[len(ops)-1].MessageID != "om-history" || strings.Contains(operationCardText(ops[len(ops)-1]), "正在读取") {
		t.Fatalf("expected result on original card, got %#v", ops)
	}
	if result != nil && result.ReplaceCurrentCard != nil {
		t.Fatal("callback can overwrite the fast result")
	}
}

func TestRequestFastAcceptanceRemainsOnOriginalCard(t *testing.T) {
	app, gateway := newCardOrderTestApp(t)
	ctx := context.Background()
	app.onEvents(ctx, "inst-1", []agentproto.Event{
		{Kind: agentproto.EventTurnStarted, ThreadID: "thread-1", TurnID: "turn-1",
			Initiator: agentproto.Initiator{Kind: agentproto.InitiatorRemoteSurface, SurfaceSessionID: "surface-1"}},
		{Kind: agentproto.EventRequestStarted, ThreadID: "thread-1", TurnID: "turn-1", RequestID: "req-1",
			Metadata: map[string]any{"requestType": "approval", "title": "需要确认"}},
	})
	record := app.service.PendingRequest("surface-1", "req-1")
	if record == nil || record.VisibleMessageID == "" {
		t.Fatalf("request card not visible: %#v", record)
	}
	messageID := record.VisibleMessageID
	offset := len(gateway.snapshotOperations())
	app.sendAgentCommand = func(instanceID string, command agentproto.Command) error {
		if command.Kind != agentproto.CommandRequestRespond {
			t.Fatalf("unexpected command: %s", command.Kind)
		}
		app.onCommandAck(ctx, instanceID, agentproto.CommandAck{CommandID: command.CommandID, Accepted: true})
		return nil
	}
	action := cardOrderTestAction(app, control.ActionRespondRequest, messageID, "")
	action.Request = &control.ActionRequestResponse{RequestID: "req-1", RequestType: "approval", RequestOptionID: "accept", RequestRevision: record.CardRevision}
	result := handleGatewayActionForTest(ctx, app, action)
	ops := gateway.snapshotOperations()[offset:]
	if len(ops) != 2 || ops[0].Kind != feishu.OperationUpdateCard || ops[0].MessageID != messageID ||
		!strings.Contains(operationCardText(ops[0]), "正在提交当前确认") ||
		ops[1].Kind != feishu.OperationUpdateCard || ops[1].MessageID != messageID ||
		!strings.Contains(operationCardText(ops[1]), "已提交当前确认") {
		t.Fatalf("expected ordered submit and accepted patches, got %v", ops)
	}
	if result != nil && result.ReplaceCurrentCard != nil {
		t.Fatal("callback can overwrite the accepted card")
	}
}

func TestHistoryEarlierFailureDoesNotOverwriteNewResult(t *testing.T) {
	app, gateway := newCardOrderTestApp(t)
	ctx := context.Background()
	var commands []agentproto.Command
	app.sendAgentCommand = func(_ string, command agentproto.Command) error {
		commands = append(commands, command)
		return nil
	}
	handleGatewayActionForTest(ctx, app, cardOrderTestAction(app, control.ActionShowHistory, "om-old-history", "/history"))
	handleGatewayActionForTest(ctx, app, cardOrderTestAction(app, control.ActionShowHistory, "om-new-history", "/history"))
	if len(commands) != 2 {
		t.Fatalf("expected two queries, got %v", commands)
	}
	app.onEvents(ctx, "inst-1", []agentproto.Event{cardOrderTestHistoryReply(commands[1])})
	before := gateway.snapshotOperations()
	if len(before) == 0 || before[len(before)-1].MessageID != "om-new-history" {
		t.Fatalf("missing new result: %v", before)
	}
	app.onCommandAck(ctx, "inst-1", agentproto.CommandAck{CommandID: commands[0].CommandID, Error: "delayed error"})
	if ops := gateway.snapshotOperations(); len(ops) != len(before) {
		t.Fatalf("outdated failure updated a card after the new result: %v", ops[len(before):])
	}
}

func TestHistoryEarlierPageResultDoesNotUpdateNewerQuery(t *testing.T) {
	app, gateway := newCardOrderTestApp(t)
	ctx := context.Background()
	var commands []agentproto.Command
	app.sendAgentCommand = func(_ string, command agentproto.Command) error {
		commands = append(commands, command)
		return nil
	}
	handleGatewayActionForTest(ctx, app, cardOrderTestAction(app, control.ActionShowHistory, "om-history", "/history"))
	if len(commands) != 1 {
		t.Fatalf("expected first history query, got %v", commands)
	}
	pickerID := app.pendingThreadHistoryReads[commands[0].CommandID].PickerID
	app.onEvents(ctx, "inst-1", []agentproto.Event{cardOrderTestHistoryReply(commands[0])})
	page := cardOrderTestAction(app, control.ActionHistoryPage, "om-history", "")
	page.PickerID = pickerID
	handleGatewayActionForTest(ctx, app, page)
	detail := cardOrderTestAction(app, control.ActionHistoryDetail, "om-history", "")
	detail.PickerID, detail.TurnID = pickerID, "turn-1"
	handleGatewayActionForTest(ctx, app, detail)
	if len(commands) != 3 {
		t.Fatalf("expected successive page and detail queries, got %v", commands)
	}

	before := len(gateway.snapshotOperations())
	old := cardOrderTestHistoryReply(commands[1])
	old.ThreadHistory.Turns = nil
	app.onEvents(ctx, "inst-1", []agentproto.Event{old})
	if ops := gateway.snapshotOperations(); len(ops) != before {
		t.Fatalf("older page result updated the active detail card: %v", ops[before:])
	}
	app.onEvents(ctx, "inst-1", []agentproto.Event{cardOrderTestHistoryReply(commands[2])})
	ops := gateway.snapshotOperations()
	if len(ops) != before+1 || ops[before].MessageID != "om-history" ||
		!strings.Contains(operationCardText(ops[before]), "turn-1") {
		t.Fatalf("current detail result did not update the original card: %v", ops[before:])
	}
}

func TestRequestFastRejectionRestoresApprovalButtons(t *testing.T) {
	app, gateway := newCardOrderTestApp(t)
	ctx := context.Background()
	app.onEvents(ctx, "inst-1", []agentproto.Event{
		{Kind: agentproto.EventTurnStarted, ThreadID: "thread-1", TurnID: "turn-1",
			Initiator: agentproto.Initiator{Kind: agentproto.InitiatorRemoteSurface, SurfaceSessionID: "surface-1"}},
		{Kind: agentproto.EventRequestStarted, ThreadID: "thread-1", TurnID: "turn-1", RequestID: "req-2",
			Metadata: map[string]any{"requestType": "approval", "title": "需要确认"}},
	})
	record := app.service.PendingRequest("surface-1", "req-2")
	if record == nil || record.VisibleMessageID == "" {
		t.Fatalf("request card not visible: %#v", record)
	}
	messageID := record.VisibleMessageID
	offset := len(gateway.snapshotOperations())
	app.sendAgentCommand = func(instanceID string, command agentproto.Command) error {
		if command.Kind != agentproto.CommandRequestRespond {
			t.Fatalf("unexpected command: %s", command.Kind)
		}
		app.onCommandAck(ctx, instanceID, agentproto.CommandAck{CommandID: command.CommandID, Error: "temporary translator error"})
		return nil
	}
	action := cardOrderTestAction(app, control.ActionRespondRequest, messageID, "")
	action.Request = &control.ActionRequestResponse{RequestID: "req-2", RequestType: "approval", RequestOptionID: "accept", RequestRevision: record.CardRevision}
	result := handleGatewayActionForTest(ctx, app, action)
	ops := gateway.snapshotOperations()[offset:]
	if len(ops) < 2 || ops[1].Kind != feishu.OperationUpdateCard || ops[1].MessageID != messageID ||
		!operationHasActionValue(ops[1], "request_respond", "request_option_id", "accept") {
		t.Fatalf("expected restored card after rejection, got %#v", ops)
	}
	if result != nil && result.ReplaceCurrentCard != nil {
		t.Fatal("callback can hide restored approval buttons")
	}
	if restored := app.service.PendingRequest("surface-1", "req-2"); restored == nil || restored.PendingDispatchCommandID != "" {
		t.Fatalf("expected request to stay actionable on backend: %#v", restored)
	}
}
