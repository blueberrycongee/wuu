# Skills

Skill 是一份可复用的任务说明，包含步骤、工具使用建议和交付要求，适合发布检查、文档
编写、代码评审等重复工作。使用后仍需检查结果，加载 Skill 不保证任务一定正确完成。

## 查看和使用 Skill

在桌面输入框中输入 `/skills` 打开 Skills 目录。你可以：

- 搜索当前 runtime 发现的 Skills；
- 区分内置和个人 Skill；
- 预览 Skill 原文；
- 选择**立即试用**，把对应工作流带回当前对话。

已发现的 Skill 也会出现在 `/` 菜单中。你可以直接输入名称和参数，例如：

```text
/browser 检查当前页面
```

Agent 也会看到带名称和简介的可用 Skill 目录，并在任务匹配时通过 `load_skill` 按需
加载全文。`disable-model-invocation: true` 可以阻止模型自行选择某个 Skill，但不影响
`user-invocable: true` 时由用户按名称调用；没有 `description` 的 Skill 也不会出现在
模型目录中。

Skill 是否可用取决于当前工作区和当前工具表面。声明了当前会话不具备的工具时，Wuu
可能隐藏该 Skill。刷新目录可以重新执行发现。

## Skill 的信任边界

Skill 内容会进入 Agent 上下文，可能影响它选择工具和处理文件。使用来自仓库或第三方
的 Skill 前先阅读原文。

当前 `load_skill` 路径只加载说明和资源，不会在加载时执行以 `!` 开头的行内代码或围栏
代码块。Skill 仍然可以在正文中要求 Agent 随后调用命令、网络或文件工具；
这些实际工具调用受当前权限模式和工作区边界约束。

## 编写或安装

项目 Skill 适合随仓库共享，个人 Skill 适合跨项目复用。目录、文件格式、覆盖顺序和
兼容字段见[编写与安装 Skill](skill-authoring.md)。

## 编写后检查

CLI 提供与实际发现规则一致的检查：

```bash
wuu skills lint path/to/skill
wuu skills lint --json path/to/skills-root
```

检查工具验证文件结构和元数据。工作流内容和任务结果仍需人工检查。

## 与项目指令的区别

- `AGENTS.md` 等项目指令会持续影响该工作区中的任务；
- Skill 代表一个按需使用的工作流；
- [记忆](memory.md)保存跨会话、由用户控制的长期信息。
