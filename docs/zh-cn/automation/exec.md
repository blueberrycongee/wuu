# 用 `wuu exec` 运行任务

`wuu exec` 无需打开桌面即可运行 Agent 任务，适合 shell、CI、系统定时任务或其他 Agent
调用。它使用 Wuu 的会话和执行服务；使用前先[配置模型服务](../getting-started/model-services.md)。

## 提供任务

```bash
wuu exec --workdir /path/to/repo "修复失败的测试并验证结果"
wuu exec - < task.md
wuu exec "调查这次失败" < error.log
```

选项必须放在提示词或会话 ID 前，参数解析会在第一个位置参数处停止。没有位置提示词时，
管道 stdin 就是任务；两者都有时，stdin 会放在 `<stdin>` 块中追加。任务至少需要文本或
附件；单独使用 `-` 则明确要求 stdin 非空。

用可重复的 `--image` 附加本地图片，用可重复的 `--file` 附加 PDF。`--file` 不接受其他
文档格式。附件相对路径以 `--workdir` 为准，未指定时以当前目录为准。

```bash
wuu exec --image screenshot.png "找出布局问题"
wuu exec --file report.pdf "总结报告中的发现"
```

`--image-original` 禁止缩放所提供的图片。模型能否使用附件，仍取决于服务商和模型的输入能力。

## 继续、分叉和评审

```bash
wuu exec resume --last "继续处理刚才的失败"
wuu exec resume --json THREAD_ID "继续这项任务"
wuu exec fork THREAD_ID "换一种方案尝试"
wuu exec review --uncommitted
wuu exec review --base main
wuu exec review --commit COMMIT_SHA
```

`resume --last` 选择工作区最近的可见会话。紧跟 `exec` 的 `--continue` 或 `-c` 是它的
快捷方式；该位置的 `--resume` 或 `-r` 是 `resume` 的快捷方式，不带其他参数时列出
可用会话。`fork` 从来源历史创建独立对话。

恢复或分叉时，显式指定的 `--provider`、`--model`、`--effort`、`--variant` 会在执行前
应用到目标会话；不指定则保留已保存的模型选择。恢复会保存对该会话的修改，分叉只修改新
会话，两者都不会修改工作区默认值或配置文件。绑定命名 Agent 的会话仍遵守该 Agent 的
模型选择限制。

`review` 为指定范围生成评审任务，再用普通工具检查代码，并不是另一个静态分析器。
不应修改文件时，选择 `--permission-mode read_only`。

## 控制运行

| 选项 | 作用 |
| --- | --- |
| `--provider`、`--model`、`--effort`、`--variant` | 选择模型路由和受支持的推理设置 |
| `--profile` | 选择 Agent 配置档 |
| `--permission-mode` | 使用 `standard`、`read_only` 或 `unconfined` |
| `--workdir` | 设置工作区目录 |
| `--config` | 显式信任一个配置文件 |
| `--ignore-user-config` | 显式信任项目配置，跳过用户配置 |
| `--env KEY=VALUE` | 设置本次运行的环境变量，可重复 |
| `--no-tools` | 禁用本地工具 |
| `--max-turns N` | 设置模型与工具循环上限；零使用配置默认值 |
| `--timeout 20m` | 设置包含 stdin 读取时间的截止时间；零不设置 CLI 截止时间 |
| `--ephemeral` | 创建内存会话，不保存为可恢复会话 |
| `--json` | 输出 JSONL 事件流 |
| `--output-last-message FILE` | 执行成功后把最终回答写入文件 |
| `--output-schema FILE` | 按 JSON Schema 验证最终回答 |
| `--input-json` | 从 stdin 读取包含任务和选项的一个 JSON 对象 |

普通启动使用用户配置和允许的项目覆盖项。两个显式信任选项会改变这一边界，自动化使用前
应先检查配置内容。分层规则见[配置参考](../reference/configuration.md)，命令限制见
[权限模式](../reference/permissions.md)。`exec` 不提供人机交互：用户提问服务立即返回
`service_unavailable`，外部执行引擎的交互式审批会被拒绝。非阻塞提问也遵循这一规则，
因为没有客户端接收问题。插件可以自行处理服务错误，它本身不决定运行退出码。需要回答
问题的客户端应使用 [app-server 协议](../../en/integrations/app-server-protocol.md)（英文）。

## 超时与取消

`--timeout` 在读取 stdin 前开始计时，执行阶段沿用同一个截止时间。仅在 `--input-json`
中提供的 `timeout` 要等输入解码后才生效；生产者可能一直不关闭 stdin 时，应使用 CLI
选项。即使有位置提示词，也会读取管道 stdin；不打算提供输入时可使用 `</dev/null`。
SIGINT 和 SIGTERM 会取消输入读取或当前执行，返回退出码 `5`；达到截止时间返回 `4`。

取消时会尝试结束当前 Run，并在有限时间内清理资源，因此进程可能在截止时间后稍晚退出。
应持续读取 stdout 和 stderr，直到进程退出。stdout 无法继续写入时，取消不会无限等待它，
最终 JSONL 结果仅尽力输出，可能缺失或不完整。输出管道断开会取消执行并返回 `6`。
SIGKILL 无法清理资源或输出结果。

## 接收输出

文本模式下，执行成功后 stdout 输出最终回答，进度、会话标识和诊断写入 stderr。
JSONL 模式下，stdout 每行是一个事件对象。应结合最终 `result` 和进程退出码判断结果，
不要在第一个 `turn_completed` 后停止读取：一次运行可能包含自动续接或结构化输出修正回合。

待发送事件有容量上限。消费者严重落后时，订阅会以明确的溢出错误结束（退出码 `6`），
而不是静默丢失事件或无限积压。无人值守的管道应始终设置超时：仍然连接但停止读取的
消费者，并不等于已经断开的管道。

```bash
wuu exec --json --timeout 20m "评审当前改动" > events.jsonl
wuu exec --output-last-message report.md "总结这个仓库"
wuu exec --json --output-schema schema.json "返回指定格式的报告"
```

Schema 路径以工作区为准，而最终回答的输出路径以启动进程的当前目录为准；已有输出文件会
被覆盖。Schema 验证成功时，JSONL 结果包含 `structured_result`。字段和失败处理见
[JSONL 事件参考](../../en/automation/jsonl-events.md)（英文）。

## 机器输入

```bash
wuu exec --input-json <<'JSON'
{
  "prompt": "调查这次失败",
  "stdin": "panic: example failure",
  "workdir": "/path/to/repo",
  "permission_mode": "read_only",
  "json": true,
  "timeout": "10m"
}
JSON
```

对象接受 `prompt`、`stdin`、`files`、`images`、`file_attachments`、`image_attachments`、
`workdir`、`provider`、`model`、`effort`、`variant`、`permission_mode`、`config`、
`profile`、`ignore_user_config`、`env`、`max_turns`、`no_tools`、`json`、`ephemeral`、
`timeout`、`output_last_message` 和 `output_schema`。路径数组遵循附件选项的规则；
结构化附件数组使用 [app-server 格式](../../en/integrations/app-server-protocol.md)（英文）。
`env` 是 `KEY=VALUE` 字符串数组。未知字段和多个 JSON 值会被拒绝，`--input-json` 不能
同时带位置提示词。

建议每个选项只在一处提供。非空 CLI 选择优先，附件和环境变量数组则会合并。CLI 中的
`true` 和非零值优先于对应输入值，因此显式传入 `false` 或零不能可靠地覆盖 JSON 输入。
`review` 子命令不接受 `--input-json`。

## 退出码

| 退出码 | 含义 |
| --- | --- |
| `0` | 成功完成 |
| `1` | 运行失败，没有更具体的分类 |
| `2` | 参数、配置或输入无效 |
| `3` | 权限拒绝 |
| `4` | 超时 |
| `5` | 中断或取消 |
| `6` | 协议或输出流错误 |
| `7` | 服务商、模型、认证或网络失败 |
| `8` | 未恢复的本地或工具失败 |
| `9` | 目标会话已有执行正在运行 |

参数解析失败或读取 stdin 时取消，可能发生在写入任何 JSONL 事件之前。缺少 `result`、结果失败或退出码非零都应视为
失败，不要根据部分回答推断成功。已保存的会话和执行记录可用
[`wuu session` 与 `wuu runs`](../reference/cli-commands.md) 查看。
