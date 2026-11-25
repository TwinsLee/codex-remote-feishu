package control

func fastCommandSpec() feishuCommandSpec {
	return feishuCommandSpec{
		definition: FeishuCommandDefinition{
			ID:               FeishuCommandFast,
			GroupID:          FeishuCommandGroupSendSettings,
			Title:            "速度档位",
			CanonicalSlash:   "/fast",
			CanonicalMenuKey: "fast",
			ArgumentKind:     FeishuCommandArgumentNone,
			Description:      "查看或切换当前 Codex 会话的速度档位，下一条消息开始生效。",
			Examples:         []string{"/fast", "/fast on", "/fast off"},
			ShowInHelp:       true,
			ShowInMenu:       true,
		},
		textPrefixes: []feishuCommandPrefixMatch{
			{alias: "/fast", kind: ActionFastCommand},
		},
		menuExact: []feishuCommandMatch{
			{alias: "fast", action: Action{Kind: ActionFastCommand, Text: "/fast"}},
		},
	}
}
