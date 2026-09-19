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

## 输入内容分段

`turn/start`、`turn/queue`、`turn/update-queued` 和 `turn/steer` 接受可选的
`content_parts`，用于在 `prompt` 之外保存有序的展示元数据。二进制附件仍使用
`images` / `files`。

| `type` | 字段 |
| --- | --- |
| `text` | `text: string` |
| `pasted_text` | `text: string`，可选 `title: string` |
| `file_selection` | `text: string`、`source: FileSelectionSource`、`intent: "comment" \| "edit" \| "quote"`、`id: string`，可选 `comment: string` |

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

## 本地调试

仓库提供 CLI 调试入口，可启动本地服务并发送单个协议请求：

```bash
wuu debug app-server initialize --workdir /path/to/project
wuu debug app-server send thread/start '{}'
```

生产环境的认证、沙箱、组织成员关系、密钥注入和配额由外部控制平面负责，不是
app-server 自身提供的能力。完整方法和参数参考见[英文协议文档](../../en/integrations/app-server-protocol.md)。
