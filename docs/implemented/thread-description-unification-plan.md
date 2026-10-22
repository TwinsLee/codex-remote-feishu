# 会话描述统一方案

> Type: `implemented`
> Updated: `2026-10-20`
> Summary: 统一 DisplayName，并在列表与状态投影中读取 Codex 桌面显式名称，保留旧数据库兼容和失败缓存边界。

## 背景

同一个 thread 过去会在 `/use`、切换提示、强踢确认、`/history`、`/status`、admin runtime surface 等入口显示成不同东西：有的用 rename/name，有的用首条 user，有的用最近 user，有的又会退回 assistant / preview / raw id。

这次实现的目标不是单点改文案，而是把“thread 到底显示什么”收口成一个统一 contract，并让各层不再各自 fallback。

## 最终 contract

### 1. Canonical `DisplayName`

实际 thread 的主显示名统一为：

`<workspace 最后一节> · <text>`

其中 `<text>` 优先级固定为：

1. 用户显式 rename 后的 `Name`
2. 最近一条用户消息前缀
3. 第一条用户消息前缀
4. 当以上都缺失时，固定退到 `未命名会话`

以下内容不再参与 thread 主显示名决策：

- `LastAssistantMessage`
- `Preview`
- `ThreadID`
- short id

### 2. 非 `/status` 场景

凡是告诉用户“当前是哪一个 thread / 正在选哪一个 thread / 要恢复哪一个 thread”的场景，都只显示 canonical `DisplayName`。

这包括：

- `/use` thread 选择
- VS Code thread 下拉
- attach / switch / follow notice
- kick prompt
- `/history` summary
- request prompt
- compact owner card
- admin runtime surface 的 thread 主标题

这些场景允许保留与 thread 身份无关的状态 meta，例如占用状态、相对时间、实例状态；但不再拼会话内容型副标题。

### 3. `/status` 例外

`/status` 是唯一允许额外显示 thread 上下文的受控例外。当前只展示：

- 当前会话：canonical `DisplayName`
- 会话起点：`FirstUserMessage`
- 最近用户：`LastUserMessage`

`/status` 不再显示 assistant/preview 作为 thread 身份补充。

### 4. 伪状态

以下不是实际 thread，继续保留独立文案，不强行套用 canonical 命名：

- `未绑定会话`
- `跟随当前 VS Code（等待中）`
- `新建会话（等待首条消息）`

## Contract 收口点

### Producer / helper

- `internal/core/threadtitle` 现在是唯一 thread title owner，统一提供：
  - raw semantic title
  - display body / display title
  - workspace prefix
  - stored title
  - legacy display 输入归一化
- orchestrator 的 `threadTitle / displayThreadTitle / threadSelectionButtonLabel` 已降为 owner package 的薄包装，不再独立维护 title contract
- preview/assistant 已移出主显示名链路
- persisted sqlite thread 会把旧库里的 `firstUserMessage` 映射到 `FirstUserMessage`，不再伪装成 `Name`

### Carrier

以下 carrier 已去掉对旧命名源的放权：

- `control.ThreadSelectionChanged`
- `control.FeishuThreadSelectionEntry`
- `control.AttachmentSummary`

其中：

- selection carrier 不再暴露 first/last/assistant/preview 让下游自己选名字
- snapshot attachment 只保留 `/status` 需要的 `FirstUserMessage / LastUserMessage`

### Consumer

以下消费面已切到新 contract：

- selection projector
- kick prompt
- `/history` summary
- request prompt
- compact owner card
- `/status`
- admin runtime surface

## Legacy 兼容

### 0. Codex 桌面改名同步

- Codex SQLite catalog 每次读取探测可选 `name` 列；非空显式名优先，旧 schema、NULL／空白名称继续使用旧 `title` 回退。只读查询，不迁移或修改 Codex 数据库；原始用户消息不被名称替换。
- 成功读取的显式名独立于 `updated_at`／运行态 `LastUsedAt`：桌面仅改名、会话仍活跃时，下次列表或状态读取也能更新。`PersistedName` 仅为本次读取的内部投影标记，不序列化到 daemon 状态。
- recent 查询失败时复用的缓存会清除此标记，不把旧缓存当作新的改名事实。名称按当前展示的 thread ID 批量只读查询，每批最多 200 个，不受全库 recent 窗口限制；读取失败时保留运行态名称。没有批量能力的旧 catalog 仍可按 ID 查询。
- headless 合并列表、当前实例下拉及 snapshot 共用上述规则；snapshot／当前实例投影不改写运行态线程。工作区前缀、CWD、claims、route、队列、锁和活跃时间沿用原逻辑，其他 backend 不读取 Codex 名称。

### 1. 旧线程 / 持久化线程

旧线程如果没有 rename/name，也没有 first/last user message，不再回退到 preview，而是显示：

`<workspace> · 未命名会话`

这样可以避免 assistant/preview 在不同入口再次拥有命名权。

### 2. Resume / headless restore

resume / headless restore 不再由 `surfaceresume` 独立维护标题归一化 contract。

当前规则是：

- surface resume store、headless restore hint、resume target 一律走 `internal/core/threadtitle`
- 历史遗留的 workspace prefix / short-id suffix 只允许在 owner package 内部做有界吸收
- 如果拿不到可复用的原始标题，不再退回 `threadID` 或保留 `未命名会话` 作为 stored raw title

恢复后的最终用户可见标题仍在最后一跳重新走 canonical display contract。

## 验证面

当前回归覆盖至少包括：

- rename / 最近 user / 首条 user / 全缺失兜底
- 非 `/status` 场景不再使用 preview/assistant/id 命名
- VS Code `/use` 下拉
- kick prompt / compact owner card
- `/history`
- `/status`
- admin runtime surface
- resume / headless restore 兼容
- stored raw title 与 display format 解耦
