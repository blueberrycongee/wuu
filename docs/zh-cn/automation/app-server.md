# App-server 集成入门

App-server 是 Wuu 核心与桌面端、脚本或编辑器外壳之间的协议边界。需要构建新的
客户端时，应复用这套协议，而不是在外壳中重新实现 Agent 循环。

## 传输格式

当前协议通过标准输入输出传输**逐行 JSON（JSONL）**。每个请求带有 `id`、`method` 和
可选的 `params`：

```json
{"id":"1","method":"initialize","params":{}}
```

成功响应使用同一个 `id`：

```json
{"id":"1","result":{}}
```

错误响应包含 `error`；通知消息没有 `id`，例如：

```json
{"method":"turn/completed","params":{}}
```

`initialize` 返回当前协议版本 `wuu-app-server/v0.1`。这是受控集成协议，字段仍可能
演进；客户端应根据方法和事件类型处理消息，不要依赖自然语言错误文本。

## 一次任务的生命周期

客户端应按以下顺序驱动核心：

1. `initialize`：建立连接并取得能力、配置和协议版本；
2. `thread/start` 或 `thread/resume`：创建或恢复会话；
3. `turn/start`：桌面等交互式客户端启动单轮任务，或使用 `run/start` 启动
   `wuu exec` 使用的自动化任务；
4. 消费 `turn/*` 通知，并等待 `run/updated` 进入终态（自动化 Run）；
5. `shutdown`：客户端退出时请求干净关闭。

`thread/start` 默认创建持久会话；传入 `{"ephemeral": true}` 的会话只存在于内存，
服务退出后不能恢复。`thread/fork` 可从已有会话、回合或条目创建新分支。

## 常用方法

| 方法 | 用途 |
| --- | --- |
| `thread/start` | 创建会话 |
| `thread/resume` | 恢复会话；空会话 ID 表示最近可见会话 |
| `thread/fork` | 从已有会话创建分支 |
| `turn/start` | 启动交互式单轮，可包含附件 |
| `run/start` | 启动自动化 Run，供 `wuu exec` 使用 |
| `turn/interrupt` | 中断单轮任务 |
| `run/interrupt` | 中断自动化 Run |
| `shutdown` | 请求服务端关闭 |

模型和权限模式属于会话选择。要修改它们，应先调用 `config/model/update`，而不是在
单轮请求中临时覆盖；正在运行的会话不能修改本轮已经采纳的模型或权限模式。

## Named Agent 媒体交接

具名 Agent 的 `session` 工具在 `create` 和 `send`（queue 或 steer）时可传入可选的
`media` 数组。这是工具参数，不是新的 JSON-RPC 方法：

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

每条引用选择房间持久消息中的一个附件。`kind` 为 `image` 或 `file`；`index` 从 1
开始，对应该消息 `images` 或 `files` 数组的位置。`chat_read` 提供消息 ID 和附件顺序。
最多可选择 32 个附件，随附房间、消息、作者来源、原消息文字及可选的逐附件说明。
省略 `media` 只发送文字；在 `prompt` 中写路径不会附上图片。交接复制的是上传处理后
保存的图片，不会再次缩放。

来源必须属于发起回合所在的房间，且该身份仍有访问权。即使身份同时加入其他房间，也
不能引用其他房间的附件。身份和回合范围由主机提供；文件路径、任意 URL 或远程缓存
引用不能绕过此检查。普通接收会话只获得选中的证据，不获得房间访问权或额外文件权限。
即使身份运行在另一个项目，接收方仍使用自身绑定项目的运行时与既有权限策略。

持久操作保存引用，在分发及队列恢复时重新检查访问权和附件数据。消息不存在、附件
序号失效、内容为空或类型不支持，都会拒绝整个交接。接纳后，字节和来源进入持久会话
输入；之后删除来源不会撤回已经交付的副本。支持 PNG/JPEG/GIF/WebP 图片，并复用现有
PDF 和视频附件路径；视频仍要求模型与连接均支持。暂不支持音频、任意文档及外部引擎
的媒体交接。

已知模型不支持时，在接纳输入前报错。必需证据也会在 provider 请求边界报错，不会变成
“不支持而省略”的标记；切换模型后仍保留在上下文中的媒体同样受保护。模型目录能力
未知时沿用原有透传及 provider 校验，不代表保证支持。Provider 或连接失败按正常会话
结果结束并报告；排队期间的失败写入操作状态，并在来源对话仍有权限时通知它。不能在未决定如何补足
证据时自动改为纯文字重试。较早历史仍沿用正常的上下文压缩与媒体恢复规则。

开发数据流为 `HarnessSessionTool` → 已认证的 `AgentClient` → 持久 Harness 操作 →
房间范围内附件解析 → `ChatMessage.Images/Files` → 会话接纳与历史 → provider 媒体
编码。以下回归使用生成图片和本地回环 HTTP，不读取私密照片，也不调用线上推理服务：

```bash
go test ./internal/appserver ./internal/channels ./internal/providers \
  -run 'TestHarnessMedia|TestHarnessWorkspace|TestRequiredMedia' -count=1
```

## 本地调试

仓库提供 CLI 调试入口，可启动本地服务并发送单个协议请求：

```bash
wuu debug app-server initialize --workdir /path/to/project
wuu debug app-server send thread/start '{}'
```

生产环境的认证、沙箱、组织成员关系、密钥注入和配额由外部控制平面负责，不是
app-server 自身提供的能力。完整方法和参数参考见[英文协议文档](../../en/integrations/app-server-protocol.md)。

