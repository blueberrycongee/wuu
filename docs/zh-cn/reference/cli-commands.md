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

## 评估内置任务

```bash
wuu eval --list
wuu eval --provider NAME --task test_failure_fix --artifacts-dir ./eval-results --json
wuu eval --provider NAME --environment inherited --task mcp_live_discovery
wuu eval --replay-trace /path/to/eval-trace.jsonl --json
```

每个任务都在独立的 Wuu 进程中运行。默认的 `--environment isolated` 为每个任务
创建新的用户目录和 Wuu 状态目录，保留选定的提供方、配置的模型角色与别名、
agent 设置和 PTC 设置。隔离前结合模型目录与所选模型的默认设置，按正常的
配置、环境变量和已保存凭据优先级解析 API 密钥与 Bearer 令牌，再通过内存
传给工作进程；不会将这些凭据复制到配置文件或评估报告中。

个人指令、技能、插件、记忆、钩子、MCP 服务器、引擎设置和执行环境设置不会
自动导入。内置插件与技能使用正常默认值；任务目录中的场景负责声明自己的
文件与 MCP 依赖。后台模型目录更新会关闭。报告和可重放轨迹记录环境模式、
agent/PTC 设置、启用插件的指纹、指令与技能的指纹，以及配置的 MCP 名称。

需要评估现有配置、扩展或订阅凭据时，显式选择 `--environment inherited`。
此模式会读取且可能修改正常的 Wuu 状态，结果会标记为 inherited。依赖用户目录
凭据存储的订阅认证需要此模式；隔离模式会给出明确提示，不会复制刷新令牌。
同一提供方的所选模型若需要不同的凭据组合，也必须使用 inherited 模式。
明确请求实时认证的 `--live-codex-oauth` 流程自动选择 inherited 模式；显式指定
不兼容的环境模式时会报错。

`--artifacts-dir` 指定保存新运行目录的父目录；省略时使用系统临时目录。
输出中会显示实际目录。每次运行保留 `report.json`、各任务的 `eval-trace.jsonl`
与 `worker.log`，隔离模式还保留各任务的 Wuu 运行时状态。
`--output FILE` 另外将 JSON 报告写入指定文件。任务工作区与临时用户目录在运行
结束后删除；加上 `--keep-workdirs` 可保留。分享前应检查证据，运行日志与工具
产物可能包含任务内容。系统可能清理临时目录，需要长期保留时请指定持久目录。

这里隔离的是 Wuu 用户状态，不是整台机器或网络。已安装工具、`PATH`、其他
继承的环境变量和网络访问仍会影响运行。比较结果时应保持机器与工具链、任务
列表、提供方与模型设置、超时和环境模式一致。重放轨迹不会调用模型或执行工具。

## 集成与诊断

[`wuu app-server`](../automation/app-server.md) 提供客户端使用的持久子进程协议。[`wuu remote` 与 `wuu relay`](../automation/remote.md)运行远程控制组件。报告问题时，应记录版本、命令、工作区和最小脱敏错误，见[故障排查](../help/troubleshooting.md)。
