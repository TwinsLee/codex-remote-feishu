package orchestrator

import (
	"time"

	"github.com/kxn/codex-remote-feishu/internal/core/agentproto"
	"github.com/kxn/codex-remote-feishu/internal/core/control"
	"github.com/kxn/codex-remote-feishu/internal/core/eventcontract"
	"github.com/kxn/codex-remote-feishu/internal/core/state"
)

func (s *Service) beginDesktopHandoff(surface *state.SurfaceConsoleRecord, action control.Action) []eventcontract.Event {
	inst := s.root.Instances[surface.AttachedInstanceID]
	if !s.surfaceIsHeadless(surface) || inst == nil || !inst.Online || !state.IsManagedHeadlessInstance(inst) || inst.PID <= 0 || agentproto.NormalizeBackend(inst.Backend) != agentproto.BackendCodex {
		return notice(surface, "desk_unavailable", "当前没有可交给桌面的 Remote 托管 Codex 会话。请先用 /use 选中会话；其他入口的进程不会被结束。")
	}
	if s.surfaceHasLiveRemoteWork(surface) || s.surfaceNeedsDelayedDetach(surface, inst) || s.surfaceHasRouteMutationBlocker(surface) || inst.ActiveTurnID != "" || surface.ReviewSession != nil && surface.ReviewSession.Phase == state.ReviewSessionPhasePending {
		return notice(surface, "desk_busy", "当前还有工作、排队输入或确认需要处理。请等本轮结束后再发 /desk；当前任务没有被中断。")
	}
	for _, thread := range inst.Threads {
		if thread == nil {
			continue
		}
		if thread.RuntimeStatus != nil && thread.RuntimeStatus.Type == agentproto.ThreadRuntimeStatusTypeActive || thread.ThreadGoal != nil && !thread.ThreadGoal.Cleared && thread.ThreadGoal.Status == "active" {
			return notice(surface, "desk_busy", "这个执行实例还有工作或 Goal 在运行。请先结束或暂停后再发 /desk。")
		}
	}
	for _, image := range surface.StagedImages {
		if image != nil && image.State == state.ImageStaged {
			return notice(surface, "desk_busy", "还有暂存图片，请先发送或处理后再发 /desk。")
		}
	}
	for _, file := range surface.StagedFiles {
		if file != nil && file.State == state.FileStaged {
			return notice(surface, "desk_busy", "还有暂存文件，请先发送或处理后再发 /desk。")
		}
	}
	for _, other := range s.root.Surfaces {
		if other != nil && other.SurfaceSessionID != surface.SurfaceSessionID && (other.AttachedInstanceID == inst.InstanceID || other.PendingHeadless != nil && other.PendingHeadless.InstanceID == inst.InstanceID) {
			return notice(surface, "desk_shared_instance", "这个执行实例仍被其他窗口使用，本次没有结束任何进程。")
		}
	}
	if s.desktopHandoffs == nil {
		s.desktopHandoffs = make(map[string]string)
	}
	s.desktopHandoffs[surface.SurfaceSessionID] = inst.InstanceID
	// 保留实例与会话的占用，退出确认前不允许新输入、恢复或另一个 Bot 抢用。
	// 这里的等待由 daemon 的有界退出确认收尾，不使用 detach 的强制超时解除。
	s.setSurfaceDetachAbandoning(surface, time.Time{})
	return []eventcontract.Event{{
		Kind: eventcontract.KindDaemonCommand, SurfaceSessionID: surface.SurfaceSessionID,
		DaemonCommand: &control.DaemonCommand{
			Kind: control.DaemonCommandDesktopHandoff, SurfaceSessionID: surface.SurfaceSessionID,
			InstanceID: inst.InstanceID, ThreadID: surface.SelectedThreadID, SourceMessageID: action.MessageID,
		},
	}}
}

// DesktopHandoffPending binds the exit request to the original surface and instance.
func (s *Service) DesktopHandoffPending(surfaceID, instanceID string) bool {
	return instanceID != "" && s.desktopHandoffs[surfaceID] == instanceID
}

func (s *Service) instanceHasDesktopHandoff(instanceID string) bool {
	for _, pendingInstanceID := range s.desktopHandoffs {
		if pendingInstanceID == instanceID {
			return true
		}
	}
	return false
}

// FinishDesktopHandoff is called after an exit confirmation or bounded failure.
// A disconnected transport alone does not prove that Codex released its writer.
func (s *Service) FinishDesktopHandoff(surfaceID, instanceID string, released bool) []eventcontract.Event {
	if !s.DesktopHandoffPending(surfaceID, instanceID) {
		return nil
	}
	delete(s.desktopHandoffs, surfaceID)
	surface := s.root.Surfaces[surfaceID]
	if surface == nil || surface.AttachedInstanceID != instanceID {
		return nil
	}
	surface.Abandoning = false
	if !released {
		var events []eventcontract.Event
		if inst := s.root.Instances[instanceID]; inst == nil || !inst.Online {
			events = s.finalizeDetachedSurface(surface)
		}
		return append(events, notice(surface, "desk_failed", "尚未确认执行实例退出，不能确认桌面已解锁。请查看 /status，必要时用 /use 恢复后重试 /desk。")...)
	}
	s.revokeGoalInterlockForSurface(surfaceID)
	events := s.discardDrafts(surface)
	events = append(events, s.finalizeDetachedSurface(surface)...)
	return append(events, notice(surface, "desk_released", "当前执行实例已退出，可以在 ChatGPT 桌面端打开原会话继续。聊天记录和代码保留；以后回飞书可用 /use 或 /list 恢复。")...)
}
