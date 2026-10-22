package orchestrator

import (
	"testing"
	"time"

	"github.com/kxn/codex-remote-feishu/internal/core/agentproto"
	"github.com/kxn/codex-remote-feishu/internal/core/control"
	"github.com/kxn/codex-remote-feishu/internal/core/eventcontract"
	"github.com/kxn/codex-remote-feishu/internal/core/state"
)

func deskServiceForTest(t *testing.T) (*Service, *state.InstanceRecord, *state.SurfaceConsoleRecord) {
	t.Helper()
	now := time.Now()
	svc := newServiceForTest(&now)
	workspace := t.TempDir()
	inst := &state.InstanceRecord{
		InstanceID: "inst-desk", WorkspaceRoot: workspace, WorkspaceKey: workspace,
		Source: "headless", Managed: true, Online: true, PID: 4321,
		Backend: agentproto.BackendCodex,
		Threads: map[string]*state.ThreadRecord{
			"thread-desk": {ThreadID: "thread-desk", Name: "当前会话", CWD: workspace, Loaded: true},
		},
	}
	svc.UpsertInstance(inst)
	svc.ApplySurfaceAction(control.Action{Kind: control.ActionAttachInstance, SurfaceSessionID: "surface-desk", ChatID: "chat-desk", ActorUserID: "user-desk", InstanceID: inst.InstanceID})
	svc.ApplySurfaceAction(control.Action{Kind: control.ActionUseThread, SurfaceSessionID: "surface-desk", ThreadID: "thread-desk"})
	surface := svc.root.Surfaces["surface-desk"]
	if surface == nil || surface.AttachedInstanceID != inst.InstanceID || surface.SelectedThreadID != "thread-desk" {
		t.Fatalf("fixture did not attach the requested session: %#v", surface)
	}
	return svc, inst, surface
}

func deskActionForTest() control.Action {
	return control.Action{Kind: control.ActionKind("surface.button.desk"), SurfaceSessionID: "surface-desk", Text: "/desk"}
}

func TestDeskReservesIdleInstanceUntilReleaseConfirmed(t *testing.T) {
	svc, inst, surface := deskServiceForTest(t)
	events := svc.ApplySurfaceAction(deskActionForTest())
	if len(events) != 1 || events[0].DaemonCommand == nil {
		t.Fatalf("expected one release request, never early success: %#v", events)
	}
	command := events[0].DaemonCommand
	if command.Kind != control.DaemonCommandKind("headless.desktop_handoff") || command.InstanceID != inst.InstanceID || command.ThreadID != "thread-desk" {
		t.Fatalf("wrong handoff target: %#v", command)
	}
	if !surface.Abandoning || surface.AttachedInstanceID != inst.InstanceID {
		t.Fatal("handoff must retain ownership and block new input until confirmed")
	}
	for _, action := range []control.Action{
		deskActionForTest(),
		{Kind: control.ActionTextMessage, SurfaceSessionID: surface.SurfaceSessionID, Text: "继续", MessageID: "new-input"},
		{Kind: control.ActionNewThread, SurfaceSessionID: surface.SurfaceSessionID},
	} {
		for _, event := range svc.ApplySurfaceAction(action) {
			if event.Command != nil || event.DaemonCommand != nil {
				t.Fatalf("input dispatched while instance was being released: %#v", event)
			}
		}
	}
	for _, event := range svc.ApplyInstanceDisconnected(inst.InstanceID) {
		if event.Notice != nil && event.Notice.Code == "attached_instance_offline" {
			t.Fatal("expected handoff must not be reported as an unexpected offline failure")
		}
	}
	if surface.AttachedInstanceID != inst.InstanceID || !surface.Abandoning {
		t.Fatal("transport disconnect alone must not be treated as confirmed process exit")
	}
}

func TestDeskRejectsBusyOrForeignInstancesWithoutChangingRoute(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Service, *state.InstanceRecord, *state.SurfaceConsoleRecord)
	}{
		{"running turn", func(_ *Service, i *state.InstanceRecord, _ *state.SurfaceConsoleRecord) { i.ActiveTurnID = "running" }},
		{"queued input", func(_ *Service, _ *state.InstanceRecord, s *state.SurfaceConsoleRecord) {
			s.QueuedQueueItemIDs = []string{"queued"}
		}},
		{"pending user confirmation", func(_ *Service, _ *state.InstanceRecord, s *state.SurfaceConsoleRecord) {
			s.ActiveRequestCapture = &state.RequestCaptureRecord{RequestID: "pending"}
		}},
		{"staged image", func(_ *Service, _ *state.InstanceRecord, s *state.SurfaceConsoleRecord) {
			s.StagedImages = map[string]*state.StagedImageRecord{"image": {State: state.ImageStaged}}
		}},
		{"staged file", func(_ *Service, _ *state.InstanceRecord, s *state.SurfaceConsoleRecord) {
			s.StagedFiles = map[string]*state.StagedFileRecord{"file": {State: state.FileStaged}}
		}},
		{"active native thread", func(_ *Service, i *state.InstanceRecord, _ *state.SurfaceConsoleRecord) {
			i.Threads["thread-desk"].RuntimeStatus = &agentproto.ThreadRuntimeStatus{Type: agentproto.ThreadRuntimeStatusTypeActive}
		}},
		{"active goal in another loaded thread", func(_ *Service, i *state.InstanceRecord, _ *state.SurfaceConsoleRecord) {
			i.Threads["another"] = &state.ThreadRecord{ThreadID: "another", ThreadGoal: &agentproto.ThreadGoalUpdate{Status: "active"}}
		}},
		{"external process", func(_ *Service, i *state.InstanceRecord, _ *state.SurfaceConsoleRecord) { i.Managed = false }},
		{"VS Code process", func(_ *Service, i *state.InstanceRecord, _ *state.SurfaceConsoleRecord) { i.Source = "vscode" }},
		{"unknown process", func(_ *Service, i *state.InstanceRecord, _ *state.SurfaceConsoleRecord) { i.PID = 0 }},
		{"offline process", func(_ *Service, i *state.InstanceRecord, _ *state.SurfaceConsoleRecord) { i.Online = false }},
		{"other surface owner", func(s *Service, i *state.InstanceRecord, _ *state.SurfaceConsoleRecord) {
			s.root.Surfaces["other"] = &state.SurfaceConsoleRecord{SurfaceSessionID: "other", AttachedInstanceID: i.InstanceID}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, inst, surface := deskServiceForTest(t)
			tc.mutate(svc, inst, surface)
			events := svc.ApplySurfaceAction(deskActionForTest())
			if len(events) == 0 || events[0].Notice == nil {
				t.Fatalf("expected a visible refusal: %#v", events)
			}
			for _, event := range events {
				if event.Command != nil || event.DaemonCommand != nil {
					t.Fatal("refusal must not interrupt or exit anything")
				}
			}
			if surface.Abandoning || surface.AttachedInstanceID != inst.InstanceID || surface.SelectedThreadID != "thread-desk" {
				t.Fatal("refusal changed the existing session")
			}
		})
	}
}

func TestDeskWithoutAttachmentDoesNotGuessAProcess(t *testing.T) {
	now := time.Now()
	svc := newServiceForTest(&now)
	events := svc.ApplySurfaceAction(deskActionForTest())
	if len(events) == 0 || events[0].Notice == nil {
		t.Fatalf("expected guidance to select the session first: %#v", events)
	}
	for _, event := range events {
		if event.Kind == eventcontract.KindDaemonCommand {
			t.Fatal("must not search for and exit an unowned process")
		}
	}
}

func TestDeskPendingDoesNotAllowAutomationToBeReenabled(t *testing.T) {
	svc, _, surface := deskServiceForTest(t)
	svc.ApplySurfaceAction(deskActionForTest())
	svc.ApplySurfaceAction(control.Action{Kind: control.ActionAutoContinueCommand, SurfaceSessionID: surface.SurfaceSessionID, Text: "/autocontinue on"})
	if surface.AutoContinue.Enabled {
		t.Fatal("automation settings changed while desktop handoff was in progress")
	}
}
