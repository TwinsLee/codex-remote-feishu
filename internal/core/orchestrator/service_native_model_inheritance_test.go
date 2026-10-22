package orchestrator

import (
	"encoding/json"
	"testing"

	codexadapter "github.com/kxn/codex-remote-feishu/internal/adapter/codex"
	"github.com/kxn/codex-remote-feishu/internal/core/agentproto"
	"github.com/kxn/codex-remote-feishu/internal/core/state"
)

func TestNativeNewThreadDoesNotFreezeRemoteFallbackModel(t *testing.T) {
	for _, test := range []struct {
		name, profileID, threadID, explicit, want string
	}{
		{name: "native new thread follows Codex", profileID: state.NativeCodexProfileID},
		{name: "explicit model wins", profileID: state.NativeCodexProfileID, explicit: "gpt-6.1-sol", want: "gpt-6.1-sol"},
		{name: "existing unknown thread unchanged", profileID: state.NativeCodexProfileID, threadID: "thread-1", want: "gpt-5.5"},
		{name: "API profile unchanged", profileID: "api-profile", want: "profile-model"},
	} {
		t.Run(test.name, func(t *testing.T) {
			svc, surfaceID, instanceID := goalInterlockTestSetup(t)
			materializeTestCodexProfiles(svc, state.CodexProfileSummary{ID: "api-profile", Model: "profile-model"})
			surface := svc.root.Surfaces[surfaceID]
			surface.CodexProfileID = test.profileID
			inst := svc.root.Instances[instanceID]
			inst.CodexProfileID = test.profileID
			frozen := svc.resolveFrozenPromptOverride(inst, surface, test.threadID, "/tmp/project", state.ModelConfigRecord{Model: test.explicit})
			if frozen.Model != test.want {
				t.Fatalf("frozen model = %q, want %q", frozen.Model, test.want)
			}
		})
	}
}

func TestNativeNewThreadAndFirstTurnOmitFallbackModel(t *testing.T) {
	svc, surfaceID, instanceID := goalInterlockTestSetup(t)
	frozen := svc.resolveFrozenPromptOverride(svc.root.Instances[instanceID], svc.root.Surfaces[surfaceID], "", "/tmp/project", state.ModelConfigRecord{})
	translator := codexadapter.NewTranslator("probe-instance")
	if _, err := translator.ObserveClient([]byte(`{"method":"turn/start","params":{"threadId":"old-thread","model":"gpt-5.5","collaborationMode":{"mode":"custom","settings":{"model":"gpt-5.5"}}}}`)); err != nil {
		t.Fatal(err)
	}
	commands, err := translator.TranslateCommand(agentproto.Command{
		Kind:      agentproto.CommandPromptSend,
		Target:    agentproto.Target{ExecutionMode: agentproto.PromptExecutionModeStartNew, CWD: "/tmp/project"},
		Prompt:    agentproto.Prompt{Inputs: []agentproto.Input{{Type: agentproto.InputText, Text: "test"}}},
		Overrides: agentproto.PromptOverrides{Model: frozen.Model},
		CodexResume: &agentproto.CodexResumePolicy{
			Mode: agentproto.CodexResumeApplyTargetProfile, ModelProviderID: "custom",
			ModelMode: agentproto.CodexThreadValueDefault, ReasoningMode: agentproto.CodexThreadValueDefault,
		},
	})
	if err != nil || len(commands) != 1 {
		t.Fatalf("expected one thread/start: err=%v count=%d", err, len(commands))
	}
	var start map[string]any
	if err := json.Unmarshal(commands[0], &start); err != nil {
		t.Fatal(err)
	}
	assertNativeModelOmitted(t, start, "thread/start")
	response, _ := json.Marshal(map[string]any{"id": start["id"], "result": map[string]any{
		"model": "gpt-6.1-sol", "thread": map[string]any{"id": "native-new-thread", "cwd": "/tmp/project"},
	}})
	result, err := translator.ObserveServer(response)
	if err != nil || len(result.OutboundToCodex) != 1 {
		t.Fatalf("expected first turn/start: err=%v count=%d", err, len(result.OutboundToCodex))
	}
	var turn map[string]any
	if err := json.Unmarshal(result.OutboundToCodex[0], &turn); err != nil {
		t.Fatal(err)
	}
	assertNativeModelOmitted(t, turn, "turn/start")
}

func assertNativeModelOmitted(t *testing.T, payload map[string]any, method string) {
	t.Helper()
	if payload["method"] != method {
		t.Fatalf("expected %s, got %v", method, payload["method"])
	}
	params, _ := payload["params"].(map[string]any)
	if model := params["model"]; model != nil {
		t.Fatalf("%s must let Codex choose model, got %v", method, model)
	}
	mode, _ := params["collaborationMode"].(map[string]any)
	settings, _ := mode["settings"].(map[string]any)
	if model := settings["model"]; model != nil {
		t.Fatalf("%s retained stale collaboration model %v", method, model)
	}
}
