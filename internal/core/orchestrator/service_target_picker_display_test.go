package orchestrator

import (
	"strings"
	"testing"
	"time"

	"github.com/kxn/codex-remote-feishu/internal/adapter/feishu/cardkit"
	cardtransport "github.com/kxn/codex-remote-feishu/internal/adapter/feishu/cardtransport"
	projectorpkg "github.com/kxn/codex-remote-feishu/internal/adapter/feishu/projector"
	"github.com/kxn/codex-remote-feishu/internal/core/control"
	"github.com/kxn/codex-remote-feishu/internal/core/state"
)

func TestTargetPickerSessionPreservesFullSemanticTitleAndWorkingDirectory(t *testing.T) {
	now := time.Date(2026, 10, 22, 9, 31, 0, 0, time.UTC)
	svc := newServiceForTest(&now)
	workspace := "/data/projects/web"
	cwd := workspace + "/packages/client"
	name := "client · " + strings.Repeat("修复布局并核对完整会话名称", 8)
	svc.UpsertInstance(&state.InstanceRecord{
		InstanceID: "inst-web", WorkspaceRoot: workspace, WorkspaceKey: workspace, Online: true,
		Threads: map[string]*state.ThreadRecord{
			"thread-web": {ThreadID: "thread-web", Name: name, WorkspaceKey: workspace, CWD: cwd, Loaded: true, LastUsedAt: now},
		},
	})
	svc.MaterializeSurface("surface-1", "app-1", "chat-1", "user-1")
	options := svc.targetPickerSessionOptions(svc.root.Surfaces["surface-1"], workspaceSelectionEntry{workspaceKey: workspace}, control.TargetPickerRequestSourceList, true)
	if len(options) != 2 || options[0].Kind != control.FeishuTargetPickerSessionNewThread {
		t.Fatalf("expected existing new-thread-first order, got %#v", options)
	}
	if options[1].Label != name {
		t.Fatalf("session label = %q, want full semantic title %q", options[1].Label, name)
	}
	if options[1].MetaText != cwd {
		t.Fatalf("session detail = %q, want session cwd %q", options[1].MetaText, cwd)
	}
}

func TestTargetPickerConfirmedHugeTitleKeepsThreadIdentityAndFitsStageCards(t *testing.T) {
	now := time.Date(2026, 10, 22, 9, 31, 0, 0, time.UTC)
	svc := newServiceForTest(&now)
	name := strings.Repeat("完整的长会话名称", 6000)
	const threadID = "real-target-thread"
	svc.UpsertInstance(&state.InstanceRecord{
		InstanceID: "inst-web", WorkspaceRoot: "/data/web", WorkspaceKey: "/data/web", Online: true,
		Threads: map[string]*state.ThreadRecord{
			threadID: {ThreadID: threadID, Name: name, CWD: "/data/web", Loaded: true, LastUsedAt: now},
		},
	})
	opened := singleTargetPickerEvent(t, svc.ApplySurfaceAction(control.Action{
		Kind: control.ActionShowAllThreads, SurfaceSessionID: "surface-1", ChatID: "chat-1", ActorUserID: "user-1",
	}))
	confirmed := singleTargetPickerEvent(t, svc.ApplySurfaceAction(control.Action{
		Kind: control.ActionTargetPickerConfirm, SurfaceSessionID: "surface-1", ChatID: "chat-1", ActorUserID: "user-1",
		PickerID: opened.PickerID, WorkspaceKey: "/data/web", TargetPickerValue: targetPickerThreadValue(threadID),
	}))
	if confirmed.Stage != control.FeishuTargetPickerStageSucceeded || svc.root.Surfaces["surface-1"].SelectedThreadID != threadID {
		t.Fatalf("actual confirm must attach the real thread: stage=%q thread=%q", confirmed.Stage, svc.root.Surfaces["surface-1"].SelectedThreadID)
	}
	if confirmed.SelectedSessionValue != targetPickerThreadValue(threadID) || svc.root.Instances["inst-web"].Threads[threadID].Name != name {
		t.Fatal("display protection must preserve the selected value and stored name")
	}
	processing := control.FeishuTargetPickerView{
		PickerID: confirmed.PickerID, Title: confirmed.Title, Stage: control.FeishuTargetPickerStageProcessing,
		SelectedSessionValue: confirmed.SelectedSessionValue, BodySections: confirmed.BodySections,
		StatusSections: targetPickerSwitchProcessingStatus("web", name).Sections,
	}
	for _, view := range []control.FeishuTargetPickerView{*confirmed, processing} {
		t.Run(string(view.Stage), func(t *testing.T) {
			elements := projectorpkg.TargetPickerElements(view, "life-confirmed")
			size, err := cardtransport.InteractiveMessageCardSize(view.Title, "", projectorpkg.TargetPickerTheme(view), elements, true)
			if err != nil {
				t.Fatalf("measure target picker stage card: %v", err)
			}
			if size > cardtransport.InteractiveCardTransportLimitBytes {
				t.Errorf("huge selected title must fit stage-card budget: got %d bytes", size)
			}
			var sawTruncation bool
			for _, element := range elements {
				text, _ := element["text"].(map[string]any)
				if strings.Contains(cardkit.StringValue(text["content"]), "名称过长，已截断") {
					sawTruncation = true
				}
			}
			if !sawTruncation {
				t.Error("overlong stage-card names must explicitly disclose truncation")
			}
		})
	}
}

func TestTargetPickerSessionDropdownLeadsWithLastActivityWithoutRepeatingItInDetails(t *testing.T) {
	for _, tc := range []struct {
		name   string
		usedAt time.Time
		want   string
	}{
		{name: "known", usedAt: time.Date(2026, 10, 22, 9, 31, 0, 0, time.Local), want: "10-22 09:31 · 真正的名称"},
		{name: "unknown", want: "时间未知 · 真正的名称"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.Local)
			svc := newServiceForTest(&now)
			svc.UpsertInstance(&state.InstanceRecord{
				InstanceID: "inst-web", WorkspaceRoot: "/data/web", WorkspaceKey: "/data/web", Online: true,
				Threads: map[string]*state.ThreadRecord{
					"private-thread-id": {ThreadID: "private-thread-id", Name: "真正的名称", CWD: "/data/web", Loaded: true, LastUsedAt: tc.usedAt},
				},
			})
			svc.MaterializeSurface("surface-1", "app-1", "chat-1", "user-1")
			options := svc.targetPickerSessionOptions(svc.root.Surfaces["surface-1"], workspaceSelectionEntry{workspaceKey: "/data/web"}, control.TargetPickerRequestSourceList, true)
			elements := projectorpkg.TargetPickerElements(control.FeishuTargetPickerView{
				PickerID: "display", SessionOptions: options,
			}, "life")
			var got []string
			for _, element := range elements {
				if element["tag"] != "select_static" {
					continue
				}
				for _, option := range element["options"].([]map[string]any) {
					text := option["text"].(map[string]any)
					got = append(got, cardkit.StringValue(text["content"]))
				}
			}
			if len(got) != 2 || got[0] != "新建会话" || got[1] != tc.want {
				t.Fatalf("dropdown labels = %q, want new-thread action then %q", got, tc.want)
			}
			if options[1].MetaText != "/data/web" {
				t.Fatalf("details must contain cwd without repeated activity time, got %q", options[1].MetaText)
			}
		})
	}
}
