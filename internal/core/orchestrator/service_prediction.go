package orchestrator

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kxn/codex-remote-feishu/internal/core/agentproto"
	"github.com/kxn/codex-remote-feishu/internal/core/control"
	"github.com/kxn/codex-remote-feishu/internal/core/eventcontract"
	"github.com/kxn/codex-remote-feishu/internal/core/state"
)

const predictionTimeout = 30 * time.Second
const predictionDraftTTL = 10 * time.Minute

// One ephemeral record per surface also remembers consumed/expired source turns.
type predictionRecord struct {
	ID, SurfaceID, InstanceID, ThreadID, SourceTurnID, ActorUserID string
	StartedAt                                                      time.Time
	Suggestion                                                     string
	Pending, Draft, Canceled                                       bool
	EditedText, MessageID, SendQueueItemID, SendCommandID          string
	Submitting                                                     bool
}

func (s *Service) predictionTargetCurrent(p *predictionRecord) bool {
	if p == nil || p.Canceled {
		return false
	}
	surface := s.root.Surfaces[p.SurfaceID]
	ownSubmission := predictionOwnsSubmissionQueue(p, surface)
	if surface == nil || surface.AttachedInstanceID != p.InstanceID || surface.SelectedThreadID != p.ThreadID ||
		!s.surfaceIsHeadless(surface) || s.surfaceBackend(surface) != agentproto.BackendCodex || surface.Abandoning ||
		(surface.RouteMode != state.RouteModePinned && surface.RouteMode != state.RouteModeFollowLocal) ||
		surface.PendingHeadless != nil || (!ownSubmission && (surface.ActiveQueueItemID != "" || len(surface.QueuedQueueItemIDs) != 0)) || activePendingRequest(surface) != nil {
		return false
	}
	inst := s.root.Instances[p.InstanceID]
	if inst == nil || !inst.Online || inst.ActiveTurnID != "" || (!ownSubmission && s.surfaceHasLiveRemoteWork(surface)) {
		return false
	}
	thread := inst.Threads[p.ThreadID]
	if !ordinaryThreadVisible(thread) || thread.ForkedFromID != "" || threadRuntimeActive(thread) {
		return false
	}
	if source := thread.Source; source != nil {
		if source.ParentThreadID != "" {
			return false
		}
		switch source.Kind {
		case agentproto.ThreadSourceKindCompact, agentproto.ThreadSourceKindThreadSpawn,
			agentproto.ThreadSourceKindMemoryConsolidation, agentproto.ThreadSourceKindSubAgentOther:
			return false
		}
	}
	if goal := thread.ThreadGoal; goal != nil && !goal.Cleared && goal.Status == "active" {
		return false
	}
	if ui := s.surfaceUIRuntimeState(surface); ui != nil {
		for _, question := range ui.AsyncUserInputs {
			if question != nil && question.ThreadID == p.ThreadID && !question.Submitted {
				return false
			}
		}
	}
	return true
}

func (s *Service) maybeRequestPrediction(outcome *remoteTurnOutcome) []eventcontract.Event {
	if outcome == nil || outcome.Surface == nil || outcome.Item == nil || outcome.Item.SourceKind != state.QueueItemSourceUser || outcome.Status != "completed" || outcome.ErrorMessage != "" || outcome.Problem != nil {
		return nil
	}
	surface := outcome.Surface
	inst := s.root.Instances[outcome.InstanceID]
	if inst == nil || !inst.CapabilitiesDeclared || !inst.Capabilities.ThreadPrediction || s.predictionUnsupported[outcome.InstanceID] {
		return nil
	}
	if old := s.predictions[surface.SurfaceSessionID]; old != nil && old.InstanceID == outcome.InstanceID && old.SourceTurnID == outcome.TurnID {
		return nil
	}
	p := &predictionRecord{ID: "prediction:" + s.nextAgentCommandID(), SurfaceID: surface.SurfaceSessionID,
		InstanceID: outcome.InstanceID, ThreadID: outcome.ThreadID, SourceTurnID: outcome.TurnID,
		ActorUserID: outcome.Item.ActorUserID, StartedAt: s.now(), Pending: true}
	if p.ActorUserID == "" || p.SourceTurnID == "" || !s.predictionTargetCurrent(p) {
		return nil
	}
	if s.predictions == nil {
		s.predictions = map[string]*predictionRecord{}
	}
	s.predictions[p.SurfaceID] = p
	return []eventcontract.Event{predictionCommandEvent(p, agentproto.CommandThreadPredictionRequest, p.ID)}
}

func predictionCommandEvent(p *predictionRecord, kind agentproto.CommandKind, id string) eventcontract.Event {
	return eventcontract.Event{Kind: eventcontract.KindAgentCommand, SurfaceSessionID: p.SurfaceID,
		Command: &agentproto.Command{CommandID: id, Kind: kind,
			Origin:     agentproto.Origin{Surface: p.SurfaceID, UserID: p.ActorUserID},
			Target:     agentproto.Target{ThreadID: p.ThreadID},
			Prediction: agentproto.PredictionCommand{InstanceID: p.InstanceID, SourceTurnID: p.SourceTurnID, RequestCommandID: p.ID}}}
}

func (s *Service) invalidatePrediction(p *predictionRecord) []eventcontract.Event {
	if p == nil || p.Canceled {
		return nil
	}
	p.Canceled = true
	p.Suggestion = ""
	p.EditedText = ""
	if !p.Pending {
		return nil
	}
	p.Pending = false
	return []eventcontract.Event{predictionCommandEvent(p, agentproto.CommandThreadPredictionCancel, "prediction-cancel:"+p.ID)}
}

func (s *Service) predictionActionPrelude(surface *state.SurfaceConsoleRecord, action control.Action) []eventcontract.Event {
	p := s.predictions[surface.SurfaceSessionID]
	// Only the recursive ordinary-text dispatch retains the draft's token.
	if action.Kind == control.ActionTextMessage && p != nil && p.Submitting && !p.Canceled &&
		action.PredictionID == p.ID && action.ActorUserID == p.ActorUserID && action.MessageID == p.MessageID && action.Text == p.EditedText {
		return nil
	}
	switch action.Kind {
	case control.ActionPredictionAccept, control.ActionPredictionSend, control.ActionStatus,
		control.ActionReactionCreated, control.ActionMessageRecalled:
		return nil
	default:
		return s.invalidatePrediction(s.predictions[surface.SurfaceSessionID])
	}
}

func (s *Service) invalidateInstancePredictions(instanceID string) []eventcontract.Event {
	var events []eventcontract.Event
	for _, p := range s.predictions {
		if p.InstanceID == instanceID {
			events = append(events, s.invalidatePrediction(p)...)
		}
	}
	return events
}

func (s *Service) forgetInstancePredictions(instanceID string) {
	s.invalidateInstancePredictions(instanceID)
	delete(s.predictionUnsupported, instanceID)
}

func (s *Service) predictionEventPrelude(instanceID string, event agentproto.Event) []eventcontract.Event {
	if event.Kind == agentproto.EventTurnStarted || event.Kind == agentproto.EventThreadFocused {
		return s.invalidateInstancePredictions(instanceID)
	}
	var events []eventcontract.Event
	nativeActive := event.Kind == agentproto.EventThreadRuntimeStatusUpdated && event.RuntimeStatus != nil && event.RuntimeStatus.Type == agentproto.ThreadRuntimeStatusTypeActive
	if event.Kind == agentproto.EventTurnCompleted || nativeActive {
		for _, p := range s.predictions {
			if p.InstanceID == instanceID && p.ThreadID == event.ThreadID && (nativeActive || p.SourceTurnID != event.TurnID) {
				events = append(events, s.invalidatePrediction(p)...)
			}
		}
	}
	return events
}

func (s *Service) expirePredictions(now time.Time) []eventcontract.Event {
	var events []eventcontract.Event
	for _, p := range s.predictions {
		if p.Canceled {
			continue
		}
		limit := predictionDraftTTL
		if p.Pending {
			limit = predictionTimeout
		}
		if now.Sub(p.StartedAt) >= limit || !s.predictionTargetCurrent(p) {
			events = append(events, s.invalidatePrediction(p)...)
		}
	}
	return events
}

func (s *Service) applyPredictionResult(instanceID string, event agentproto.Event) []eventcontract.Event {
	for _, p := range s.predictions {
		if p.ID != event.CommandID || p.InstanceID != instanceID || p.ThreadID != event.ThreadID || !p.Pending || p.Canceled {
			continue
		}
		p.Pending = false
		if !s.predictionTargetCurrent(p) || s.now().Sub(p.StartedAt) >= predictionTimeout {
			p.Canceled = true
			return nil
		}
		if result := event.Prediction; result != nil && result.Unsupported {
			p.Canceled = true
			if s.predictionUnsupported == nil {
				s.predictionUnsupported = map[string]bool{}
			}
			if s.predictionUnsupported[instanceID] {
				return nil
			}
			s.predictionUnsupported[instanceID] = true
			return notice(s.root.Surfaces[p.SurfaceID], "predictions_unsupported", "当前 Codex 不支持下一句建议；本次连接已停止请求建议。普通对话可以继续。")
		}
		if event.ErrorMessage != "" || event.Prediction == nil || event.Prediction.SourceTurnID != p.SourceTurnID {
			p.Canceled = true
			return nil
		}
		p.Suggestion = strings.TrimSpace(event.Prediction.Suggestion)
		if p.Suggestion == "" || utf8.RuneCountInString(p.Suggestion) > 200 {
			p.Canceled = true
			return nil
		}
		return []eventcontract.Event{s.pageEvent(s.root.Surfaces[p.SurfaceID], predictionSuggestionPage(p))}
	}
	return nil
}

func (s *Service) handlePredictionAction(surface *state.SurfaceConsoleRecord, action control.Action) []eventcontract.Event {
	p := s.predictions[surface.SurfaceSessionID]
	if p == nil || p.ID != action.PredictionID || p.ActorUserID != action.ActorUserID || p.Pending || p.Suggestion == "" ||
		!s.predictionTargetCurrent(p) || s.now().Sub(p.StartedAt) >= predictionDraftTTL {
		return notice(surface, "prediction_expired", "这条建议已失效或不属于当前用户，请继续直接输入消息。")
	}
	if p.Submitting {
		return notice(surface, "prediction_submitting", "这条消息已在排队或发送中，请等待结果。")
	}
	if action.Kind == control.ActionPredictionAccept {
		p.Draft = true
		p.MessageID = action.MessageID
		return []eventcontract.Event{s.pageEvent(surface, predictionDraftPage(p))}
	}
	if !p.Draft {
		return notice(surface, "prediction_not_adopted", "请先采用建议并编辑，再点击发送。")
	}
	text := strings.TrimSpace(action.Text)
	if text == "" {
		return notice(surface, "prediction_empty", "请输入要发送的内容。")
	}
	p.EditedText, p.MessageID, p.Submitting = text, action.MessageID, true
	action.Kind = control.ActionTextMessage
	action.Text = text
	action.Inbound = nil
	action.LocalPageAction = false
	events := s.ApplySurfaceAction(action)
	if p.SendQueueItemID == "" {
		p.Submitting = false
		if s.predictionTargetCurrent(p) {
			events = append(events, s.pageEvent(surface, predictionDraftPage(p)))
		}
	}
	return events
}

func predictionOwnsSubmissionQueue(p *predictionRecord, surface *state.SurfaceConsoleRecord) bool {
	if p == nil || surface == nil || !p.Submitting || p.SendQueueItemID == "" {
		return false
	}
	item := surface.QueueItems[p.SendQueueItemID]
	if item == nil || (item.Status != state.QueueItemQueued && item.Status != state.QueueItemDispatching) {
		return false
	}
	if surface.ActiveQueueItemID != "" && surface.ActiveQueueItemID != item.ID {
		return false
	}
	for _, id := range surface.QueuedQueueItemIDs {
		if id != item.ID {
			return false
		}
	}
	return true
}

func (s *Service) bindPredictionQueueItem(surface *state.SurfaceConsoleRecord, item *state.QueueItemRecord) {
	p := s.predictions[surface.SurfaceSessionID]
	if p != nil && !p.Canceled && p.Submitting && p.SendQueueItemID == "" &&
		item.SourceKind == state.QueueItemSourceUser && item.ActorUserID == p.ActorUserID && item.SourceMessageID == p.MessageID {
		p.SendQueueItemID = item.ID
	}
}

func (s *Service) bindPredictionSendCommand(surface *state.SurfaceConsoleRecord, commandID string) {
	p := s.predictions[surface.SurfaceSessionID]
	if p != nil && !p.Canceled && p.Submitting && p.SendQueueItemID != "" && surface.ActiveQueueItemID == p.SendQueueItemID {
		p.SendCommandID = commandID
	}
}

func (s *Service) restorePredictionDraftAfterDispatchFailure(surfaceID, commandID string) []eventcontract.Event {
	p := s.predictions[surfaceID]
	surface := s.root.Surfaces[surfaceID]
	if p == nil || surface == nil || p.Canceled || !p.Submitting || p.SendCommandID == "" || p.SendCommandID != commandID {
		return nil
	}
	item := surface.QueueItems[p.SendQueueItemID]
	if item == nil || item.Status != state.QueueItemFailed {
		return nil
	}
	p.Submitting = false
	p.SendQueueItemID, p.SendCommandID = "", ""
	if !s.predictionTargetCurrent(p) || s.now().Sub(p.StartedAt) >= predictionDraftTTL {
		s.invalidatePrediction(p)
		return nil
	}
	return []eventcontract.Event{s.pageEvent(surface, predictionDraftPage(p))}
}

func (s *Service) failPredictionDispatch(commandID string) bool {
	if strings.HasPrefix(commandID, "prediction-cancel:") {
		return true
	}
	if !strings.HasPrefix(commandID, "prediction:") {
		return false
	}
	for _, p := range s.predictions {
		if p.ID == commandID {
			p.Canceled = true
			p.Pending = false
			p.Suggestion = ""
		}
	}
	return true
}
