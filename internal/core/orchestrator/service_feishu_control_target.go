package orchestrator

import (
	"strings"
	"time"

	"github.com/kxn/codex-remote-feishu/internal/core/control"
	"github.com/kxn/codex-remote-feishu/internal/core/state"
)

func (s *Service) FeishuControlTargetRejection(action control.Action) *control.Notice {
	if action.Kind != control.ActionStop && action.Kind != control.ActionDetach {
		return nil
	}
	if action.Inbound == nil || action.GatewayID == "" || !strings.HasPrefix(action.SurfaceSessionID, "feishu:") {
		return nil
	}
	sentAt := feishuControlActionTime(action.Inbound)
	if !sentAt.IsZero() && s.feishuControlTargetIsCurrent(s.root.Surfaces[action.SurfaceSessionID], sentAt) {
		return nil
	}
	command := "/stop"
	if action.Kind == control.ActionDetach {
		command = "/detach"
	}
	return &control.Notice{
		Code:     "feishu_control_target_unconfirmed",
		Title:    "请重新发送命令",
		Text:     "无法确认这次操作仍对应原来的接管或任务。请重新发送 " + command + "。",
		ThemeKey: "system",
	}
}

func feishuControlActionTime(meta *control.ActionInboundMeta) time.Time {
	switch {
	case meta.CardCallback:
		return meta.EventCreateTime
	case !meta.MenuClickTime.IsZero():
		return meta.MenuClickTime
	default:
		return meta.MessageCreateTime
	}
}

func (s *Service) feishuControlTargetIsCurrent(surface *state.SurfaceConsoleRecord, sentAt time.Time) bool {
	if surface == nil {
		return false
	}
	// Newer inbound work may have added drafts, queue entries or requests that
	// the command would also clear.
	if sentAt.Before(surface.ControlTargetChangedAt) || sentAt.Before(surface.LastInboundAt) {
		return false
	}
	if pending := s.pendingSurfaceHeadlessLaunch(surface, ""); pending != nil &&
		(pending.RequestedAt.IsZero() || sentAt.Before(pending.RequestedAt)) {
		return false
	}
	if binding := s.activeRemoteBindingForSurface(surface); binding != nil &&
		(binding.StartedAt.IsZero() || sentAt.Before(binding.StartedAt)) {
		return false
	}
	inst := s.root.Instances[surface.AttachedInstanceID]
	if inst == nil {
		return surface.AttachedInstanceID == ""
	}
	if inst.ActiveTurnID != "" && inst.LastTurnStartedAt.IsZero() {
		return false
	}
	return !sentAt.Before(inst.LastTurnStartedAt)
}
