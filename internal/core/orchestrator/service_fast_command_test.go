package orchestrator

import (
	"testing"
	"time"

	"github.com/kxn/codex-remote-feishu/internal/core/agentproto"
	"github.com/kxn/codex-remote-feishu/internal/core/control"
	"github.com/kxn/codex-remote-feishu/internal/core/frontstagecontract"
	"github.com/kxn/codex-remote-feishu/internal/core/state"
)

func TestFastCommandChangesNextTurnForOnlyTheSelectedThread(t *testing.T) {
	now := time.Now()
	s := newServiceForTest(&now)
	inst := threadLifecycleInstance()
	inst.Threads["thread-1"] = &state.ThreadRecord{ThreadID: "thread-1", Loaded: true}
	inst.Threads["thread-2"] = &state.ThreadRecord{ThreadID: "thread-2", Loaded: true}
	s.UpsertInstance(inst)
	surface := s.ensureSurface(control.Action{SurfaceSessionID: "surface-1", ChatID: "chat-1", ActorUserID: "user-1"})
	surface.Backend = agentproto.BackendCodex
	surface.AttachedInstanceID = inst.InstanceID
	surface.SelectedThreadID = "thread-1"

	observe := func(tier string, initiator agentproto.Initiator) {
		s.ApplyAgentEvent(inst.InstanceID, agentproto.Event{
			Kind: agentproto.EventThreadSettingsUpdated, ThreadID: "thread-1", Initiator: initiator,
			ThreadSettings: &agentproto.ThreadSettingsUpdate{ThreadID: "thread-1", ServiceTier: tier},
		})
	}
	command := func(threadID string, asyncUserInput bool) *agentproto.Command {
		item := &state.QueueItemRecord{
			SourceKind:         state.QueueItemSourceUser,
			AsyncUserInput:     asyncUserInput,
			FrozenDispatchPlan: agentproto.DefaultPromptDispatchPlanForExecutionThread(threadID),
		}
		return s.promptSendCommandFromQueueItem(surface, item, "user-1", "msg")
	}
	apply := func(text string) {
		action, ok := control.ParseFeishuTextActionWithoutCatalog(text)
		if !ok {
			t.Fatalf("missing command %s", text)
		}
		action.SurfaceSessionID = surface.SurfaceSessionID
		action.ActorUserID = "user-1"
		events := s.ApplySurfaceAction(action)
		if findPageEvent(events) == nil || findAgentCommand(events, agentproto.CommandPromptSend) != nil {
			t.Fatalf("expected a speed page without a model turn: %#v", events)
		}
	}
	status := func(current, pending string) {
		t.Helper()
		snapshot := s.buildSnapshot(surface)
		if snapshot.Attachment.CodexServiceTier != current || snapshot.Attachment.PendingCodexServiceTier != pending {
			t.Fatalf("got speed %q, pending %q; want %q, %q", snapshot.Attachment.CodexServiceTier, snapshot.Attachment.PendingCodexServiceTier, current, pending)
		}
	}
	observe("default", agentproto.Initiator{})
	apply("/fast on")
	status("default", "fast")
	if got := command("thread-1", true).Overrides.ServiceTier; got != "" {
		t.Fatalf("asynchronous question answer got speed override %q", got)
	}
	if got := command("thread-2", false).Overrides.ServiceTier; got != "" {
		t.Fatalf("unrelated thread got override %q", got)
	}
	if got := command("thread-1", false).Overrides.ServiceTier; got != "fast" {
		t.Fatalf("targeted turn got override %q", got)
	}
	remote := agentproto.Initiator{Kind: agentproto.InitiatorRemoteSurface, SurfaceSessionID: surface.SurfaceSessionID}
	observe("fast", remote)
	status("fast", "")
	if got := command("thread-1", false).Overrides.ServiceTier; got != "" {
		t.Fatalf("acknowledged setting was sent again: %q", got)
	}
	apply("/fast off")
	if got := command("thread-1", false).Overrides.ServiceTier; got != "default" {
		t.Fatalf("Fast off did not explicitly request Standard: %q", got)
	}
	observe("default", remote)
	status("default", "")
	observe("priority", agentproto.Initiator{Kind: agentproto.InitiatorLocalUI})
	status("fast", "")

	apply("/fast off")
	action, _ := control.ParseFeishuTextActionWithoutCatalog("/fast")
	action.SurfaceSessionID = surface.SurfaceSessionID
	page := findPageEvent(s.ApplySurfaceAction(action))
	if page == nil || len(page.RelatedButtons) != 2 {
		t.Fatalf("expected both speed buttons, got %#v", page)
	}
	button := page.RelatedButtons[0].CallbackValue
	surface.SelectedThreadID = "thread-2"
	action = control.Action{
		Kind: control.ActionFastCommand, Text: "/fast on", SurfaceSessionID: surface.SurfaceSessionID,
		InstanceID:       button[frontstagecontract.CardActionPayloadKeyInstanceID].(string),
		ThreadID:         button[frontstagecontract.CardActionPayloadKeyThreadID].(string),
		CatalogFamilyID:  button[frontstagecontract.CardActionPayloadKeyCatalogFamilyID].(string),
		CatalogVariantID: button[frontstagecontract.CardActionPayloadKeyCatalogVariantID].(string),
		CatalogBackend:   agentproto.BackendCodex,
		Inbound:          &control.ActionInboundMeta{CardDaemonLifecycleID: "current"},
	}
	if events := s.ApplySurfaceAction(action); findPageEvent(events) != nil || surface.PendingCodexServiceTier.Tier != "" {
		t.Fatalf("a speed button from the previous thread changed the selected thread: %#v", events)
	}
	action.Inbound = &control.ActionInboundMeta{CardCallback: true}
	if events := s.ApplySurfaceAction(action); findPageEvent(events) != nil || surface.PendingCodexServiceTier.Tier != "" {
		t.Fatalf("an unstamped button changed the selected thread: %#v", events)
	}
	status("", "")
}

func TestFastCommandUsesCodexCatalogForAvailability(t *testing.T) {
	now := time.Now()
	s := newServiceForTest(&now)
	inst := threadLifecycleInstance()
	inst.Threads["thread-1"] = &state.ThreadRecord{ThreadID: "thread-1", Loaded: true}
	inst.ModelCatalog = &agentproto.ModelCatalogSnapshot{Entries: []agentproto.ModelCatalogEntry{
		{Model: "model-without-fast"},
		{Model: "model-with-fast", ServiceTiers: []agentproto.ModelServiceTier{{ID: "priority"}}},
	}}
	s.UpsertInstance(inst)
	surface := s.ensureSurface(control.Action{SurfaceSessionID: "surface-1", ChatID: "chat-1", ActorUserID: "user-1"})
	surface.Backend = agentproto.BackendCodex
	surface.AttachedInstanceID = inst.InstanceID
	surface.SelectedThreadID = "thread-1"

	for _, item := range []struct {
		model   string
		blocked bool
	}{
		{"model-without-fast", true},
		{"model-with-fast", false},
		{"model-not-in-catalog", false},
	} {
		override := state.ModelConfigRecord{Model: item.model}
		if got := s.codexModelDoesNotOfferFast(surface, inst, "thread-1", "", override); got != item.blocked {
			t.Errorf("Fast block for %s = %t, want %t", item.model, got, item.blocked)
		}
	}

	surface.PendingCodexServiceTier = state.CodexServiceTierRequest{InstanceID: inst.InstanceID, ThreadID: "thread-1", Tier: "fast"}
	item := &state.QueueItemRecord{
		SourceKind:         state.QueueItemSourceUser,
		FrozenDispatchPlan: agentproto.DefaultPromptDispatchPlanForExecutionThread("thread-1"),
		FrozenOverride:     state.ModelConfigRecord{Model: "model-without-fast"},
	}
	command, notices := s.promptSendCommandAndGuardEventsFromQueueItem(surface, item, "user-1", "msg")
	if command.Overrides.ServiceTier != "" || len(notices) == 0 || surface.PendingCodexServiceTier.Tier != "" {
		t.Fatalf("speed requested after changing to unsupported model: command=%#v, notices=%#v", command, notices)
	}
}
