# 编写与安装技能

技能通常是一个目录，包含 `SKILL.md`，以及可选的脚本、模板或参考资料。描述帮助模型判断何时需要加载完整正文。

## 编写最小技能

创建 `.wuu/skills/release-check/SKILL.md`：

```markdown
---
name: release-check
description: 发布前检查版本、构建和发布说明。
argument-hint: "[version]"
---

# 发布检查

阅读项目的发布说明和版本文件。
确认目标版本是 ${ARGUMENTS}。
运行要求的发布检查，报告结果。
不要创建标签或发布产物。
```

目录名就是发现后的技能名。为了便于跨工具复用，使用 1–64 个小写字母、数字和单个连字符，不要以连字符开头或结尾。描述应说明适用时机，正文应交代输入、操作和需要提供的证据。

## 发现与覆盖顺序

项目发现沿仓库根目录到当前工作目录的路径逐层进行，较近的目录覆盖祖先目录。同一层按以下顺序读取，同名时后面的条目优先：

1. `.claude/skills/`
2. `.agents/skills/`
3. `.opencode/skill/`
4. `.opencode/skills/`
5. `.wuu/skills/`

用户目录也采用后者优先：`~/.codex/skills/`、`~/.claude/skills/`、`~/.agents/skills/`、`~/.config/opencode/skills/`，最后是 Wuu 主目录下的 `skills/`。最后一个路径默认是 `~/.wuu/skills/`，设置 `WUU_HOME` 后随之改变。

项目技能覆盖用户技能，磁盘上的定义覆盖同名内置技能。已启用插件包也可以提供技能；对应作用域下普通发现的技能优先于这些包内条目。

每个根目录都接受 `<name>/SKILL.md` 和平铺的 `<name>.md`。目录形式更便于携带资源共享。Wuu 还会沿项目路径适配 `.claude/commands/*.md` 和 `.wuu/commands/*.md`，以及 Wuu 用户主目录下的命令。原生技能优先于同名命令模板。

## 元数据

| 字段 | 当前行为 |
|---|---|
| `name` | 声明名称；目录形式以目录名为准 |
| `description` | 供模型选择的摘要；为空时不进入模型目录 |
| `argument-hint` | 调用时显示的参数提示 |
| `user-invocable` | 是否提供用户直接调用，默认 `true` |
| `disable-model-invocation` | 为 `true` 时不供模型自动选择 |
| `allowed-tools` | 声明所需工具，供兼容性过滤使用，不授予权限 |
| `when-to-use`、`trigger` | 补充适用时机 |
| `required-context`、`examples`、`verification-checklist` | 补充工作流程信息 |
| `progressive-disclosure`、`version` | 兼容性元数据 |

解析器为兼容而接受 `model`、`context`、`agent`、`effort`、`paths` 和 `hooks`，但加载技能不会因此切换模型、分叉上下文、创建 agent、改变推理力度、按路径激活或注册 Hook。这些字段承诺了不支持的行为时，Lint 会发出警告。`shell` 也能解析，但不会开启内联执行。

## 参数与资源

正文支持 `${ARGUMENTS}`、`${CLAUDE_SKILL_DIR}` 和 `${CLAUDE_SESSION_ID}`，分别替换为调用参数、技能目录和当前会话 ID。加载结果包含基础目录和部分资源文件名；正文中的相对资源路径应以该目录为基准。

正常加载时，内联 shell 表达式和代码块仍是文本。流程需要动态信息时，应要求 agent 使用可用工具获取，让通常的权限检查继续生效。

## 安装与检查

下载技能后，先检查 `SKILL.md` 和配套资源，再把整个目录复制到发现路径中。重点检查命令执行、网络访问、凭据处理和项目外路径。本地技能不需要构建或包清单。

```bash
wuu skills lint .wuu/skills/release-check
wuu skills lint --json .wuu/skills
```

Lint 接受单个技能目录、包含技能的根目录或平铺 Markdown 文件。错误表示发现流程无法使用文件或其元数据，命令会以非零状态退出；警告表示可以加载，但行为可能与预期不同。

刷新桌面技能目录，预览实际加载的源码，再尝试低风险任务。技能未出现时检查 frontmatter 和所需工具；模型从不选择它时检查描述和调用开关；版本不对时检查同名覆盖。结构检查不能证明流程安全或有效。
