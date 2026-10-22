package control

const (
	FeishuCommandDesk                             = "desk"
	ActionDesk                  ActionKind        = "surface.button.desk"
	DaemonCommandDesktopHandoff DaemonCommandKind = "headless.desktop_handoff"
)

func deskCommandSpec() feishuCommandSpec {
	return feishuCommandSpec{
		definition: FeishuCommandDefinition{
			ID: FeishuCommandDesk, GroupID: FeishuCommandGroupSwitchTarget,
			Title: "交给桌面", CanonicalSlash: "/desk", ArgumentKind: FeishuCommandArgumentNone,
			Description: "仅在空闲时释放当前 Remote 托管的 Codex 实例，确认退出后交给桌面继续；不影响其他 Bot。",
			ShowInHelp:  true,
		},
		textExact: []feishuCommandMatch{{alias: "/desk", action: Action{Kind: ActionDesk}}},
	}
}
