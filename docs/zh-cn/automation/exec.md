# 用 `wuu exec` 做自动化

`wuu exec` 用于从脚本、CI 或其他 Agent 运行任务。它与桌面端共用会话、工具和权限，
返回文本或 JSONL，不打开界面。使用前先[配置模型](../getting-started/model-services.md)。

## 运行一个任务

```bash
wuu exec "修复失败的测试并验证结果"
```

默认情况下，stdout 只包含最终回答，运行信息和诊断写入 stderr，因此可以安全地捕获
结果：

```bash
result=$(wuu exec "总结这个仓库当前未提交的改动")
printf '%s\n' "$result"
```

也可以从 stdin 提供任务，或把 stdin 作为补充上下文：

```bash
wuu exec - < task.md
wuu exec "根据这段日志修复问题" < error.log
```

空输入会在创建回合前失败。

## 指定工作区和附件

```bash
wuu exec --workdir /path/to/repo "运行测试并修复失败项"
wuu exec --file report.pdf "阅读报告并更新实现"
wuu exec --image screenshot.png "定位界面问题"
```

`--file` 和 `--image` 可以重复使用。`--file` 当前只接受 PDF；相对路径以 `--workdir`
为准，没有设置时以当前目录为准。

## 继续或分叉会话

```bash
wuu exec --continue "继续处理刚才的失败"
wuu exec resume --last "继续最近的会话"
wuu exec resume <thread-id> "继续这个会话"
wuu exec fork <thread-id> "换一种方案尝试"
```

`--continue` 是顶层快捷方式；指定会话既可以使用 `resume <thread-id>`，也可以使用
`-r <thread-id>` 或 `--resume <thread-id>`。`fork` 会保留原会话并创建一个新的分支会话。

## 输出 JSONL

需要实时进度或机器解析时使用 `--json`：

```bash
wuu exec --json "评审当前改动" > events.jsonl
```

JSONL 模式保证：

- stdout 每行都是一个 JSON 对象；
- 每个事件都有 `type`；
- 诊断和调试日志仍写入 stderr；
- 最后一行是 `result` 事件。

完整事件字段见 [JSONL events](../../en/automation/jsonl-events.md)（英文）。脚本应优先
使用事件类型和退出码，不要解析自然语言错误文本。

## 常用控制

```bash
wuu exec --timeout 20m "完成任务并验证"
wuu exec --max-turns 8 "只调查并给出结论"
wuu exec --permission-mode read_only "评审改动，不要写文件"
wuu exec --output-last-message result.md "生成最终报告"
wuu exec --ephemeral "临时分析，不保存会话"
```

模型、provider、effort、variant、profile 和配置文件也可以按次覆盖。查看当前版本的完整
参数：

```bash
wuu --help
```

标准和只读模式下，Agent 命令还受到文件系统写入沙箱约束；沙箱后端不可用时会拒绝运行，
不会悄悄放开限制。这不隔离命令继承的环境变量或网络访问。无人值守检查优先使用只读
模式，允许修改前先检查任务配置。完整边界见[权限模式](../reference/permissions.md)和
[安全说明](../reference/security-model.md)。

## 退出码

| 退出码 | 含义 |
| --- | --- |
| `0` | 成功完成 |
| `1` | Agent 回合失败 |
| `2` | 参数、配置或输入无效 |
| `3` | 工作区边界或工具策略拒绝 |
| `4` | 超时 |
| `5` | 被中断 |
| `6` | app-server 协议错误 |
| `7` | provider 或模型错误 |
| `8` | 工具执行失败且 Agent 未恢复 |
| `9` | 目标会话已有回合在运行 |

## 选择哪种自动化方式

- 本机按时间运行：使用桌面 [Automations](scheduled-tasks.md)；
- CI、系统 cron 或其他 Agent 调用：使用 `wuu exec`；
- 构建新的桌面壳或编辑器插件：使用
  [app-server protocol](../../en/integrations/app-server-protocol.md)（英文）。

更完整的参数和输入对象参考见 [英文 `wuu exec` 文档](../../en/automation/exec.md)。
