package codex

import (
	"fmt"

	"github.com/kxn/codex-remote-feishu/internal/core/agentproto"
	"github.com/kxn/codex-remote-feishu/internal/xutil"
)

type localServiceTierRequest struct {
	threadID string
	tier     string
}

func (t *Translator) rememberLocalServiceTierRequest(message map[string]any, threadID, tier string) {
	if requestID, ok := message["id"]; ok && !t.internalThreadIDs[threadID] {
		t.pendingLocalServiceTiers[fmt.Sprint(requestID)] = localServiceTierRequest{threadID: threadID, tier: tier}
	}
}

func (t *Translator) observeServiceTierResponse(requestID string, message map[string]any) []agentproto.Event {
	if pending, ok := t.pendingLocalServiceTiers[requestID]; ok {
		delete(t.pendingLocalServiceTiers, requestID)
		return t.serviceTierResponseEvents(message, pending.threadID, pending.tier, agentproto.Initiator{Kind: agentproto.InitiatorLocalUI})
	}
	if pending, ok := t.pendingSuppressedResponse[requestID]; ok && pending.ServiceTier != "" {
		initiator := agentproto.Initiator{Kind: agentproto.InitiatorRemoteSurface, SurfaceSessionID: pending.SurfaceSessionID}
		return t.serviceTierResponseEvents(message, pending.ThreadID, pending.ServiceTier, initiator)
	}
	if t.pendingInternalThreadSet[requestID] {
		return nil
	}
	if pending, ok := t.pendingThreadCreate[requestID]; ok && pending.Action == "thread/start" {
		return t.serviceTierResponseEvents(message, "", "", agentproto.Initiator{})
	}
	if pending, ok := t.pendingThreadResume[requestID]; ok {
		return t.serviceTierResponseEvents(message, pending.ThreadID, "", agentproto.Initiator{})
	}
	if pending, ok := t.pendingChildRestartRestore[requestID]; ok {
		return t.serviceTierResponseEvents(message, pending.ThreadID, "", agentproto.Initiator{})
	}
	return nil
}

func (t *Translator) serviceTierResponseEvents(message map[string]any, threadID, requestedTier string, initiator agentproto.Initiator) []agentproto.Event {
	result := lookupMap(message, "result")
	if len(result) == 0 || message["error"] != nil {
		return nil
	}
	tier := requestedTier
	turnID := ""
	if requestedTier != "" {
		turnID = lookupString(result, "turn", "id")
		if turnID == "" {
			return nil
		}
	} else {
		reportedThreadID := lookupString(result, "thread", "id")
		if threadID != "" && reportedThreadID != "" && threadID != reportedThreadID {
			return nil
		}
		threadID = choose(threadID, reportedThreadID)
		tier = nativeServiceTier(result)
	}
	if threadID == "" || tier == "" || t.internalThreadIDs[threadID] {
		return nil
	}
	return []agentproto.Event{{
		Kind:           agentproto.EventThreadSettingsUpdated,
		ThreadID:       threadID,
		TurnID:         turnID,
		Initiator:      initiator,
		ThreadSettings: &agentproto.ThreadSettingsUpdate{ThreadID: threadID, ServiceTier: tier},
	}}
}

func nativeServiceTier(settings map[string]any) string {
	value, exists := settings["serviceTier"]
	if exists && value == nil {
		return "default"
	}
	return agentproto.NormalizeCodexServiceTier(xutil.LookupStringFromAny(value))
}
