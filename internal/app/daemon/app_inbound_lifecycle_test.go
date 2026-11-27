package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kxn/codex-remote-feishu/internal/app/daemon/surfaceresume"
	"github.com/kxn/codex-remote-feishu/internal/core/agentproto"
	"github.com/kxn/codex-remote-feishu/internal/core/control"
	"github.com/kxn/codex-remote-feishu/internal/core/state"
)

func TestFeishuDelayedControlActionDoesNotInterruptOrDetach(t *testing.T) {
	for _, command := range []struct {
		kind control.ActionKind
		text string
	}{{control.ActionDetach, "/detach"}, {control.ActionStop, "/stop"}} {
		t.Run(command.text, func(t *testing.T) {
			ctx := context.Background()
			gateway := newLifecycleGateway()
			app := New(":0", ":0", gateway, agentproto.ServerIdentity{PID: 42, StartedAt: time.Now().Add(-2 * time.Hour)})
			const surfaceID = "feishu:codex2:user:user-1"
			app.service.UpsertInstance(&state.InstanceRecord{
				InstanceID: "inst-1", Online: true, WorkspaceRoot: "/work", WorkspaceKey: "/work",
				Threads: map[string]*state.ThreadRecord{"thread-1": {ThreadID: "thread-1", CWD: "/work"}},
			})
			app.service.MaterializeSurface(surfaceID, "codex2", "chat-1", "user-1")
			app.service.ApplySurfaceAction(control.Action{Kind: control.ActionAttachInstance, SurfaceSessionID: surfaceID, InstanceID: "inst-1"})
			app.service.ApplySurfaceAction(control.Action{Kind: control.ActionUseThread, SurfaceSessionID: surfaceID, ThreadID: "thread-1"})
			app.service.ApplySurfaceAction(control.Action{Kind: control.ActionTextMessage, SurfaceSessionID: surfaceID, Text: "运行测试"})
			app.service.ApplyAgentEvent("inst-1", agentproto.Event{Kind: agentproto.EventTurnStarted, ThreadID: "thread-1", TurnID: "turn-1"})
			surface := app.service.Surface(surfaceID)
			queueID := surface.ActiveQueueItemID
			if surface.SelectedThreadID != "thread-1" || queueID == "" {
				t.Fatal("test must start with a selected thread and active remote work")
			}
			store := surfaceresume.NewStore(surfaceresume.StatePath(t.TempDir()))
			app.surfaceResumeRuntime.store = store
			if err := store.Put(surfaceresume.Entry{
				SurfaceSessionID: surfaceID, GatewayID: "codex2", ChatID: "chat-1", ActorUserID: "user-1", ResumeThreadID: "thread-1",
			}); err != nil {
				t.Fatal(err)
			}
			var interrupts []string
			app.sendAgentCommand = func(_ string, outbound agentproto.Command) error {
				if outbound.Kind == agentproto.CommandTurnInterrupt {
					interrupts = append(interrupts, outbound.Target.TurnID)
				}
				return nil
			}
			action := control.Action{
				Kind: command.kind, Text: command.text, GatewayID: "codex2", SurfaceSessionID: surfaceID, ChatID: "chat-1", ActorUserID: "user-1",
				Inbound: &control.ActionInboundMeta{EventID: "old", MessageCreateTime: time.Now().Add(-65 * time.Minute)},
			}
			app.HandleGatewayAction(ctx, action)
			if len(interrupts) != 0 || surface.Abandoning || surface.SelectedThreadID != "thread-1" || surface.ActiveQueueItemID != queueID {
				t.Fatal("the delayed command changed the current attachment or work")
			}
			if entry, ok := store.Get(surfaceID); !ok || entry.ResumeThreadID != "thread-1" {
				t.Fatal("the delayed command removed the persisted recovery target")
			}
			ops := gateway.snapshotOperations()
			if len(ops) == 0 || !strings.Contains(operationCardText(ops[len(ops)-1]), "请重新发送 "+command.text) {
				t.Fatal("the delayed command did not produce a retry notice")
			}
			action.Inbound = &control.ActionInboundMeta{EventID: "fresh", MessageCreateTime: time.Now()}
			app.HandleGatewayAction(ctx, action)
			if len(interrupts) != 1 || interrupts[0] != "turn-1" {
				t.Fatalf("the fresh command must interrupt the current turn, got %v", interrupts)
			}
		})
	}
}

func TestClassifyInboundActionMarksOldMessage(t *testing.T) {
	startedAt := time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC)
	app := New(":0", ":0", nil, agentproto.ServerIdentity{PID: 42, StartedAt: startedAt})

	action := app.classifyInboundAction(control.Action{
		Inbound: &control.ActionInboundMeta{
			MessageCreateTime: startedAt.Add(-3 * time.Minute),
		},
	})

	if action.Inbound == nil || action.Inbound.LifecycleVerdict != control.InboundLifecycleOld || action.Inbound.LifecycleReason != "message_before_start_window" {
		t.Fatalf("expected old message verdict, got %#v", action.Inbound)
	}
}

func TestClassifyInboundActionKeepsBoundaryMessageCurrent(t *testing.T) {
	startedAt := time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC)
	app := New(":0", ":0", nil, agentproto.ServerIdentity{PID: 42, StartedAt: startedAt})

	action := app.classifyInboundAction(control.Action{
		Inbound: &control.ActionInboundMeta{
			MessageCreateTime: startedAt.Add(-90 * time.Second),
		},
	})

	if action.Inbound == nil || action.Inbound.LifecycleVerdict != control.InboundLifecycleCurrent || action.Inbound.LifecycleReason != "" {
		t.Fatalf("expected current message verdict, got %#v", action.Inbound)
	}
}

func TestClassifyInboundActionMarksOldMenu(t *testing.T) {
	startedAt := time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC)
	app := New(":0", ":0", nil, agentproto.ServerIdentity{PID: 42, StartedAt: startedAt})

	action := app.classifyInboundAction(control.Action{
		Inbound: &control.ActionInboundMeta{
			MenuClickTime: startedAt.Add(-5 * time.Minute),
		},
	})

	if action.Inbound == nil || action.Inbound.LifecycleVerdict != control.InboundLifecycleOld || action.Inbound.LifecycleReason != "menu_before_start_window" {
		t.Fatalf("expected old menu verdict, got %#v", action.Inbound)
	}
}

func TestClassifyInboundActionMarksOldCard(t *testing.T) {
	startedAt := time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC)
	app := New(":0", ":0", nil, agentproto.ServerIdentity{PID: 42, StartedAt: startedAt})

	action := app.classifyInboundAction(control.Action{
		Inbound: &control.ActionInboundMeta{
			CardDaemonLifecycleID: "older-life",
		},
	})

	if action.Inbound == nil || action.Inbound.LifecycleVerdict != control.InboundLifecycleOldCard || action.Inbound.LifecycleReason != "card_lifecycle_mismatch" {
		t.Fatalf("expected old card verdict, got %#v", action.Inbound)
	}
}

func TestClassifyInboundActionRejectsUnstampedFeishuUICallback(t *testing.T) {
	startedAt := time.Date(2026, 4, 8, 12, 0, 0, 0, time.UTC)
	app := New(":0", ":0", nil, agentproto.ServerIdentity{PID: 42, StartedAt: startedAt})

	action := app.classifyInboundAction(control.Action{
		Kind: control.ActionShowCommandMenu,
		Text: "/menu send_settings",
		Inbound: &control.ActionInboundMeta{
			CardCallback: true,
		},
	})

	if action.Inbound == nil || action.Inbound.LifecycleVerdict != control.InboundLifecycleOldCard || action.Inbound.LifecycleReason != "card_callback_missing_lifecycle" {
		t.Fatalf("expected unstamped feishu-ui callback to be rejected as old card, got %#v", action.Inbound)
	}
}

func TestRejectedInboundActionDetailShowsCommandText(t *testing.T) {
	got := rejectedInboundActionDetail(control.Action{
		Kind: control.ActionDetach,
		Text: "/detach",
	})
	if got != "命令“/detach”" {
		t.Fatalf("rejectedInboundActionDetail() = %q, want %q", got, "命令“/detach”")
	}
}

func TestRejectedInboundActionDetailShowsMenuFallback(t *testing.T) {
	got := rejectedInboundActionDetail(control.Action{
		Kind: control.ActionStop,
	})
	if got != "停止推理（对应 /stop）" {
		t.Fatalf("rejectedInboundActionDetail() = %q, want %q", got, "停止推理（对应 /stop）")
	}
}

func TestRejectedInboundActionDetailUsesCommandCatalogForDaemonCommand(t *testing.T) {
	tests := []struct {
		name string
		kind control.ActionKind
		want string
	}{
		{
			name: "direct daemon command",
			kind: control.ActionUpgradeCommand,
			want: "升级系统（对应 /upgrade）",
		},
		{
			name: "command backed ui intent",
			kind: control.ActionListInstances,
			want: "工作区与会话（对应 /list）",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rejectedInboundActionDetail(control.Action{Kind: tt.kind})
			if got != tt.want {
				t.Fatalf("rejectedInboundActionDetail() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRejectedInboundActionDetailUsesCommandCatalogRouteTitle(t *testing.T) {
	tests := []struct {
		name string
		kind control.ActionKind
		want string
	}{
		{
			name: "turn patch rollback",
			kind: control.ActionTurnPatchRollback,
			want: "回滚最近一次修补（对应 /bendtomywill rollback）",
		},
		{
			name: "review uncommitted",
			kind: control.ActionReviewStartUncommitted,
			want: "审阅待提交内容（对应 /review uncommitted）",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rejectedInboundActionDetail(control.Action{Kind: tt.kind})
			if got != tt.want {
				t.Fatalf("rejectedInboundActionDetail() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRejectedInboundActionDetailKeepsLocalLabelForOwnerCardAction(t *testing.T) {
	tests := []struct {
		kind control.ActionKind
		want string
	}{
		{kind: control.ActionReviewFollowUp, want: "继续追问审阅"},
		{kind: control.ActionReviewDiscard, want: "退出审阅"},
		{kind: control.ActionReviewApply, want: "按审阅意见继续修改"},
	}
	for _, tt := range tests {
		got := rejectedInboundActionDetail(control.Action{Kind: tt.kind})
		if got != tt.want {
			t.Fatalf("rejectedInboundActionDetail(%q) = %q, want %q", tt.kind, got, tt.want)
		}
	}
}

func TestRejectedInboundActionDetailUsesIntentLabelForWorkspaceListNavigation(t *testing.T) {
	got := rejectedInboundActionDetail(control.Action{
		Kind: control.ActionShowAllWorkspaces,
	})
	if got != "查看工作区列表（对应 /list）" {
		t.Fatalf("rejectedInboundActionDetail() = %q, want %q", got, "查看工作区列表（对应 /list）")
	}
}

func TestRejectedInboundActionDetailUsesIntentLabelForWorkspaceThreadExpansion(t *testing.T) {
	got := rejectedInboundActionDetail(control.Action{
		Kind:         control.ActionShowWorkspaceThreads,
		WorkspaceKey: "/data/dl/web",
	})
	if got != "展开该工作区下的会话列表" {
		t.Fatalf("rejectedInboundActionDetail() = %q, want %q", got, "展开该工作区下的会话列表")
	}
}

func TestRejectedInboundActionDetailShowsMessagePreview(t *testing.T) {
	got := rejectedInboundActionDetail(control.Action{
		Kind: control.ActionTextMessage,
		Text: "  这是  一条\n重启前的旧消息  ",
	})
	if got != "消息“这是 一条 重启前的旧消息”" {
		t.Fatalf("rejectedInboundActionDetail() = %q, want %q", got, "消息“这是 一条 重启前的旧消息”")
	}
}
