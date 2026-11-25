package control

import (
	"strings"
	"testing"

	"github.com/kxn/codex-remote-feishu/internal/core/agentproto"
)

func TestClaudeProfileConfigCopyClaimsAccessButNotPlanMemory(t *testing.T) {
	page := BuildFeishuCommandConfigPageView(FeishuCatalogConfigView{
		CommandID:      FeishuCommandClaudeProfile,
		CurrentValue:   "devseek",
		FormOptions:    []CommandCatalogFormFieldOption{{Label: "DevSeek", Value: "devseek"}},
		CatalogBackend: agentproto.BackendClaude,
	})
	text := configPageSummaryText(page)
	if !strings.Contains(text, "推理与权限临时覆盖") {
		t.Fatalf("expected claude profile copy to mention reasoning/access override, got %q", text)
	}
	if strings.Contains(text, "Plan 记忆") {
		t.Fatalf("claude profile copy must not claim plan memory, got %q", text)
	}
}

func TestPlanConfigPageShowsNoLocalOverrideState(t *testing.T) {
	page := BuildFeishuCommandConfigPageView(FeishuCatalogConfigView{
		CommandID:                   FeishuCommandPlan,
		CatalogBackend:              agentproto.BackendCodex,
		CurrentValue:                "off",
		EffectiveValue:              "on",
		UsesLocalRequestedOverrides: true,
		PlanModeOverrideSet:         false,
	})
	text := configPageSummaryText(page)
	if !strings.Contains(text, "飞书覆盖\n无（跟随底层当前状态）") {
		t.Fatalf("expected plan page to show no local override, got %q", text)
	}
	if !strings.Contains(text, "当前会话模式（最近观察）\n开启") {
		t.Fatalf("expected plan page to keep observed backend state, got %q", text)
	}
}

func TestAccessConfigPageShowsObservedThreadAccess(t *testing.T) {
	page := BuildFeishuCommandConfigPageView(FeishuCatalogConfigView{
		CommandID:            FeishuCommandAccess,
		CatalogBackend:       agentproto.BackendClaude,
		CurrentValue:         agentproto.AccessModeConfirm,
		EffectiveValue:       agentproto.AccessModeConfirm,
		EffectiveValueSource: "thread",
	})
	text := configPageSummaryText(page)
	if !strings.Contains(text, "当前会话权限（最近观察）\nconfirm") {
		t.Fatalf("expected access page to show observed thread access, got %q", text)
	}
}

func TestAccessConfigPageShowsUnmappedObservedThreadAccess(t *testing.T) {
	page := BuildFeishuCommandConfigPageView(FeishuCatalogConfigView{
		CommandID:            FeishuCommandAccess,
		CatalogBackend:       agentproto.BackendClaude,
		CurrentValue:         "dontAsk（当前无本地精确映射）",
		EffectiveValue:       agentproto.AccessModeConfirm,
		EffectiveValueSource: "thread",
	})
	text := configPageSummaryText(page)
	if !strings.Contains(text, "当前会话权限（最近观察）\ndontAsk（当前无本地精确映射）") {
		t.Fatalf("expected access page to show raw native mode for unmapped projection, got %q", text)
	}
}

func TestAccessConfigPageSeparatesRestoringDefaultsFromPermissionModes(t *testing.T) {
	view := FeishuCatalogConfigView{
		CommandID:      FeishuCommandAccess,
		CatalogBackend: agentproto.BackendCodex,
		EffectiveValue: agentproto.AccessModeFullAccess,
	}
	page := BuildFeishuCommandConfigPageView(view)
	if len(page.Sections) != 1 || page.Sections[0].Title != "权限档位" ||
		len(page.Sections[0].Entries) != 1 || len(page.Sections[0].Entries[0].Buttons) != 2 {
		t.Fatalf("expected only two permission modes with no override, got %#v", page.Sections)
	}
	modes := page.Sections[0].Entries[0].Buttons
	if modes[0].CommandText != "/access full" || modes[1].CommandText != "/access confirm" {
		t.Fatalf("expected full and confirm controls, got %#v", modes)
	}
	if text := configPageSummaryText(page); !strings.Contains(text, "下条消息\nfull_access") ||
		!strings.Contains(text, "飞书覆盖\n无") {
		t.Fatalf("expected effective default permission and no override, got %q", text)
	}

	view.OverrideValue = agentproto.AccessModeFullAccess
	page = BuildFeishuCommandConfigPageView(view)
	if len(page.Sections) != 2 || page.Sections[1].Title != "飞书权限覆盖" ||
		len(page.Sections[1].Entries) != 1 || len(page.Sections[1].Entries[0].Buttons) != 1 {
		t.Fatalf("expected a separate override section, got %#v", page.Sections)
	}
	reset := page.Sections[1].Entries[0].Buttons[0]
	if reset.Label != "恢复默认权限" || reset.CommandText != "/access clear" || reset.Disabled {
		t.Fatalf("expected the existing clear command on the restore button, got %#v", reset)
	}
	if !page.Sections[0].Entries[0].Buttons[0].Disabled {
		t.Fatal("expected the full override to be marked as current")
	}
}

func configPageSummaryText(page FeishuPageView) string {
	var parts []string
	for _, section := range page.SummarySections {
		if section.Label != "" {
			parts = append(parts, section.Label)
		}
		parts = append(parts, section.Lines...)
	}
	return strings.Join(parts, "\n")
}
