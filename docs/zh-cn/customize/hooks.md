# Hook

Hook 在 Wuu 支持的生命周期事件上运行命令或模型检查，可以拒绝工具调用、调整参数、补充检查结果或记录事件。它会执行受信任代码或把数据发送给模型，不是操作系统沙箱。

## 配置 Hook

把 `hooks` 合并到用户配置中，默认位置为 `~/.wuu/config.json` 或 `$WUU_HOME/config.json`，然后重启需要使用它的运行时。已启用插件包也可以贡献 Hook。技能 frontmatter 中的 `hooks` 只作兼容解析，不会注册 Hook。

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "bash",
        "type": "command",
        "command": "python3 /absolute/path/check-shell.py",
        "timeout": 10
      }
    ]
  }
}
```

这是配置片段，不应覆盖已有的模型服务和 agent 设置。项目配置遵循[正常加载与信任规则](../reference/configuration.md)；自动化显式指定的配置中，只应包含你确实打算执行的 Hook。

| 字段 | 含义 |
|---|---|
| `matcher` | 不区分大小写的工具名精确匹配；空值或 `*` 匹配全部 |
| `type` | 默认 `command`，也可以是 `prompt` |
| `command` | 命令 Hook 的 shell 命令 |
| `prompt` | 模型检查模板，`$ARGUMENTS` 替换为事件 JSON |
| `model` | 提示 Hook 的可选模型名；否则使用已配置 Hook 客户端的默认值 |
| `timeout` | 单个 Hook 超时秒数；省略或非正数时为 30 |

Matcher 不支持 glob 表达式。没有工具名的事件需要空 matcher 或 `*`。匹配的 Hook 按配置顺序运行，遇到第一个错误就停止；多个成功 Hook 设置同一输出字段时，后者覆盖前者。

## 事件

这些事件对应 Wuu 运行时路径，不代表能以同样方式拦截外部引擎内部操作。

| 事件 | 用途与效果 |
|---|---|
| `PreToolUse` | 执行前触发，可以阻止或替换工具参数 |
| `PermissionRequest` | 工具授权时触发，可以阻止，但不能授予更大权限 |
| `PostToolUse` | 成功后触发，可以补充模型上下文，不能撤销操作 |
| `PostToolUseFailure` | 失败后触发，Hook 错误不替换工具原错误 |
| `UserPromptSubmit` | 提示开始轮次前触发，可以阻止 |
| `PreCompact` | 压缩前触发，可以阻止压缩 |
| `PostCompact` | 压缩返回后触发，可以拒绝采用结果 |
| `SubagentStart`、`SubagentStop` | 子代理轮次前后触发，错误使对应操作失败 |
| `SessionStart`、`SessionEnd` | 会话绑定与清理时触发，错误传回对应操作 |
| `Stop` | 轮次结束时触发，可以将轮次标记为失败 |
| `FileChanged` | Wuu 文件工具成功写入或编辑后触发，输出不改变结果 |

`FileChanged` 不是文件系统监听器。手动编辑、shell 命令和其他程序不一定触发它。工具执行后的 Hook 无法让已经发生的副作用变成从未发生。

## 命令输入与输出

Wuu 通过 shell 启动命令，继承进程环境，并通过 stdin 传入一个 JSON 对象。请读取其中的 `cwd`，不要假设 Hook 进程正好在工作区启动。

```json
{
  "hook_event_name": "PreToolUse",
  "session_id": "example-session",
  "cwd": "/path/to/project",
  "tool_name": "bash",
  "tool_input": { "command": "go test ./..." }
}
```

事件专用字段包括成功结果文本 `tool_response`、失败详情 `error`，以及 `prompt`、`file_path`、`compact_reason` 和 `agent_id`。并非所有路径都会提供非空 `session_id`。富媒体工具结果会转为文本投影，不会把完整内部对象交给 Hook。

在 stdout 输出一个 JSON 对象，诊断信息写入 stderr。所有输出字段都可选：

| 字段 | 效果 |
|---|---|
| `decision: "block"` 或 `continue: false` | 在支持阻止的事件中拒绝操作 |
| `reason` | 解释决定 |
| `updated_input` | 在 `PreToolUse` 中替换完整参数对象 |
| `additional_context` | 在成功的 `PostToolUse` 后补充上下文 |

下面的脚本演示如何明确阻止一种命令文本：

```python
import json
import sys

event = json.load(sys.stdin)
command = (event.get("tool_input") or {}).get("command", "")
if "git push" in command:
    json.dump({"decision": "block", "reason": "Publish changes manually."}, sys.stdout)
else:
    json.dump({}, sys.stdout)
```

这个子串示例用于说明协议，不是完整 shell 安全策略。相同效果的 shell 命令可以有不同文本。

成功解码的 JSON 对象决定是否阻止；没有可解码对象时，退出码 0 表示继续，2 表示阻止，其他非零退出码表示执行失败。需要可靠阻止时，应输出明确的阻止决定，或保持 stdout 为空并退出 2。不要输出 `{}` 后期待退出码 2 覆盖它。无效或混杂的 stdout 不会被当作结构化输出。

## 提示 Hook

提示 Hook 请求模型返回 `ok` 布尔值和原因：

```json
{
  "type": "prompt",
  "matcher": "bash",
  "prompt": "Check whether this action fits the requested review-only task: $ARGUMENTS",
  "timeout": 20
}
```

把该条目放在所需事件下。`ok: false` 会阻止操作。当前实现中，没有模型客户端、模型请求失败或回复无法解析时都会放行，所以提示 Hook 不能作为唯一安全边界。它还会增加模型请求、延迟和费用，并把事件数据发送给所选服务。

## 排查 Hook

先使用无害事件，分别验证输入、matcher 和输出。stdout 不要混入日志。检查命令路径、依赖、超时，以及脚本是否在等待交互输入。修改文件配置后重启运行时。

Hook 输入可能包含源码、提示、路径和工具结果。调试结束后移除临时日志，不要把秘密放进命令字符串或共享配置。agent 使用 Read only 模式，不会自动让 Hook 也变成只读；详见[安全模型](../reference/security-model.md)。
