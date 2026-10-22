package orchestrator

import (
	"testing"

	"github.com/kxn/codex-remote-feishu/internal/core/control"
	"github.com/kxn/codex-remote-feishu/internal/core/state"
)

func TestDeskRejectsReviewBeforeTurnStarted(t *testing.T) {
	svc, _, surface := deskServiceForTest(t)
	surface.ReviewSession = &state.ReviewSessionRecord{Phase: state.ReviewSessionPhasePending}
	for _, event := range svc.ApplySurfaceAction(deskActionForTest()) {
		if event.DaemonCommand != nil {
			t.Fatal("pending review must not be interrupted by desktop handoff")
		}
	}
}

func TestDeskTransportDegradedCannotReopenInput(t *testing.T) {
	svc, inst, surface := deskServiceForTest(t)
	svc.ApplySurfaceAction(deskActionForTest())
	svc.ApplyInstanceTransportDegraded(inst.InstanceID, false)
	svc.ApplySurfaceAction(control.Action{Kind: control.ActionAutoContinueCommand, SurfaceSessionID: surface.SurfaceSessionID, Text: "/autocontinue on"})
	if surface.AutoContinue.Enabled || !surface.Abandoning {
		t.Fatal("degraded transport removed the desktop handoff gate")
	}
}

func TestDeskDisconnectRetainsInstanceClaim(t *testing.T) {
	svc, inst, surface := deskServiceForTest(t)
	svc.ApplySurfaceAction(deskActionForTest())
	svc.ApplyInstanceDisconnected(inst.InstanceID)
	if svc.instanceClaimSurface(inst.InstanceID) != surface {
		t.Fatal("disconnect released the desktop handoff instance claim")
	}
}

func TestDeskDefersCrossSurfaceProfileReconciliation(t *testing.T) {
	svc, inst, surface := deskServiceForTest(t)
	svc.ApplySurfaceAction(deskActionForTest())
	surface.CodexProfileID = "other-profile"
	for _, event := range svc.reconcileHeadlessSurfaceContract(surface) {
		if event.DaemonCommand != nil {
			t.Fatal("profile reconciliation restarted an instance during desktop handoff")
		}
	}
	if surface.AttachedInstanceID != inst.InstanceID || !surface.ContractRefreshPending {
		t.Fatal("profile reconciliation did not defer the incompatible contract")
	}
}
