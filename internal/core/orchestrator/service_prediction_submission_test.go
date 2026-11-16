package orchestrator

import (
	"testing"

	"github.com/kxn/codex-remote-feishu/internal/core/agentproto"
	"github.com/kxn/codex-remote-feishu/internal/core/control"
	"github.com/kxn/codex-remote-feishu/internal/core/state"
)

func TestPredictionQueuedReceiptSurvivesActivityBeforeCommandBinding(t *testing.T) {
	svc, sid, iid := predictionTestSetup(t)
	request := findAgentCommand(predictionCompleteTurn(t, svc, sid, iid), predictionRequestKind)
	svc.ApplyAgentEvent(iid, predictionTestResult(t, request.CommandID, "turn-1", "建议内容"))
	svc.ApplySurfaceAction(predictionTestAction(t, predictionAcceptKind, sid, request.CommandID, "user-1", ""))
	surface := svc.root.Surfaces[sid]
	surface.DispatchMode = state.DispatchModePausedForLocal
	action := predictionTestAction(t, predictionSendKind, sid, request.CommandID, "user-1", "排队后实际发送的正文")
	svc.ApplySurfaceAction(action)
	svc.ApplyAgentEvent(iid, agentproto.Event{Kind: agentproto.EventThreadRuntimeStatusUpdated, ThreadID: "thread-1",
		RuntimeStatus: &agentproto.ThreadRuntimeStatus{Type: agentproto.ThreadRuntimeStatusTypeActive}})
	svc.ApplyAgentEvent(iid, agentproto.Event{Kind: agentproto.EventThreadRuntimeStatusUpdated, ThreadID: "thread-1",
		RuntimeStatus: &agentproto.ThreadRuntimeStatus{Type: agentproto.ThreadRuntimeStatusTypeIdle}})
	if findAgentCommand(svc.ResumeSurfaceDispatch(sid, nil), agentproto.CommandPromptSend) == nil {
		t.Fatal("queued submission did not dispatch after resume")
	}
	svc.BindPendingRemoteCommand(sid, "queued-draft-command")
	events := svc.ApplyAgentEvent(iid, agentproto.Event{Kind: agentproto.EventTurnStarted, ThreadID: "thread-1", TurnID: "turn-2",
		Initiator: agentproto.Initiator{Kind: agentproto.InitiatorRemoteSurface, SurfaceSessionID: sid}})
	page := findPageEvent(events)
	if page == nil || page.MessageID != action.MessageID || page.StatusText != "已发送，任务已开始处理。" || len(page.Sections) != 0 {
		t.Fatal("the submitted queue lost its read-only receipt when suggestion freshness expired")
	}
}

func TestPredictionReceiptRequiresItsOwnNewTurn(t *testing.T) {
	for _, kind := range []string{"source-turn-replay", "other-command", "other-surface", "local-turn", "other-thread"} {
		t.Run(kind, func(t *testing.T) {
			svc, sid, iid := predictionTestSetup(t)
			request := findAgentCommand(predictionCompleteTurn(t, svc, sid, iid), predictionRequestKind)
			svc.ApplyAgentEvent(iid, predictionTestResult(t, request.CommandID, "turn-1", "建议内容"))
			svc.ApplySurfaceAction(predictionTestAction(t, predictionAcceptKind, sid, request.CommandID, "user-1", ""))
			action := predictionTestAction(t, predictionSendKind, sid, request.CommandID, "user-1", "原始编辑内容")
			svc.ApplySurfaceAction(action)
			svc.BindPendingRemoteCommand(sid, "draft-command")
			event := agentproto.Event{Kind: agentproto.EventTurnStarted, ThreadID: "thread-1", TurnID: "turn-2",
				Initiator: agentproto.Initiator{Kind: agentproto.InitiatorRemoteSurface, SurfaceSessionID: sid}}
			switch kind {
			case "source-turn-replay":
				event.TurnID = "turn-1"
			case "other-command":
				event.CommandID = "other-command"
			case "other-surface":
				event.Initiator.SurfaceSessionID = "other-surface"
			case "local-turn":
				event.Initiator.Kind = agentproto.InitiatorLocalUI
			case "other-thread":
				event.ThreadID = "other-thread"
			}
			for _, event := range svc.ApplyAgentEvent(iid, event) {
				if event.PageView != nil && event.PageView.MessageID == action.MessageID {
					t.Fatal("unrelated or replayed turn changed this submission receipt")
				}
			}
		})
	}
}

func TestPredictionConfirmedReceiptCannotRestoreEditorOnLateRejection(t *testing.T) {
	svc, sid, iid := predictionTestSetup(t)
	request := findAgentCommand(predictionCompleteTurn(t, svc, sid, iid), predictionRequestKind)
	svc.ApplyAgentEvent(iid, predictionTestResult(t, request.CommandID, "turn-1", "建议内容"))
	svc.ApplySurfaceAction(predictionTestAction(t, predictionAcceptKind, sid, request.CommandID, "user-1", ""))
	action := predictionTestAction(t, predictionSendKind, sid, request.CommandID, "user-1", "已开始执行的原文")
	svc.ApplySurfaceAction(action)
	svc.BindPendingRemoteCommand(sid, "draft-command")
	started := svc.ApplyAgentEvent(iid, agentproto.Event{Kind: agentproto.EventTurnStarted, ThreadID: "thread-1", TurnID: "turn-2",
		Initiator: agentproto.Initiator{Kind: agentproto.InitiatorRemoteSurface, SurfaceSessionID: sid}})
	if page := findPageEvent(started); page == nil || page.MessageID != action.MessageID || len(page.Sections) != 0 {
		t.Fatal("confirmed start did not seal the original card")
	}
	for _, event := range svc.HandleCommandRejected(iid, agentproto.CommandAck{CommandID: "draft-command"}) {
		if event.PageView != nil && len(event.PageView.Sections) != 0 {
			t.Fatal("late rejection restored a draft whose task already started")
		}
	}
	if findAgentCommand(svc.ApplySurfaceAction(control.Action{Kind: predictionSendKind, SurfaceSessionID: sid,
		ChatID: "chat-1", ActorUserID: "user-1", MessageID: action.MessageID, PredictionID: request.CommandID, Text: action.Text}), agentproto.CommandPromptSend) != nil {
		t.Fatal("late rejection allowed the old card to send again")
	}
}
