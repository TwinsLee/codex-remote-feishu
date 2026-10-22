# Goal 回显与选择卡可靠性修复

> **For agentic workers:** Use test-driven development and the repository state-machine guardrails. Implement only confirmed defects; keep existing Goal scheduling semantics.

**Goal:** 保住 Goal 核心控制功能，修复误导性回显，并简化工作区/会话选择卡。

**Architecture:** 复用现有命令卡与投递链路，区分文字入口与有效卡片入口。复用当前会话数据完成选择卡展示，保留归组、接管、Goal 调度架构和持久化协议，只收紧已确认不安全的控制与恢复条件。

**Tech Stack:** Go，飞书 JSON 2.0 卡片，现有 Codex Goal RPC。

**Spec:** 本文件记录用户在本会话已确认的范围与保守实施约束。

## Global Constraints

- 不操作真实用户 Goal，不发送飞书消息，不安装二进制，不重启 launchd 服务。
- 不重构 Goal 的暂停、恢复、队列互锁和自动续跑机制；只修有复现证据的缺陷。
- 复用既有 callback kind、目标字段和 catalog 信息，不新增 owner、持久化格式或调度状态；不改变会话归组、工作目录、接管和释放语义。
- 工作区下拉只显示末级目录名；选中后展示完整路径。
- 会话下拉显示最近活动时间和真正名称；目录信息独立展示；不显示 ID。
- 完整信息顺序与选择顺序相同，不重复已经前置的时间；两个下拉撑满可用宽度。

## Review Focus

- 文字命令不可 patch 用户文字消息；卡片入口仍原位更新。
- 查询确认不存在 Goal 时清除旧缓存并显示无 Goal；未知结果不能报成功。
- 异步结果不能落到已经切换的另一会话上；旧表单或按钮不能操作后来选中的另一目标。
- 用户成功控制后撤销旧自动恢复权，保留尚未完成的 idle 屏障；自动恢复仅允许仍为 paused 的 Goal。
- 用户操作不能被重复执行。
- 投递失败及时反馈，但不新增无限重试、重复通知或操作重放。
- 长名称、重复目录名及工作区与会话工作目录不同，不能丢失核对信息。极端长名仅截断显示并明确标注，不能修改真实名称或目标。

### Task 1: Goal 控制与回显

- [x] 在 `service_goal_command_*test.go` 补充文字/卡片入口、查询空结果、未确认清除、会话切换的回归，先观察失败。
- [x] 在 `service_goal_command.go` 和必要的现有结果处理处做最小修正。
- [x] 回归现有 new/edit/pause/resume/clear、旧卡校验、队列互锁及通知翻译测试；正常路径不改调度语义。

### Task 2: 错误通知

- [x] 在 daemon 中先复现通知延迟和不恰当的可重试标记。
- [x] 沿现有投递路径最小修复，保留去重与防递归；不重放 Goal 操作。
- [x] 运行 daemon 对应回归，确认网络持续失败不会刷屏。

### Task 3: 选择卡显示

- [x] 在 orchestrator/projector 补充已确认布局的回归。
- [x] 修改下拉标签与核对区，复用原始名称、选中工作区路径和会话 CWD；不靠截字符串删除真实标题。
- [x] 验证长名称、同名目录、分页、原有创建入口以及原位更新；不改变默认选择与排序规则。

### Task 4: 审阅与验证

- [x] 同步 canonical 卡片/状态机文档中的实际变更。
- [x] 运行 focused tests、必要的全套 Go 测试及 `scripts/check/pre-commit.sh`。
- [x] 独立审阅完整 diff；说明已验证内容、剩余限制及尚未安装的事实。

## 验证结果

- Goal 文字/卡片、目标绑定、手动控制与自动恢复时序，以及选择卡、错误通知的回归通过；关键缺陷均先观察失败，再确认修复。
- `go test ./...` 通过；`scripts/check/pre-commit.sh` 通过，现有跨平台路径提示为非阻断项。
- Goal 独立复审的 8 条隔离复现通过；选择卡与通知的独立静态复审未发现阻断问题。
- 仅完成本地代码与文档，不推送、不安装、不重启 Remote；未操作真实用户 Goal 或发送飞书消息。上线后的实际飞书交互尚待验证。
