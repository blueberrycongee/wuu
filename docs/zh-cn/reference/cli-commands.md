# CLI 命令

CLI 可用于初始化配置、执行任务、检查已保存会话和管理本地扩展。`wuu --help` 提供命令摘要。以下示例中的大写 ID 和路径需要替换为实际值；除非某个命令另有说明，选项应放在位置参数之前。

## 初始化与查看构建

```bash
wuu init
wuu version
wuu version --long
wuu version --json
```

`init` 创建用户配置，已有文件时拒绝覆盖。只有确实需要替换时才使用 `wuu init --force`。开始任务前，先配置可用的模型连接。

`wuu models --provider NAME --json` 对 `openai-codex` provider 执行在线模型查询。它不是所有已配置服务的通用离线列表，当前会拒绝其他 provider 类型，并需要对应的连接和认证。

## 执行、继续与审查

```bash
wuu exec --workdir /path/to/project "运行相关测试并解释失败原因"
wuu exec --continue "继续调查"
wuu exec resume THREAD_ID "继续这个任务"
wuu exec fork THREAD_ID "尝试另一种方案"
wuu exec review --uncommitted --permission-mode read_only
wuu exec review --base main --permission-mode read_only
wuu exec review --commit COMMIT_SHA --permission-mode read_only
```

`wuu -c` 和 `wuu -r THREAD_ID` 分别是 exec 继续和恢复的快捷方式。Fork 从已保存上下文创建新会话，不要与桌面的 worktree 分叉混淆。审查仍使用所选权限模式，只需要调查时请选择 Read only。

标准输入、附件、JSONL 输出、schema、超时和退出码见 [`wuu exec`](../automation/exec.md)。`wuu run` 保留为兼容入口，新脚本应使用 `exec`。

## 检查会话

```bash
wuu session list --json
wuu session list --all-workdirs --include-archived
wuu session show --json THREAD_ID
wuu session show --last
wuu session trace --json THREAD_ID
wuu session search --workdir /path/to/project "关键词"
```

列表默认限定当前工作区。`show` 读取元数据和历史；`trace` 回放已保存轨迹，不重新执行工具，也不请求模型。搜索检查元数据和历史。脚本优先使用命令支持的 JSON 输出，不要解析展示文本。

## 归档、导出与删除

```bash
wuu session archive THREAD_ID
wuu session export --out conversation.jsonl THREAD_ID
wuu session export --json --out conversation.json THREAD_ID
wuu session delete THREAD_ID
```

归档从普通列表隐藏会话，不删除记录。导出默认以 JSONL 写入元数据头和历史记录；`--json` 则写入一个包含元数据与历史的对象。`--out` 会创建或覆盖输出文件，分享前应检查导出内容。

删除会移除已保存的会话数据及关联工作区产物。操作前确认 ID，并保留需要的成果。

## 检查执行记录

```bash
wuu runs --json
wuu runs read RUN_ID
```

这些命令读取持久化的执行 Run 清单，与桌面 Automation 插件的任务和运行列表不同。

## 技能与插件

```bash
wuu skills lint .wuu/skills
wuu plugin list
wuu plugin install ./my-plugin.zip
wuu plugin approve my-plugin
wuu plugin disable my-plugin
wuu plugin remove my-plugin
```

Lint 行为见[技能](../customize/skill-authoring.md)，本地安装和更新见[插件](../customize/plugins.md)。作者还可以使用 `create`、`dev`、`validate`、`build`、`test` 和 `pack`，详见[编写参考](../customize/plugin-authoring.md)。

## 集成与诊断

[`wuu app-server`](../automation/app-server.md) 提供客户端使用的持久子进程协议。[`wuu remote` 与 `wuu relay`](../automation/remote.md)运行远程控制组件。报告问题时，应记录版本、命令、工作区和最小脱敏错误，见[故障排查](../help/troubleshooting.md)。
