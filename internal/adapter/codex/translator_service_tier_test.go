package codex

import (
	"encoding/json"
	"testing"

	"github.com/kxn/codex-remote-feishu/internal/core/agentproto"
)

func TestServiceTierExplicitTurnUsesNativeSettingAndAcknowledgment(t *testing.T) {
	tr := NewTranslator("inst")
	observeServiceTierClient(t, tr, `{"id":"ui-turn","method":"turn/start","params":{"threadId":"thread","serviceTier":"fast"}}`)

	command := agentproto.Command{
		Kind:   agentproto.CommandPromptSend,
		Origin: agentproto.Origin{Surface: "feishu-surface"},
		Target: agentproto.Target{ThreadID: "thread"},
		Prompt: agentproto.Prompt{Inputs: []agentproto.Input{{Type: agentproto.InputText, Text: "next"}}},
	}
	frame := translateServiceTierCommand(t, tr, command)
	if _, ok := lookupMap(frame, "params")["serviceTier"]; ok {
		t.Fatal("captured UI template must not resend a service tier")
	}

	for _, tier := range []string{"fast", "default"} {
		command.Overrides.ServiceTier = tier
		frame = translateServiceTierCommand(t, tr, command)
		if got := lookupString(frame, "params", "serviceTier"); got != tier {
			t.Fatalf("turn/start serviceTier = %q, want %q", got, tier)
		}
		id := lookupString(frame, "id")
		ack := observeServiceTierServer(t, tr, `{"id":"`+id+`","result":{"turn":{"id":"native-turn","status":"inProgress"}}}`)
		event := requireServiceTierEvent(t, ack, "thread", tier)
		if !ack.Suppress || event.TurnID != "native-turn" || event.Initiator.Kind != agentproto.InitiatorRemoteSurface || event.Initiator.SurfaceSessionID != "feishu-surface" {
			t.Fatalf("remote ack lost origin: %#v", ack)
		}
	}
	frame = translateServiceTierCommand(t, tr, command)
	id := lookupString(frame, "id")
	failed := observeServiceTierServer(t, tr, `{"id":"`+id+`","error":{"code":-32600,"message":"not supported"}}`)
	if len(failed.Events) != 1 || failed.Events[0].Kind != agentproto.EventTurnCompleted || failed.Events[0].ThreadSettings != nil {
		t.Fatalf("rejected turn must not update the tier: %#v", failed.Events)
	}
}

func TestServiceTierObservesRemoteManagedStartAndResume(t *testing.T) {
	for _, c := range []struct {
		name, existingThreadID, createdThreadID, method, field, tier string
	}{
		{"start", "", "thread-new", "thread/start", `"priority"`, "fast"},
		{"resume", "thread-saved", "thread-saved", "thread/resume", "null", "default"},
	} {
		t.Run(c.name, func(t *testing.T) {
			tr := NewTranslator("inst")
			frame := translateServiceTierCommand(t, tr, agentproto.Command{
				Kind:   agentproto.CommandPromptSend,
				Origin: agentproto.Origin{Surface: "feishu-surface"},
				Target: agentproto.Target{ThreadID: c.existingThreadID, CWD: "/tmp"},
				Prompt: agentproto.Prompt{Inputs: []agentproto.Input{{Type: agentproto.InputText, Text: "next"}}},
			})
			if method := lookupString(frame, "method"); method != c.method {
				t.Fatalf("method = %q, want %q", method, c.method)
			}
			id := lookupString(frame, "id")
			reply := observeServiceTierServer(t, tr, `{"id":"`+id+`","result":{"thread":{"id":"`+c.createdThreadID+`"},"serviceTier":`+c.field+`}}`)
			event := requireServiceTierEvent(t, reply, c.createdThreadID, c.tier)
			if event.Initiator.Kind == agentproto.InitiatorRemoteSurface || !reply.Suppress || len(reply.OutboundToCodex) != 1 {
				t.Fatalf("config observation must preserve follow-up, not acknowledge a tier switch: %#v", reply)
			}
		})
	}
}

func TestServiceTierObservesLocalRepliesAndSkipsHelpers(t *testing.T) {
	tr := NewTranslator("inst")
	observeServiceTierClient(t, tr, `{"id":1,"method":"thread/start","params":{}}`)
	start := observeServiceTierServer(t, tr, `{"id":1,"result":{"thread":{"id":"a"},"serviceTier":"priority"}}`)
	requireServiceTierEvent(t, start, "a", "fast")
	if start.Suppress {
		t.Fatal("local thread/start reply must still reach its client")
	}

	observeServiceTierClient(t, tr, `{"id":2,"method":"thread/resume","params":{"threadId":"b"}}`)
	resume := observeServiceTierServer(t, tr, `{"id":2,"result":{"thread":{"id":"b"},"serviceTier":"ultrafast"}}`)
	requireServiceTierEvent(t, resume, "b", "ultrafast")

	observeServiceTierClient(t, tr, `{"id":3,"method":"turn/start","params":{"threadId":"a","serviceTier":"default"}}`)
	turn := observeServiceTierServer(t, tr, `{"id":3,"result":{"turn":{"id":"native-local-turn"}}}`)
	if event := requireServiceTierEvent(t, turn, "a", "default"); event.Initiator.Kind != agentproto.InitiatorLocalUI {
		t.Fatalf("turn should remain a local observation: %#v", event)
	}

	observeServiceTierClient(t, tr, `{"id":4,"method":"thread/start","params":{"ephemeral":true}}`)
	helper := observeServiceTierServer(t, tr, `{"id":4,"result":{"thread":{"id":"helper"},"serviceTier":"fast"}}`)
	if len(helper.Events) != 0 {
		t.Fatalf("helper should not publish speed settings: %#v", helper.Events)
	}
	observeServiceTierClient(t, tr, `{"id":5,"method":"thread/resume","params":{"threadId":"a"}}`)
	missing := observeServiceTierServer(t, tr, `{"id":5,"result":{"thread":{"id":"a"}}}`)
	if len(missing.Events) != 0 {
		t.Fatalf("legacy response without tier must not invent a value: %#v", missing.Events)
	}
}

func TestServiceTierObservesSuccessfulChildRestartRestore(t *testing.T) {
	tr := NewTranslator("inst")
	observeServiceTierClient(t, tr, `{"method":"thread/resume","params":{"threadId":"thread"}}`)
	_, id, ok, err := tr.BuildChildRestartRestoreFrame("restart")
	if err != nil || !ok {
		t.Fatalf("build restore frame: ready=%t err=%v", ok, err)
	}
	reply := observeServiceTierServer(t, tr, `{"id":"`+id+`","result":{"thread":{"id":"thread"},"serviceTier":"flex"}}`)
	requireServiceTierEvent(t, reply, "thread", "flex")
	if !reply.Suppress || len(reply.Events) != 2 || reply.Events[0].Kind != agentproto.EventProcessChildRestartUpdated {
		t.Fatalf("restore response lost existing event: %#v", reply)
	}
}

func TestServiceTierObservesSettingsNotification(t *testing.T) {
	tr := NewTranslator("inst")
	native := observeServiceTierServer(t, tr, `{"method":"thread/settings/updated","params":{"threadId":"a","threadSettings":{"serviceTier":"priority","effort":"high","model":"gpt-6.2-sol"}}}`)
	event := requireServiceTierEvent(t, native, "a", "fast")
	if event.ThreadSettings.ReasoningEffort != "high" || event.ThreadSettings.Model != "gpt-6.2-sol" {
		t.Fatalf("native notification lost thread settings: %#v", event.ThreadSettings)
	}

	legacy := observeServiceTierServer(t, tr, `{"method":"thread/settings/updated","params":{"threadId":"a","settings":{"serviceTier":null,"reasoningEffort":"medium"}}}`)
	if event := requireServiceTierEvent(t, legacy, "a", "default"); event.ThreadSettings.ReasoningEffort != "medium" {
		t.Fatalf("legacy notification lost thread settings: %#v", event.ThreadSettings)
	}

	observeServiceTierClient(t, tr, `{"id":4,"method":"thread/start","params":{"ephemeral":true}}`)
	observeServiceTierServer(t, tr, `{"id":4,"result":{"thread":{"id":"helper"}}}`)
	helper := observeServiceTierServer(t, tr, `{"method":"thread/settings/updated","params":{"threadId":"helper","threadSettings":{"serviceTier":"priority"}}}`)
	if len(helper.Events) != 0 {
		t.Fatalf("helper tier should remain private: %#v", helper.Events)
	}
}

func translateServiceTierCommand(t *testing.T, tr *Translator, command agentproto.Command) map[string]any {
	t.Helper()
	frames, err := tr.TranslateCommand(command)
	if err != nil || len(frames) != 1 {
		t.Fatalf("translate command: %v, frames=%d", err, len(frames))
	}
	var frame map[string]any
	if err := json.Unmarshal(frames[0], &frame); err != nil {
		t.Fatal(err)
	}
	return frame
}

func observeServiceTierClient(t *testing.T, tr *Translator, raw string) {
	t.Helper()
	if _, err := tr.ObserveClient([]byte(raw)); err != nil {
		t.Fatal(err)
	}
}

func observeServiceTierServer(t *testing.T, tr *Translator, raw string) Result {
	t.Helper()
	result, err := tr.ObserveServer([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func requireServiceTierEvent(t *testing.T, result Result, threadID, tier string) agentproto.Event {
	t.Helper()
	for _, e := range result.Events {
		if e.Kind == agentproto.EventThreadSettingsUpdated && e.ThreadID == threadID &&
			e.ThreadSettings != nil && e.ThreadSettings.ServiceTier == tier {
			return e
		}
	}
	t.Fatalf("no %q service tier observation for %q: %#v", tier, threadID, result.Events)
	return agentproto.Event{}
}
