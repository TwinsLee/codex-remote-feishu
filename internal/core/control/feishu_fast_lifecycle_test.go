package control

import "testing"

func TestFastCardRequiresFreshnessAndReplacesFirstResult(t *testing.T) {
	action := Action{
		Kind: ActionFastCommand, Text: "/fast on",
		Inbound: &ActionInboundMeta{CardCallback: true},
	}
	if !RejectsUnstampedFeishuCardCallback(action) {
		t.Fatal("unstamped callback should be rejected")
	}
	action.Inbound.CardDaemonLifecycleID = "live-daemon"
	contract := ResolveFeishuFrontstageActionContract(action)
	if !contract.RequiresDaemonFreshness || contract.CurrentCardMode != FeishuFrontstageCurrentCardFirstResultCard {
		t.Fatalf("unexpected speed card contract: %#v", contract)
	}
}
