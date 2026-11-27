package orchestrator

import (
	"testing"
	"time"

	"github.com/kxn/codex-remote-feishu/internal/core/agentproto"
	"github.com/kxn/codex-remote-feishu/internal/core/control"
	"github.com/kxn/codex-remote-feishu/internal/core/state"
)

func TestFeishuStopAndDetachKeepTheirOriginalTargets(t *testing.T) {
	for _, kind := range []control.ActionKind{control.ActionStop, control.ActionDetach} {
		for _, tc := range []struct {
			name      string
			source    string
			change    string
			wantBlock bool
		}{
			{name: "late command predates current turn", source: "message", change: "turn", wantBlock: true},
			{name: "late menu click predates current turn", source: "menu", change: "turn", wantBlock: true},
			{name: "late card click predates current turn", source: "card", change: "turn", wantBlock: true},
			{name: "late command predates new attachment", source: "message", change: "route", wantBlock: true},
			{name: "missing action time cannot establish target", source: "missing", wantBlock: true},
			{name: "late command still targets the same turn", source: "message"},
			{name: "new card click still targets the same turn", source: "card"},
		} {
			t.Run(string(kind)+"/"+tc.name, func(t *testing.T) {
				now := time.Date(2026, 11, 27, 20, 29, 0, 0, time.UTC)
				svc := newServiceForTest(&now)
				surfaceID := "feishu:codex2:user:user-1"
				svc.UpsertInstance(&state.InstanceRecord{
					InstanceID: "inst-1", Online: true, WorkspaceRoot: "/work", WorkspaceKey: "/work",
					Threads: map[string]*state.ThreadRecord{"thread-1": {ThreadID: "thread-1", CWD: "/work"}},
				})
				attach := control.Action{Kind: control.ActionAttachInstance, SurfaceSessionID: surfaceID, InstanceID: "inst-1"}
				svc.ApplySurfaceAction(attach)
				svc.ApplySurfaceAction(control.Action{Kind: control.ActionUseThread, SurfaceSessionID: surfaceID, ThreadID: "thread-1"})
				svc.ApplySurfaceAction(control.Action{Kind: control.ActionTextMessage, SurfaceSessionID: surfaceID, MessageID: "prompt-1", Text: "运行测试"})
				start := agentproto.Event{Kind: agentproto.EventTurnStarted, ThreadID: "thread-1", TurnID: "turn-1"}
				if tc.change == "" {
					svc.ApplyAgentEvent("inst-1", start)
				}
				sent := now.Add(time.Minute)
				now = sent.Add(64 * time.Minute)
				switch tc.change {
				case "turn":
					svc.ApplyAgentEvent("inst-1", start)
				case "route":
					svc.ApplySurfaceAction(control.Action{Kind: control.ActionDetach, SurfaceSessionID: surfaceID})
					svc.ApplySurfaceAction(attach)
				}
				if tc.source == "card" && tc.change == "" {
					sent = now
				}
				now = now.Add(time.Minute)
				meta := &control.ActionInboundMeta{EventID: "feishu-event"}
				switch tc.source {
				case "message":
					meta.MessageCreateTime = sent
					meta.EventCreateTime = now
				case "menu":
					meta.MenuClickTime = sent
					meta.EventCreateTime = now
				case "card":
					meta.CardCallback = true
					meta.MessageCreateTime = sent.Add(-time.Hour)
					meta.EventCreateTime = sent
				}
				rejection := svc.FeishuControlTargetRejection(control.Action{Kind: kind, GatewayID: "codex2", SurfaceSessionID: surfaceID, Inbound: meta})
				if got := rejection != nil; got != tc.wantBlock {
					t.Fatalf("rejection = %v, want %v (%+v)", got, tc.wantBlock, rejection)
				}
			})
		}
	}
}

func TestStopInterruptsActiveTurnAndDiscardsQueuedMessages(t *testing.T) {
	now := time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)
	svc := newServiceForTest(&now)
	svc.UpsertInstance(&state.InstanceRecord{
		InstanceID:              "inst-1",
		DisplayName:             "droid",
		WorkspaceRoot:           "/data/dl/droid",
		WorkspaceKey:            "/data/dl/droid",
		ShortName:               "droid",
		Online:                  true,
		ObservedFocusedThreadID: "thread-1",
		ActiveThreadID:          "thread-1",
		ActiveTurnID:            "turn-1",
		Threads: map[string]*state.ThreadRecord{
			"thread-1": {ThreadID: "thread-1", Name: "修复登录流程", CWD: "/data/dl/droid"},
		},
	})
	svc.ApplySurfaceAction(control.Action{Kind: control.ActionAttachInstance, SurfaceSessionID: "surface-1", ChatID: "chat-1", ActorUserID: "user-1", InstanceID: "inst-1"})
	svc.root.Surfaces["surface-1"].QueuedQueueItemIDs = []string{"queue-1"}
	svc.root.Surfaces["surface-1"].QueueItems["queue-1"] = &state.QueueItemRecord{
		ID:              "queue-1",
		SourceMessageID: "msg-1",
		Status:          state.QueueItemQueued,
	}
	svc.root.Surfaces["surface-1"].StagedImages["img-1"] = &state.StagedImageRecord{
		ImageID:         "img-1",
		SourceMessageID: "msg-img",
		State:           state.ImageStaged,
	}

	events := svc.ApplySurfaceAction(control.Action{
		Kind:             control.ActionStop,
		SurfaceSessionID: "surface-1",
	})
	if len(events) != 4 {
		t.Fatalf("expected interrupt + 2 discard events + notice, got %#v", events)
	}
	if events[0].Command == nil || events[0].Command.Kind != agentproto.CommandTurnInterrupt {
		t.Fatalf("expected interrupt command, got %#v", events[0])
	}
	if events[3].Notice == nil || events[3].Notice.Code != "stop_requested" {
		t.Fatalf("expected stop_requested notice, got %#v", events[3])
	}
}

func TestUnknownSideTurnLifecycleDoesNotDisturbTrackedRemoteTurn(t *testing.T) {
	now := time.Date(2026, 4, 3, 12, 5, 0, 0, time.UTC)
	svc := newServiceForTest(&now)
	svc.UpsertInstance(&state.InstanceRecord{
		InstanceID:              "inst-1",
		DisplayName:             "droid",
		WorkspaceRoot:           "/data/dl/droid",
		WorkspaceKey:            "/data/dl/droid",
		ShortName:               "droid",
		Online:                  true,
		ObservedFocusedThreadID: "thread-main",
		Threads: map[string]*state.ThreadRecord{
			"thread-main": {ThreadID: "thread-main", Name: "主线程", CWD: "/data/dl/droid"},
			"thread-side": {ThreadID: "thread-side", Name: "旁路线程", CWD: "/data/dl/droid"},
		},
	})
	svc.ApplySurfaceAction(control.Action{Kind: control.ActionAttachInstance, SurfaceSessionID: "surface-1", ChatID: "chat-1", ActorUserID: "user-1", InstanceID: "inst-1"})
	svc.ApplySurfaceAction(control.Action{Kind: control.ActionUseThread, SurfaceSessionID: "surface-1", ThreadID: "thread-main"})
	svc.ApplySurfaceAction(control.Action{
		Kind:             control.ActionTextMessage,
		SurfaceSessionID: "surface-1",
		MessageID:        "msg-main",
		Text:             "开始主任务",
	})
	svc.ApplyAgentEvent("inst-1", agentproto.Event{
		Kind:      agentproto.EventTurnStarted,
		ThreadID:  "thread-main",
		TurnID:    "turn-main",
		Initiator: agentproto.Initiator{Kind: agentproto.InitiatorUnknown},
	})

	surface := svc.root.Surfaces["surface-1"]
	activeQueueID := surface.ActiveQueueItemID
	if activeQueueID == "" {
		t.Fatalf("expected tracked remote turn to keep an active queue item")
	}

	svc.ApplyAgentEvent("inst-1", agentproto.Event{
		Kind:      agentproto.EventTurnStarted,
		ThreadID:  "thread-side",
		TurnID:    "turn-side",
		Initiator: agentproto.Initiator{Kind: agentproto.InitiatorUnknown},
	})
	if svc.root.Instances["inst-1"].ActiveTurnID != "turn-main" || svc.root.Instances["inst-1"].ActiveThreadID != "thread-main" {
		t.Fatalf("expected side turn start not to steal tracked remote turn, got thread=%q turn=%q", svc.root.Instances["inst-1"].ActiveThreadID, svc.root.Instances["inst-1"].ActiveTurnID)
	}
	if surface.ActiveQueueItemID != activeQueueID {
		t.Fatalf("expected side turn start not to disturb active queue item, before=%q after=%q", activeQueueID, surface.ActiveQueueItemID)
	}

	svc.ApplyAgentEvent("inst-1", agentproto.Event{
		Kind:      agentproto.EventTurnCompleted,
		ThreadID:  "thread-side",
		TurnID:    "turn-side",
		Status:    "failed",
		Initiator: agentproto.Initiator{Kind: agentproto.InitiatorUnknown},
	})
	if svc.root.Instances["inst-1"].ActiveTurnID != "turn-main" || svc.root.Instances["inst-1"].ActiveThreadID != "thread-main" {
		t.Fatalf("expected side turn completion not to clear tracked remote turn, got thread=%q turn=%q", svc.root.Instances["inst-1"].ActiveThreadID, svc.root.Instances["inst-1"].ActiveTurnID)
	}
	if binding := svc.turns.activeRemote["inst-1"]; binding == nil || binding.TurnID != "turn-main" || binding.ThreadID != "thread-main" {
		t.Fatalf("expected active remote binding to remain on main turn, got %#v", binding)
	}
}

func TestStopUsesBoundRemoteTurnWhenInstanceActiveTurnMissing(t *testing.T) {
	now := time.Date(2026, 4, 3, 12, 10, 0, 0, time.UTC)
	svc := newServiceForTest(&now)
	svc.UpsertInstance(&state.InstanceRecord{
		InstanceID:              "inst-1",
		DisplayName:             "droid",
		WorkspaceRoot:           "/data/dl/droid",
		WorkspaceKey:            "/data/dl/droid",
		ShortName:               "droid",
		Online:                  true,
		ObservedFocusedThreadID: "thread-main",
		Threads: map[string]*state.ThreadRecord{
			"thread-main": {ThreadID: "thread-main", Name: "主线程", CWD: "/data/dl/droid"},
		},
	})
	svc.ApplySurfaceAction(control.Action{Kind: control.ActionAttachInstance, SurfaceSessionID: "surface-1", ChatID: "chat-1", ActorUserID: "user-1", InstanceID: "inst-1"})
	svc.ApplySurfaceAction(control.Action{Kind: control.ActionUseThread, SurfaceSessionID: "surface-1", ThreadID: "thread-main"})
	svc.ApplySurfaceAction(control.Action{
		Kind:             control.ActionTextMessage,
		SurfaceSessionID: "surface-1",
		MessageID:        "msg-main",
		Text:             "开始主任务",
	})
	svc.ApplyAgentEvent("inst-1", agentproto.Event{
		Kind:      agentproto.EventTurnStarted,
		ThreadID:  "thread-main",
		TurnID:    "turn-main",
		Initiator: agentproto.Initiator{Kind: agentproto.InitiatorUnknown},
	})

	svc.root.Instances["inst-1"].ActiveTurnID = ""
	svc.root.Instances["inst-1"].ActiveThreadID = ""

	events := svc.ApplySurfaceAction(control.Action{
		Kind:             control.ActionStop,
		SurfaceSessionID: "surface-1",
	})
	if len(events) != 2 {
		t.Fatalf("expected interrupt command + notice, got %#v", events)
	}
	if events[0].Command == nil || events[0].Command.Kind != agentproto.CommandTurnInterrupt {
		t.Fatalf("expected interrupt command, got %#v", events[0])
	}
	if events[0].Command.Target.ThreadID != "thread-main" || events[0].Command.Target.TurnID != "turn-main" {
		t.Fatalf("expected stop to target bound remote turn, got %#v", events[0].Command.Target)
	}
	if events[1].Notice == nil || events[1].Notice.Code != "stop_requested" {
		t.Fatalf("expected stop_requested notice, got %#v", events)
	}
}

func TestStopWithoutActiveTurnReturnsNotice(t *testing.T) {
	now := time.Date(2026, 4, 3, 12, 0, 0, 0, time.UTC)
	svc := newServiceForTest(&now)
	svc.UpsertInstance(&state.InstanceRecord{
		InstanceID:              "inst-1",
		DisplayName:             "droid",
		WorkspaceRoot:           "/data/dl/droid",
		WorkspaceKey:            "/data/dl/droid",
		ShortName:               "droid",
		Online:                  true,
		ObservedFocusedThreadID: "thread-2",
		Threads: map[string]*state.ThreadRecord{
			"thread-2": {ThreadID: "thread-2", Name: "修复登录流程", CWD: "/data/dl/droid"},
		},
	})
	svc.ApplySurfaceAction(control.Action{Kind: control.ActionAttachInstance, SurfaceSessionID: "surface-1", ChatID: "chat-1", ActorUserID: "user-1", InstanceID: "inst-1"})

	events := svc.ApplySurfaceAction(control.Action{
		Kind:             control.ActionStop,
		SurfaceSessionID: "surface-1",
	})
	if len(events) != 1 || events[0].Notice == nil || events[0].Notice.Code != "stop_no_active_turn" {
		t.Fatalf("expected stop_no_active_turn notice, got %#v", events)
	}
}
