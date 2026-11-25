package orchestrator

import (
	"strings"

	"github.com/kxn/codex-remote-feishu/internal/core/agentproto"
	"github.com/kxn/codex-remote-feishu/internal/core/control"
	"github.com/kxn/codex-remote-feishu/internal/core/eventcontract"
	"github.com/kxn/codex-remote-feishu/internal/core/frontstagecontract"
	"github.com/kxn/codex-remote-feishu/internal/core/state"
)

func (s *Service) handleFastCommand(surface *state.SurfaceConsoleRecord, action control.Action) []eventcontract.Event {
	if !s.surfaceIsHeadless(surface) || s.surfaceBackend(surface) != agentproto.BackendCodex {
		return notice(surface, "fast_backend_unsupported", "速度档位适用于 Codex 会话。请切到 Codex 模式后再试。")
	}
	inst := s.root.Instances[surface.AttachedInstanceID]
	threadID := strings.TrimSpace(surface.SelectedThreadID)
	if inst == nil || !inst.Online || threadID == "" || state.EffectiveInstanceBackend(inst) != agentproto.BackendCodex {
		return notice(surface, "fast_thread_missing", "请先用 /useall 选中一个可用的 Codex 会话。")
	}
	if thread := inst.Threads[threadID]; thread != nil && !ordinaryThreadVisible(thread) {
		return notice(surface, "fast_thread_unsupported", "当前会话无法切换速度档位。请用 /useall 选中一个普通 Codex 会话。")
	}

	choice := strings.ToLower(control.FeishuActionArgumentText(action.Text))
	switch choice {
	case "":
		return []eventcontract.Event{s.fastPageEvent(surface, inst, "")}
	case "on", "off":
		// A menu entry can open the page, but only its scoped buttons can mutate the setting.
		fromCard := action.IsCardAction() || (action.Inbound != nil && action.Inbound.CardCallback)
		if fromCard && (action.ThreadID != threadID || action.InstanceID != inst.InstanceID) {
			return notice(surface, "fast_card_expired", "这张速度卡已失效，请重新发送 /fast。")
		}
		if len(surface.QueuedQueueItemIDs) != 0 {
			return notice(surface, "fast_queue_pending", "还有消息排队，请等它们开始执行后再切换速度档位。")
		}
	default:
		return notice(surface, "fast_argument_invalid", "使用 /fast 查看档位，/fast on 开启 Fast，/fast off 切回标准。")
	}

	tier := "default"
	if choice == "on" {
		if s.codexModelDoesNotOfferFast(surface, inst, threadID, "", state.ModelConfigRecord{}) {
			return fastModelUnsupportedNotice(surface)
		}
		tier = "fast"
	}
	surface.PendingCodexServiceTier = state.CodexServiceTierRequest{InstanceID: inst.InstanceID, ThreadID: threadID, Tier: tier}
	return []eventcontract.Event{s.fastPageEvent(surface, inst, "将在下条消息请求切换到 "+control.CodexServiceTierLabel(tier)+"。")}
}

func fastModelUnsupportedNotice(surface *state.SurfaceConsoleRecord) []eventcontract.Event {
	return notice(surface, "fast_model_unsupported", "Codex 的模型列表显示当前模型不支持 Fast。")
}

func (s *Service) codexModelDoesNotOfferFast(surface *state.SurfaceConsoleRecord, inst *state.InstanceRecord, threadID, cwd string, override state.ModelConfigRecord) bool {
	if inst == nil || inst.ModelCatalog == nil || inst.ModelCatalog.Unsupported {
		return false
	}
	model := s.resolvePromptConfig(inst, surface, threadID, cwd, override).EffectiveModel.Value
	entry, found := modelCatalogEntryForModel(inst, model)
	if !found || codexModelOffersFast(entry) {
		return false
	}
	// Older catalogs may omit tier metadata altogether.
	for _, candidate := range inst.ModelCatalog.Entries {
		if codexModelOffersFast(candidate) {
			return true
		}
	}
	return false
}

func codexModelOffersFast(entry agentproto.ModelCatalogEntry) bool {
	if agentproto.NormalizeCodexServiceTier(entry.DefaultServiceTier) == "fast" {
		return true
	}
	for _, tier := range entry.ServiceTiers {
		if agentproto.NormalizeCodexServiceTier(tier.ID) == "fast" {
			return true
		}
	}
	return false
}

func (s *Service) fastPageEvent(surface *state.SurfaceConsoleRecord, inst *state.InstanceRecord, status string) eventcontract.Event {
	current, pending := snapshotCodexServiceTiers(surface, inst)
	lines := []string{"当前档位：" + control.CodexServiceTierLabel(current)}
	if pending != "" && pending != current {
		lines = append(lines, "下条消息："+control.CodexServiceTierLabel(pending))
	}
	buttons := []control.CommandCatalogButton{}
	for _, option := range []struct{ label, command string }{
		{"开启 Fast", "/fast on"},
		{"切回标准", "/fast off"},
	} {
		resolved, ok := control.ResolveFeishuTextCommand(s.buildCatalogContext(surface), option.command)
		if !ok {
			continue
		}
		payload := frontstagecontract.ActionPayloadPageAction(string(control.ActionFastCommand), control.FeishuActionArgumentText(option.command))
		payload[frontstagecontract.CardActionPayloadKeyThreadID] = surface.SelectedThreadID
		payload[frontstagecontract.CardActionPayloadKeyInstanceID] = inst.InstanceID
		payload = frontstagecontract.ActionPayloadWithCatalog(payload, resolved.FamilyID, resolved.VariantID, string(resolved.Backend))
		buttons = append(buttons, control.CommandCatalogButton{
			Label: option.label, Kind: control.CommandCatalogButtonCallbackAction,
			CommandText: option.command, CommandID: control.FeishuCommandFast, CallbackValue: payload,
		})
	}
	view := control.FeishuPageView{
		PageID: control.FeishuCommandFast, CommandID: control.FeishuCommandFast,
		Title: "速度档位", Interactive: true, StatusText: status,
		BodySections:   []control.FeishuCardTextSection{{Lines: lines}},
		RelatedButtons: buttons,
	}
	return s.pageEvent(surface, control.NormalizeFeishuPageView(view))
}

func snapshotCodexServiceTiers(surface *state.SurfaceConsoleRecord, inst *state.InstanceRecord) (current, pending string) {
	if inst == nil || state.EffectiveInstanceBackend(inst) != agentproto.BackendCodex || surface.SelectedThreadID == "" {
		return "", ""
	}
	if thread := inst.Threads[surface.SelectedThreadID]; thread != nil && thread.ThreadSettings != nil {
		current = thread.ThreadSettings.ServiceTier
	}
	return current, pendingCodexServiceTier(surface, inst.InstanceID, surface.SelectedThreadID)
}

func pendingCodexServiceTier(surface *state.SurfaceConsoleRecord, instanceID, threadID string) string {
	request := surface.PendingCodexServiceTier
	if threadID == "" || request.ThreadID != threadID || surface.SelectedThreadID != threadID ||
		request.InstanceID != instanceID || surface.AttachedInstanceID != instanceID || surface.Abandoning {
		return ""
	}
	return request.Tier
}

func clearStaleCodexServiceTierRequest(surface *state.SurfaceConsoleRecord) {
	if surface.PendingCodexServiceTier.Tier != "" && pendingCodexServiceTier(surface, surface.AttachedInstanceID, surface.SelectedThreadID) == "" {
		surface.PendingCodexServiceTier = state.CodexServiceTierRequest{}
	}
}
