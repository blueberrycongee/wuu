# 配置

Wuu 把模型连接和执行选择放在用户配置中，项目可以补充行为设置。CLI 首次使用先运行 `wuu init`，桌面则通过首次引导配置，之后只修改需要的选项。未知字段会报错，不会悄悄忽略拼写错误。

## 用户配置与项目层

正常启动选择 `~/.wuu/config.json`；设置 `WUU_HOME` 时使用 `$WUU_HOME/config.json`。旧路径 `~/.config/wuu/config.json` 用于迁移和后备读取，不是在当前用户文件之后继续叠加的一层。

随后按以下顺序合并项目文件，在允许覆盖的字段上，后者优先：

1. `.wuu.json`，不存在时使用 `wuu.json`。
2. 共享设置 `.wuu/settings.json`。
3. 本地设置 `.wuu/settings.local.json`。

对象递归合并，数组和标量直接替换。本地设置文件应排除在版本控制之外。用户配置缺失时，不会悄悄把项目文件当作可信基础；CLI 会要求先初始化。

## 受保护的用户设置

正常启动会从所有项目层移除以下字段，包括 `settings.local.json`，并报告忽略了哪些字段：

| 字段 | 归用户所有的原因 |
|---|---|
| `default_provider`、`providers` | 选择模型服务、端点、凭据和连接选项 |
| `instructions`、旧字段 `memory` | 控制指令发现，包括用户路径 |
| `agent.model_roles`、`agent.model_aliases` | 路由模型工作 |
| `agent.permission_mode` | 设置本地执行权限 |

改变 JSON 字段大小写不能绕过限制。其他允许的项目字段仍可能影响提示、工具、Hook 和服务，因此这种过滤不代表陌生仓库可以安全执行。

## 模型与引擎配置

Provider 条目为一条模型连接命名。例如，下面的用户配置片段使用 OpenAI 兼容 Chat 端点：

```json
{
  "default_provider": "work",
  "providers": {
    "work": {
      "type": "openai-compatible",
      "base_url": "https://api.example.com/v1",
      "api_key_env": "WORK_MODEL_API_KEY",
      "model": "your-model-id"
    }
  },
  "agent": { "permission_mode": "standard" }
}
```

请替换为服务实际支持的端点和模型，并把对应环境变量传给运行 Wuu 的进程。订阅登录、服务类型和桌面配置见[模型服务](../getting-started/model-services.md)。

[外部引擎](../getting-started/external-engines.md)是独立程序，不是 provider 类型。机器本地的 `engines` 配置控制检测、可执行文件选择和默认引擎。在桌面中修改某个会话的模型，不一定改变工作区默认值；需要影响未来会话时，应从设置中修改。

## 指令与插件设置

团队共享规则放在 `AGENTS.md` 中。核心 `instructions` 对象控制文件名、项目根标记、用户目录和可选的旧指令发现。顶层旧字段 `memory` 用于迁移指令发现设置，不是 Memory 插件设置接口。

[Memory](../customize/memory.md)、[Dream](../customize/dream.md) 和其他插件维护各自的产品设置与存储。核心的持久会话工作笔记也与插件记忆分开。请使用插件设置页，不要把过时的 `memory.dream` 或委派开关复制进核心配置。

## Worker 容量

`agent.max_parallel` 设置通用匿名 worker 的执行容量，默认 `5`；`0` 表示采用默认值，负数无效。

```json
{ "agent": { "max_parallel": 5 } }
```

排队或等待子任务的 worker 不占用通常的执行槽位。这是执行容量设置，不是要求每个任务都委派，也不是所有后台进程的总量上限。[子代理行为](../desktop/subagents.md)由已启用的委派插件负责。

## 显式自动化配置

`wuu exec --config /path/to/config.json` 只加载一个完整文件，不叠加正常项目层。`wuu exec --ignore-user-config` 则把项目 `.wuu.json` 或 `wuu.json` 当作可信基础，再应用两个项目设置层。

两种选择都会明确接受这些文件里的连接、凭据引用、指令路径、Hook 和 MCP 定义，只应使用已经检查的配置。将 `HOME` 留空不会隐式授予同样的信任。

## 更换用户状态目录

启动 Wuu 前设置 `WUU_HOME`，可以选择不同的用户状态根目录。它影响配置、认证状态、会话、记忆、插件和日志，而不只是配置文件。例如，`WUU_HOME=/data/wuu` 会选择 `/data/wuu/config.json`。请保护该目录，不要把它整体作为诊断材料分享。

Windows 命令执行需要 Git Bash，可通过 `WUU_GIT_BASH_PATH` 指定可执行文件。命令可用与沙箱支持是两个独立要求，详见[权限](permissions.md)和[命令系统](agent-command-system.md)。
