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

## Named Agent 媒体交接

具名 Agent 的 `session` 工具可以在创建工作会话或向其发送消息时附上选中的房间媒体，包括 queue 和 steer 模式。这是工具契约，不是新的 JSON-RPC 方法：

```json
{
  "action": "create",
  "workspace_root": "/path/to/project",
  "prompt": "对照实现检查截图",
  "media": [
    {"message_id": "chat_read 返回的消息 ID", "kind": "image", "index": 1,
     "description": "检查底部被裁切的行"}
  ]
}
```

使用 `chat_read` 提供的消息 ID 和附件顺序。`kind` 选择 `image` 或 `file`，`index` 从 1 开始，对应该消息相应数组中的位置。最多选择 32 个不重复附件，随附房间、消息、作者、来源文字和可选说明。省略 `media` 只发送文字，在提示词中写路径不会附上文件；已保存的图片复制时不会再次缩放。

来源必须属于发起回合所在的房间，且具名身份仍有访问权。即使加入了其他房间，也不能跨房间引用。身份和回合范围由宿主提供，路径、URL 和远程缓存引用不能替代持久消息引用。接收会话获得选中的证据，不获得房间访问权或额外文件权限，仍使用自身绑定项目的运行时。

交付前，持久操作保留引用，并在分发或恢复时重新检查访问权和数据。消息不存在、位置无效、数据为空或类型不支持时拒绝交接。接纳后，字节和来源成为持久会话输入；之后删除来源不会撤回已交付副本。恢复时会先识别已有接收记录，再决定是否重新解析来源。

PNG、JPEG、GIF、WebP、PDF 和受支持的视频附件沿用已有媒体路径。视频需要兼容的模型与连接；暂不支持音频、任意文档，以及向外部引擎交接媒体。

选中的媒体属于必需证据。已知模型不兼容时，在接纳输入前报错；provider 请求边界也会拒绝不支持的必需媒体，而不是丢弃它，包括切换模型后仍保留的证据。模型目录能力未知时交给 provider 校验，不保证支持。较早历史仍遵循正常的上下文压缩规则。

Provider 失败通过普通会话结果报告；排队期间的失败记入操作，并在来源对话仍有权限时通知它。不要静默改成纯文字重试，应先选择兼容输入或补足缺失证据。

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
