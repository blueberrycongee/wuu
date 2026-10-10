# 将客户端接入 app-server

需要管理会话、接收回合流或控制执行时，使用 app-server。脚本如果只需提交任务并取得
结果，[`wuu exec`](exec.md) 已经封装了这套生命周期。

## 启动进程

```bash
wuu app-server --workdir /path/to/project
```

让客户端持续连接进程的 stdin 和 stdout。每条协议消息是一行 JSON 对象；stdout 是
协议流，进程诊断应单独处理。未提供显式 `--config` 文件时，服务使用本地配置。
排查插件故障时，`--safe-mode` 可在不激活插件的情况下启动。

先发送 `initialize`：

```json
{"id":"1","method":"initialize","params":{"protocol_version":"wuu-app-server/v0.1","client":{"name":"example-client"}}}
```

响应使用相同的 `id`，并包含 `result` 或 `error`。通知只有 `method` 和 `params`，没有
`id`。检查初始化结果的 `status` 和 `issues`：协议响应成功，仍可能返回 `needs_setup`。

## 运行任务

创建会话，再使用返回的 `result.thread.id`：

```json
{"id":"2","method":"thread/start","params":{}}
{"id":"3","method":"run/start","params":{"thread_id":"THREAD_ID","prompt":"总结这个仓库","request":{"mode":"start"}}}
```

这是有先后关系的两个请求，不能原样作为批次提交。收到第一个响应后，将 `THREAD_ID`
替换为实际 ID。持续读取通知，直到 `run/updated` 为返回的运行 ID 报告终态。
`run/start` 响应表示执行已被接纳，不表示任务完成。

交互式客户端也可以使用 `turn/start`，接收回合和条目更新。自动化执行可能包含多个续接或
Schema 修正回合，因此单个 `turn/completed` 不代表整次运行结束。用 `run/interrupt`
停止运行；关闭由客户端启动的服务前，先请求 `shutdown`。

## 托管浏览器请求

内嵌浏览器客户端应协商全部六个反向请求方法：`browser/cdp`、`browser/screenshot`、
`browser/open_tab`、`browser/close_tab`、`browser/set_visibility` 和 `browser/list_tabs`。
这些请求由核心发给客户端；客户端应持续读取协议流，并以原请求 ID 返回结果或错误。

每个浏览器请求都带核心指定的 `thread_id` 和 `workdir`。标签页创建、列举、输入、截图和
清理按对话隔离；页面打开的标签页继承来源页的归属。现有标签页属于其他对话时，应拒绝
请求，不得导航或接管该标签页。

`request_id` 标识进行中的操作。核心停止等待时，`browser/request_cancelled` 通知携带
该 ID 及相同的 `thread_id`、`workdir`。只取消匹配的操作，在异步工作后和输入派发前
再次检查。用户接管或停止活动会撤销该对话所有标签页的输入权限；之后归还控制权可
授权新操作，但不能恢复已经撤销的请求。

## 复用会话

`thread/resume` 接受 `session_id`，省略时选择工作区最近的可见会话。`thread/fork` 接受
`thread_id`，创建独立对话。`thread/start` 的 `ephemeral: true` 创建内存会话，服务退出后
无法恢复。

每个对话保留自己的模型和权限选择。应在对话空闲时，用带 `thread_id` 的
`config/model/update` 修改；这不会改变工作区默认值。对话忙时返回 `thread_busy`，客户端可
等待后重试。不带 `thread_id` 的请求修改未来
对话的默认设置；加上 `keep_selection: true` 则只保存服务的连接，不把它设为默认。不要通过 `turn/start` 临时覆盖单个回合的权限模式。

## 删除已归档对话

`thread/listArchived` 返回当前会话存储中的全部已归档对话，包括其他工作区的对话。
删除这份列表快照时，为每个 ID 单独调用 `thread/delete`，并设置
`only_if_archived: true`。每次请求独立执行永久删除，客户端应报告各项失败。
如果对话在删除事务开始前已恢复，服务会拒绝删除，保留聊天记录和侧聊内容。
正在运行的对话、活跃子 Agent 或正在运行的侧聊也会阻止删除。
省略 `only_if_archived` 时，仍可删除普通空闲对话。

## 运行项目

`thread/start` 带 `project: {"name": "..."}` 时，在工作区创建项目主 Agent。返回的会话带
`source: "project"`，遵循普通会话的模型和权限设置。主 Agent 可以直接动手，通过
`session` 工具管理其他会话。每个托管会话都是普通会话，带
`source: "project-session"`，`project_id` 指向其协调者；它的 `session_control` 以
`manager_name` 给出项目名。worktree 会话的改动留在它的 worktree 里，由团队用普通的 Git
命令交付。提交、合并、推送和 PR 修改均需用户授权或该操作已有的工作流程；委派不能扩大权限。

托管会话提供 `project_role: "side" | "worker"`，旧成员默认视为 Worker。
`session create` 接受 `role`（默认 worker）和可选的 `model_alias`。只有主 Agent
能创建 Side；重复创建会返回已有的活跃 Side，不会发送新任务。主 Agent 和 Side
都能创建 Worker。活跃成员均可 `list`、`inspect` 和 `message`，只有主 Agent 能
`send` 和 `stop`。消息仅限项目内，包含 `prompt`、`session_id` 和可选的 `wake`
（默认 false）。停止成员或直接给它发消息不会改变项目成员关系。停止会使之前排队的
自动输入失效；之后仍可明确派发新任务，无需归还控制权。

`session side` 创建或继续持久 Side 并发送新的任务简报，运行中会引导当前回合。Side
默认共享工作区，Worker 在 Git 工作区中默认使用隔离 worktree。派发持久化并防止重放
重复，首个简报与会话创建一起提交；忙时返回 `state: "queued"`，恢复后重试。

`side` 默认等待，`create` 和 `send` 默认立即返回，除非设置 `block: true`。
`wait` 接受 `session_id` 和可选的精确 `turn_id`；主 Agent 可等待成员，Side 可等待
Worker。结果包含 `turn_id`、`turn_status` 和终态 `final_output`。`timeout_ms` 默认
60000，上限为 300000；超时返回 `timed_out: true`。超时或调用方取消都不会停止子会话。

主 Agent 使用会话模型。`agent.project_models.side` 和 `.worker` 为新成员指定服务
和模型，空选择继承主 Agent。创建时明确传入的 `model_alias` 优先于角色默认值，已有
会话保留保存的模型。`config/advanced/update` 接受同结构的 `project_models`；初始化、
配置读取、更新和配置变更事件返回当前默认值。在 `features.project_agent` 为 true
的构建中，桌面运行设置提供这两项选择。

协调者以用户条目接收宿主事件，条目带 `origin: "host"`，`related_session_id` 指向相关会话，
`cause` 给出事件：

| Cause | 事件 | 协调者空闲时是否开始新回合 |
|---|---|---|
| `project_message` | 成员发来的消息，主 Agent 或其他成员均可接收 | 仅当 `wake` 为 true |
| `project_result` | 托管会话的一个回合结束，附带用户在其中写的内容 | 是，中断的回合除外 |
| `project_stopped` | 用户停止了成员的当前回合 | 否，随下一个回合送达 |
| `project_user_message` | 用户直接给成员发了消息 | 否，随下一个回合送达 |
| `project_adopted` | 用户把对话加入了项目 | 是 |
| `project_released` | 用户把会话移出了项目 | 否，随下一个回合送达 |

在项目成员中开始、引导、排队或中断回合，成员关系仍保持活跃。用户直接发消息会通知协调者，
但不会唤醒空闲的协调者。中断结果也会送达而不唤醒空闲的协调者；正常结果会唤醒它。
恢复后补发的中断结果，以及发给派遣该成员的 Side Agent 的报告，也遵循相同的不唤醒规则。
worktree 改动留在成员的 worktree 里。`thread/control/take` 和 `thread/control/return` 的控制权生命周期
用于插件托管会话，不用于项目成员。

`project/session` 修改项目成员：`adopt` 传入 `project_id` 和 `session_id`，把项目所在工作区
的普通对话交给项目管理；`release` 让托管会话重新成为普通对话。两者都返回更新后的 `thread`。

## 输入内容分段

`turn/start`、`turn/queue`、`turn/update-queued` 和 `turn/steer` 接受可选的
`content_parts`，用于在 `prompt` 之外保存有序的展示元数据。二进制附件仍使用
`images` / `files`。

| `type` | 字段 |
| --- | --- |
| `text` | `text: string` |
| `pasted_text` | `text: string`，可选 `title: string` |
| `file_selection` | `text: string`、`source: FileSelectionSource`、`intent: "comment" \| "edit" \| "quote"`、`id: string`，可选 `comment: string` |
| `response_selection` | `text: string`、`selection: ResponseSelection`，引用捕获时已显示的助手回复，支持仍在输出的回复 |

`FileSelectionSource` 包含字符串字段 `workspace`、`path`、`quote`、`revision`，
以及整数字段 `start_line`、`start_column`、`end_line`、`end_column`。行号和
UTF-16 列号均从 1 开始，结束位置不包含在选区内。`revision` 标识捕获时的文件
内容，不一定是 Git 版本号。服务端检查必需元数据、意图值、正数坐标及范围顺序，
不读取文件或核实其版本。

文件选区的 `text` 必须包含模型可见的完整序列化文本块，包括来源上下文、意图和
可选评论。客户端按顺序拼接各段文本形成 `prompt`。只有拼接结果与提交的提示词
在去除首尾空白后相同，服务端才保留有效元数据；服务端不重建或解析客户端的
序列化格式。未知或无效分段会被丢弃，文本不匹配时丢弃展示元数据，但不修改
提示词。Provider 和引擎使用规范消息文本；Codex 接收普通文本输入，无需特殊的
`text_elements`。

已接受的元数据会随历史和暂存队列回放保留。客户端必须容忍未知字段和分段类型；
无法展示元数据时，回退到完整的消息 `text` 或队列 `prompt`。模型所需的上下文
不能只保存在元数据中。

## 查询订阅状态

`engine/list` 可选参数 `{ "include_quota": true }` 使用各模型服务和支持的外部 Agent 自己的凭据查询上游额度，返回已配置服务 `subscription_providers`。服务或引擎可附带 `quota`：

- `status`：`available`、`stale`、`unavailable`、`sign_in` 或 `unsupported`。
- `kind`：订阅、套餐或余额；`plan` 为可选的上游套餐信息。
- `account`：不透明、按来源隔离的 `id`，可选账号 `label` 和凭据 `source`；不返回 token 或密钥。
- `checked_at` 为最近尝试时间，`observed_at` 为最后成功读取时间，`expires_at` 为新鲜度期限，目前为成功读取后五分钟。
- `windows` 提供 `id`，以及可选的 `label`、`used_percent`、`window_minutes`、`resets_at`、`model`、`scope`、`display` 和上游明确报告的 `unlimited`。
- `balances` 保留币种及十进制字符串 `amount`，不合并不同币种或账号；可选 `reset_credits` 表示上游重置次数。
- `error_code` 为安全的失败类别，不包含上游原始响应。

已接入原生 ChatGPT/Codex、Grok Build 订阅、Anthropic OAuth、Kimi Code 和智谱/Z.ai 套餐周期，以及 DeepSeek/OpenRouter 预付余额。外部 Codex 通过 CLI 账号请求读取；外部 Claude Code 和 Grok 使用自己的本机登录。macOS 默认 Claude 登录可读取钥匙串，但自定义凭据目录不会借用默认账号。浏览器 SuperGrok、任意兼容端点和没有适配器的 Agent 暂不支持。

缺失百分比表示未知，不是零或无限；上游可用 100% 或更大数值表示耗尽。ACP 上下文占用和本地 token 不能推导账号额度，过期快照也不能在重置时间自行补满。暂时读取失败且仍能确认账号时，保留该凭据来源的最后成功快照为 `stale`，不改成功读取时间或到期时间；认证被拒绝时丢弃该账号的缓存额度。快照跨服务重启保留，更换凭据不会继承另一个账号的数据。所有来源的采集共用八秒期限。额度查询不发起推理，不更改模型选择、工作区默认值或路由策略。

只有上述按需订阅响应读取历史统计。`initialize`、配置响应、普通 `engine/list` 和 `engine/update` 不附带 `latest_request` 与 `local_usage`；需要统计的客户端应独立加载订阅快照，不阻塞导航。所有请求来源共用一次历史扫描；扫描失败时保留服务清单，统计字段缺失而非零值。

该响应中引擎及订阅来源的 `local_usage` 汇总本地保留历史中已上报的输入、输出、缓存 token 和 `reported_turns`，不含未上报或 Wuu 外用量，也不是账单。可选 `latest_request` 提供最近请求的 `status`、`error`、`at`、`model`、`usage_reported`，有上报时附带 token 计数。新旧顺序按请求时间判断，来源按持久记录归属；对话编辑和供应商切换不改变历史归属，旧请求用量不得填充到新请求。

## 查询用量概览

`usage/overview` 汇总本地保留历史中已记录的 token 用量：`total_sessions`（至少有一条用量记录的会话数）、`metrics`（与 `settings/usage` 相同的汇总对象，含 `active_days`）和 `days`（每个活跃日一条，按日期升序）。它只读取用量记录，不读取对话内容。可选参数 `{ "timezone": "America/Los_Angeles" }` 按 IANA 时区划分日期；省略时使用 UTC，未知时区返回请求错误。没有记录时返回零值和空的 `days`。与 `local_usage` 一样，这些值不含未上报或 Wuu 外的用量，也不是账单。桌面「用量」页使用的完整快照 `settings/usage` 接受同样的可选 `timezone`；桌面端会带上自己的时区，让每天的柱子落在日历对应的那一天。

## 探查协议

```bash
wuu debug app-server initialize --workdir /path/to/project
wuu debug app-server send --workdir /path/to/project config/read '{}'
```

每条调试命令都会启动服务、完成请求并关闭服务。流式执行需要持续连接的客户端，不应靠
串联独立探查命令实现。

stdio 协议是受信任的本地控制接口，不是带认证的网络服务。远程或托管部署必须在外围提供
传输安全和隔离。[协议参考](../../en/integrations/app-server-protocol.md)（英文）说明消息
格式、能力协商、选择规则和云端进程身份。
