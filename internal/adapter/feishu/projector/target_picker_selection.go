package projector

import (
	"strings"

	"github.com/kxn/codex-remote-feishu/internal/adapter/feishu/cardkit"
	"github.com/kxn/codex-remote-feishu/internal/adapter/feishu/selectflow"
	"github.com/kxn/codex-remote-feishu/internal/core/control"
	"github.com/kxn/codex-remote-feishu/internal/xutil"
)

const (
	targetPickerSessionDropdownNameLimit = 80
	targetPickerSessionDetailNameLimit   = 1000
)

func targetPickerSessionOptionLabel(option control.FeishuTargetPickerSessionOption) string {
	label := strings.TrimSpace(option.Label)
	if option.Kind != control.FeishuTargetPickerSessionThread {
		return label
	}
	label = xutil.TruncateRunes(label, targetPickerSessionDropdownNameLimit, xutil.TruncateOptions{CollapseSpaces: true, ReserveEllipsis: true})
	activity := "时间未知"
	if !option.LastUsedAt.IsZero() {
		activity = option.LastUsedAt.Local().Format("01-02 15:04")
	}
	return activity + " · " + label
}

func targetPickerRenderLaneElements(view control.FeishuTargetPickerView, lane paginatedSelectFlowLane, daemonLifecycleID string, page paginatedSelectPage) []map[string]any {
	elements := lane.renderElements(daemonLifecycleID, page)
	targetPickerFillSelectWidth(elements)
	if page.InitialOption == "" || len(elements) < 2 {
		return elements
	}
	var detail string
	switch lane.Flow.FieldName {
	case selectflow.TargetPickerWorkspaceFlow.FieldName:
		if path := strings.TrimSpace(view.SelectedWorkspaceKey); path != "" {
			detail = "完整路径：" + path
		}
	case selectflow.TargetPickerSessionFlow.FieldName:
		label, meta := view.SelectedSessionLabel, view.SelectedSessionMeta
		var kind control.FeishuTargetPickerSessionKind
		for _, option := range view.SessionOptions {
			if option.Value == page.InitialOption {
				label = xutil.FirstNonEmpty(label, option.Label)
				meta = xutil.FirstNonEmpty(meta, option.MetaText)
				kind = option.Kind
				break
			}
		}
		if kind == control.FeishuTargetPickerSessionThread {
			if label = strings.TrimSpace(label); label != "" {
				if len([]rune(label)) > targetPickerSessionDetailNameLimit {
					label = "会话名称（名称过长，已截断）：" + xutil.TruncateRunes(label, targetPickerSessionDetailNameLimit, xutil.TruncateOptions{ReserveEllipsis: true})
				} else {
					label = "完整名称：" + label
				}
			}
			if meta = strings.TrimSpace(meta); meta != "" {
				meta = "会话工作目录：" + meta
			}
		}
		lines := make([]string, 0, 2)
		for _, value := range []string{label, meta} {
			if value = strings.TrimSpace(value); value != "" {
				lines = append(lines, value)
			}
		}
		detail = strings.Join(lines, "\n")
	}
	if block := cardkit.PlainTextBlockElement(detail); len(block) != 0 {
		// Keep the full context directly after the selector, ahead of pagination hints.
		return targetPickerCombinePageElements(elements[:2], []map[string]any{block}, elements[2:])
	}
	return elements
}

func targetPickerFillSelectWidth(value any) {
	switch typed := value.(type) {
	case map[string]any:
		if typed["tag"] == "select_static" {
			typed["width"] = "fill"
		}
		for _, child := range typed {
			targetPickerFillSelectWidth(child)
		}
	case []map[string]any:
		for _, child := range typed {
			targetPickerFillSelectWidth(child)
		}
	case []any:
		for _, child := range typed {
			targetPickerFillSelectWidth(child)
		}
	}
}
