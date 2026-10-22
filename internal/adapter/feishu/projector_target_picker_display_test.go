package feishu

import (
	"strings"
	"testing"

	"github.com/kxn/codex-remote-feishu/internal/adapter/feishu/cardkit"
	cardtransport "github.com/kxn/codex-remote-feishu/internal/adapter/feishu/cardtransport"
	projectorpkg "github.com/kxn/codex-remote-feishu/internal/adapter/feishu/projector"
	"github.com/kxn/codex-remote-feishu/internal/core/control"
	"github.com/kxn/codex-remote-feishu/internal/core/frontstagecontract"
)

func TestTargetPickerSelectorsShowOnlyNamesAndImmediateFullDetails(t *testing.T) {
	name := "client · " + strings.Repeat("完整名称", 35)
	elements := projectorpkg.TargetPickerElements(control.FeishuTargetPickerView{
		PickerID: "picker-display", SelectedWorkspaceKey: "/data/projects/web", SelectedWorkspaceLabel: "web",
		SelectedWorkspaceMeta: "main", SelectedSessionValue: "thread:thread-web", SelectedSessionLabel: name,
		SelectedSessionMeta: "/data/projects/web/packages/client",
		WorkspaceOptions: []control.FeishuTargetPickerWorkspaceOption{
			{Value: "/data/projects/web", Label: "web", MetaText: "main"},
			{Value: "/data/other/web", Label: "web", MetaText: "feature/topic"},
		},
		SessionOptions: []control.FeishuTargetPickerSessionOption{
			{Value: "thread:thread-web", Kind: control.FeishuTargetPickerSessionThread, Label: name, MetaText: "/data/projects/web/packages/client"},
		},
	}, "life-display")
	var selects []int
	for index, element := range elements {
		if cardkit.StringValue(element["tag"]) == "select_static" {
			selects = append(selects, index)
		}
	}
	if len(selects) != 2 {
		t.Fatalf("expected two selectors, got %d", len(selects))
	}
	workspaceSelect, sessionSelect := elements[selects[0]], elements[selects[1]]
	for _, option := range cardMapArray(workspaceSelect["options"]) {
		text, _ := option["text"].(map[string]any)
		if cardkit.StringValue(text["content"]) != "web" {
			t.Errorf("workspace dropdown must show leaf only, got %#v", option)
		}
	}
	for _, selector := range []map[string]any{workspaceSelect, sessionSelect} {
		if selector["width"] != "fill" {
			t.Errorf("selector width = %#v, want fill", selector["width"])
		}
	}
	if got := plainTextContent(elements[selects[0]+1]); got != "完整路径：/data/projects/web" {
		t.Errorf("workspace detail = %q, want full selected path immediately below workspace", got)
	}
	if got := plainTextContent(elements[selects[1]+1]); got != "完整名称："+name+"\n会话工作目录：/data/projects/web/packages/client" {
		t.Errorf("session detail = %q, want full name and cwd immediately below session", got)
	}
	if selects[0]+1 >= selects[1] {
		t.Fatal("workspace detail must precede session selector")
	}
}

func TestTargetPickerLongUnselectedTitleFitsBudgetAndKeepsOtherOptions(t *testing.T) {
	view := control.FeishuTargetPickerView{
		PickerID: "long-title", Title: "切换工作区与会话", ShowSessionSelect: true,
		SessionOptions: []control.FeishuTargetPickerSessionOption{
			{Value: "thread:long", Kind: control.FeishuTargetPickerSessionThread, Label: strings.Repeat("完整的长会话名称", 6000)},
			{Value: "thread:normal", Kind: control.FeishuTargetPickerSessionThread, Label: "核对正常会话"},
		},
	}
	elements := projectorpkg.TargetPickerElements(view, "life-long-title")
	size, err := cardtransport.InteractiveMessageCardSize(view.Title, "", projectorpkg.TargetPickerTheme(view), elements, true)
	if err != nil {
		t.Fatalf("measure target picker card: %v", err)
	}
	if size > cardtransport.InteractiveCardTransportLimitBytes {
		t.Errorf("one long title must not exceed transport budget: got %d bytes", size)
	}
	var values []string
	var collectSelectValues func(any)
	collectSelectValues = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			if typed["tag"] == "select_static" {
				for _, option := range cardMapArray(typed["options"]) {
					values = append(values, cardkit.StringValue(option["value"]))
				}
			}
			for _, child := range typed {
				collectSelectValues(child)
			}
		case []map[string]any:
			for _, child := range typed {
				collectSelectValues(child)
			}
		case []any:
			for _, child := range typed {
				collectSelectValues(child)
			}
		}
	}
	collectSelectValues(elements)
	if len(values) != 2 || values[0] != "thread:long" || values[1] != "thread:normal" {
		t.Errorf("long title must preserve both selectable values and order, got %q", values)
	}
}

func TestTargetPickerSyntheticOperationKeepsItsNormalHint(t *testing.T) {
	elements := projectorpkg.TargetPickerElements(control.FeishuTargetPickerView{
		PickerID: "new-thread-hint", SelectedSessionValue: "new_thread",
		SessionOptions: []control.FeishuTargetPickerSessionOption{
			{Value: "new_thread", Kind: control.FeishuTargetPickerSessionNewThread, Label: "新建会话", MetaText: "在这个工作区里开始一个新的会话"},
		},
	}, "life-hint")
	if !containsCardTextExact(elements, "新建会话\n在这个工作区里开始一个新的会话") {
		t.Fatal("synthetic operation must keep its normal hint without thread-detail labels")
	}
}

func TestTargetPickerSelectedHugeTitleFitsBudgetAndKeepsSwitchButton(t *testing.T) {
	view := control.FeishuTargetPickerView{
		PickerID: "selected-huge-title", Title: "切换工作区与会话", ShowSessionSelect: true,
		SelectedSessionValue: "thread:long", ConfirmLabel: "切换", CanConfirm: true,
		SessionOptions: []control.FeishuTargetPickerSessionOption{
			{Value: "thread:long", Kind: control.FeishuTargetPickerSessionThread, Label: strings.Repeat("完整的长会话名称", 6000), MetaText: "/data/projects/web/client"},
		},
	}
	elements := projectorpkg.TargetPickerElements(view, "life-selected-title")
	size, err := cardtransport.InteractiveMessageCardSize(view.Title, "", projectorpkg.TargetPickerTheme(view), elements, true)
	if err != nil {
		t.Fatalf("measure target picker card: %v", err)
	}
	if size > cardtransport.InteractiveCardTransportLimitBytes {
		t.Errorf("selected huge title must not exceed transport budget: got %d bytes", size)
	}
	var sawSwitch, sawTruncationNotice bool
	for _, action := range cardActionsFromElements(elements) {
		if cardValueMap(action)[frontstagecontract.CardActionPayloadKeyKind] == cardActionKindTargetPickerConfirm && action["disabled"] != true {
			sawSwitch = true
		}
	}
	for _, element := range elements {
		text := plainTextContent(element)
		if strings.Contains(text, "名称过长，已截断") {
			sawTruncationNotice = true
			if strings.Contains(text, "完整名称：") {
				t.Error("truncated title must not be labelled as the complete name")
			}
		}
	}
	if !sawSwitch || !sawTruncationNotice {
		t.Errorf("expected enabled switch button and explicit truncation notice: switch=%v notice=%v", sawSwitch, sawTruncationNotice)
	}
}
