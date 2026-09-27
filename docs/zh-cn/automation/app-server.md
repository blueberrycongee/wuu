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

## 复用会话

`thread/resume` 接受 `session_id`，省略时选择工作区最近的可见会话。`thread/fork` 接受
`thread_id`，创建独立对话。`thread/start` 的 `ephemeral: true` 创建内存会话，服务退出后
无法恢复。

每个对话保留自己的模型和权限选择。应在对话空闲时，用带 `thread_id` 的
`config/model/update` 修改；这不会改变工作区默认值。对话忙时返回 `thread_busy`，客户端可
等待后重试。不带 `thread_id` 的请求修改未来
对话的默认设置。不要通过 `turn/start` 临时覆盖单个回合的权限模式。

## 运行项目

`thread/start` 带 `project: {"name": "..."}` 时，在工作区创建项目主 Agent。返回的会话带
`source: "project"`，遵循普通会话的模型和权限设置。主 Agent 可以直接动手，通过
`session` 工具管理其他会话。每个托管会话都是普通会话，带
`source: "project-session"`，`project_id` 指向其协调者；它的 `session_control` 以
`manager_name` 给出项目名。worktree 会话的改动留在它的 worktree 里，由团队用普通的 Git
命令交付；协议里没有等待用户决定的步骤。

托管会话提供 `project_role: "side" | "worker"`，旧成员默认视为 Worker。
`session create` 接受 `role`（默认 worker）和可选的 `model_alias`。只有主 Agent
能创建 Side；重复创建会返回已有的活跃 Side，不会发送新任务。主 Agent 和 Side
都能创建 Worker。活跃成员均可 `list`、`inspect` 和 `message`，只有主 Agent 能
`send` 和 `stop`。消息仅限项目内，包含 `prompt`、`session_id` 和可选的 `wake`
（默认 false）。停止成员或直接给它发消息不会改变项目成员关系，也不会使排队中的团队消息失效。

协调者以用户条目接收宿主事件，条目带 `origin: "plugin"`，`related_session_id` 指向相关会话，
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

## 查询订阅状态

`engine/list` 可选参数 `{ "include_quota": true }` 通过支持的本地 CLI（目前为 Codex）读取账户额度，并返回内置订阅来源 `subscription_providers`。`quota` 包含 `status`（`available` 或 `unavailable`）、`checked_at` 和可选 `windows`；窗口提供 `id`、`label`、`used_percent`、`window_minutes`、`resets_at`。缺失额度表示未支持或未查询，不代表无限额度。过期快照应提示刷新，不能在重置时间自行补满。

引擎及订阅来源的 `local_usage` 汇总本地保留历史中已上报的输入、输出、缓存 token 和 `reported_turns`，不含未上报或 Wuu 外用量，也不是账单。可选 `latest_request` 提供最近请求的 `status`、`error`、`at`、`model`、`usage_reported`，有上报时附带 token 计数。新旧顺序按请求时间判断，来源按持久记录归属；对话编辑和供应商切换不改变历史归属，旧请求用量不得填充到新请求。

## 查询用量概览

`usage/overview` 汇总本地保留历史中已记录的 token 用量：`total_sessions`（至少有一条用量记录的会话数）、`metrics`（与 `settings/usage` 相同的汇总对象，含 `active_days`）和 `days`（每个活跃日一条，按日期升序）。它只读取用量记录，不读取对话内容。可选参数 `{ "timezone": "America/Los_Angeles" }` 按 IANA 时区划分日期；省略时使用 UTC，未知时区返回请求错误。没有记录时返回零值和空的 `days`。与 `local_usage` 一样，这些值不含未上报或 Wuu 外的用量，也不是账单。

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
