package orchestrator

import (
	"strings"
	"testing"
	"time"
)

func TestPredictionDesktopDraftHasNoLocalLengthOrAgePolicy(t *testing.T) {
	svc, sid, iid := predictionTestSetup(t)
	request := findAgentCommand(predictionCompleteTurn(t, svc, sid, iid), predictionRequestKind)
	suggestion := strings.Repeat("next ", 50) + "next"
	result := svc.ApplyAgentEvent(iid, predictionTestResult(t, request.CommandID, "turn-1", suggestion))
	if findPageEvent(result) == nil {
		t.Fatal("desktop suggestions do not have a 200-character rejection policy")
	}
	svc.predictions[sid].StartedAt = svc.now().Add(-20 * time.Minute)
	svc.expirePredictions(svc.now())
	if svc.predictions[sid].Canceled {
		t.Fatal("unchanged conversation must not expire a completed suggestion after a custom TTL")
	}
	accept := predictionTestAction(t, predictionAcceptKind, sid, request.CommandID, "user-1", "")
	page := findPageEvent(svc.ApplySurfaceAction(accept))
	if page == nil || page.Sections[0].Entries[0].Form.Field.DefaultValue != suggestion {
		t.Fatal("native suggestion should remain editable while its source context is current")
	}
}

func TestPredictionPreservesAdapterNormalizedSuggestion(t *testing.T) {
	svc, sid, iid := predictionTestSetup(t)
	request := findAgentCommand(predictionCompleteTurn(t, svc, sid, iid), predictionRequestKind)
	suggestion := "\u0085next\u0085"
	svc.ApplyAgentEvent(iid, predictionTestResult(t, request.CommandID, "turn-1", suggestion))
	if svc.predictions[sid].Suggestion != suggestion {
		t.Fatal("core must preserve the adapter's ECMAScript-normalized suggestion")
	}
	page := findPageEvent(svc.ApplySurfaceAction(predictionTestAction(t, predictionAcceptKind, sid, request.CommandID, "user-1", "")))
	if page == nil || page.Sections[0].Entries[0].Form.Field.DefaultValue != suggestion {
		t.Fatal("editable draft must retain the adapter's normalized text")
	}
}
