package control

func CodexServiceTierLabel(tier string) string {
	switch tier {
	case "fast":
		return "Fast"
	case "default":
		return "标准"
	case "flex":
		return "Flex"
	case "ultrafast":
		return "Ultrafast"
	default:
		return "未确认（会话运行后读取）"
	}
}
