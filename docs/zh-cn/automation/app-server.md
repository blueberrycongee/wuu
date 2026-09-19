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
`config/model/update` 修改；这不会改变工作区默认值。不带 `thread_id` 的请求修改未来
对话的默认设置。不要通过 `turn/start` 临时覆盖单个回合的权限模式。

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
