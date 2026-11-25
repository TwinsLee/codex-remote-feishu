package daemon

import (
	"strings"

	"github.com/kxn/codex-remote-feishu/internal/core/agentproto"
	"github.com/kxn/codex-remote-feishu/internal/core/control"
	"github.com/kxn/codex-remote-feishu/internal/core/eventcontract"
)

func (a *App) handleThreadHistoryDaemonCommand(command control.DaemonCommand) []eventcontract.Event {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.shuttingDown {
		return nil
	}
	return a.handleThreadHistoryDaemonCommandLocked(command)
}

func (a *App) handleThreadHistoryDaemonCommandLocked(command control.DaemonCommand) []eventcontract.Event {
	surfaceID := strings.TrimSpace(command.SurfaceSessionID)
	instanceID := strings.TrimSpace(command.InstanceID)
	threadID := strings.TrimSpace(command.ThreadID)
	if surfaceID == "" || instanceID == "" || threadID == "" || command.PickerID == "" || command.ThreadHistoryRevision <= 0 {
		return []eventcontract.Event{{
			Kind:             eventcontract.KindNotice,
			SurfaceSessionID: surfaceID,
			Notice: &control.Notice{
				Code: "history_query_invalid",
				Text: "history 查询参数不完整。",
			},
		}}
	}
	pending := pendingThreadHistoryRead{
		SurfaceSessionID: surfaceID,
		InstanceID:       instanceID,
		ThreadID:         threadID,
		PickerID:         command.PickerID,
		Revision:         command.ThreadHistoryRevision,
	}
	if !a.isCurrentThreadHistoryRead(pending) {
		return nil
	}

	commandID := a.nextCommandID()
	agentCommand := agentproto.Command{
		CommandID: commandID,
		Kind:      agentproto.CommandThreadHistoryRead,
		Origin: agentproto.Origin{
			Surface:   surfaceID,
			ChatID:    command.SourceMessageID,
			MessageID: command.SourceMessageID,
		},
		Target: agentproto.Target{
			ThreadID: threadID,
		},
	}
	a.pendingThreadHistoryReads[commandID] = pending
	a.mu.Unlock()
	err := a.sendAgentCommand(instanceID, agentCommand)
	a.mu.Lock()
	if _, waiting := a.pendingThreadHistoryReads[commandID]; err != nil && waiting {
		delete(a.pendingThreadHistoryReads, commandID)
		if a.isCurrentThreadHistoryRead(pending) {
			return a.service.HandleSurfaceThreadHistoryFailure(surfaceID, "history_query_dispatch_failed", "history 查询未成功发送到本地 Codex。")
		}
	}
	return nil
}

func (a *App) isCurrentThreadHistoryRead(pending pendingThreadHistoryRead) bool {
	return a.service.IsCurrentThreadHistoryQuery(pending.SurfaceSessionID, pending.InstanceID, pending.PickerID, pending.Revision)
}

func (a *App) handleThreadHistoryCommandAckLocked(instanceID string, ack agentproto.CommandAck) ([]eventcontract.Event, bool) {
	commandID := strings.TrimSpace(ack.CommandID)
	pending, ok := a.pendingThreadHistoryReads[commandID]
	if !ok {
		return nil, false
	}
	if pending.InstanceID != strings.TrimSpace(instanceID) || ack.Accepted {
		return nil, true
	}
	delete(a.pendingThreadHistoryReads, commandID)
	if !a.isCurrentThreadHistoryRead(pending) {
		return nil, true
	}
	return a.service.HandleSurfaceThreadHistoryFailure(pending.SurfaceSessionID, "history_query_rejected", "本地 Codex 拒绝了这次 history 查询。"), true
}

func (a *App) handleThreadHistoryEventLocked(instanceID string, event agentproto.Event) ([]eventcontract.Event, bool) {
	if event.Kind != agentproto.EventThreadHistoryRead || strings.TrimSpace(event.CommandID) == "" || event.ThreadHistory == nil {
		return nil, false
	}
	pending, ok := a.pendingThreadHistoryReads[strings.TrimSpace(event.CommandID)]
	if !ok {
		return nil, false
	}
	if pending.InstanceID != strings.TrimSpace(instanceID) {
		return nil, true
	}
	delete(a.pendingThreadHistoryReads, strings.TrimSpace(event.CommandID))
	if !a.isCurrentThreadHistoryRead(pending) {
		return nil, true
	}
	if pending.ThreadID != strings.TrimSpace(event.ThreadID) && pending.ThreadID != strings.TrimSpace(event.ThreadHistory.Thread.ThreadID) {
		return a.service.HandleSurfaceThreadHistoryFailure(pending.SurfaceSessionID, "history_query_stale", "history 查询结果已过期。"), true
	}
	a.service.RecordSurfaceThreadHistory(pending.SurfaceSessionID, *event.ThreadHistory)
	return a.service.HandleSurfaceThreadHistoryLoaded(pending.SurfaceSessionID), true
}
