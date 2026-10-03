# 配置

Wuu 把模型连接和执行选择放在用户配置中，项目可以补充行为设置。CLI 首次使用先运行 `wuu init`，桌面则通过首次引导配置，之后只修改需要的选项。未知字段会报错，不会悄悄忽略拼写错误。

内置 Agent 可以在标准或无限制模式下用普通文件工具修改配置；只读模式允许查看，但拒绝修改。Wuu 主目录属于正常文件访问范围，无需专门的配置工具。添加 Provider 或修改默认模型时，编辑下文所述的用户配置文件；项目层的字段限制仍然适用。有效的模型改动沿用桌面已有的自动刷新机制，其他设置仍遵循各自说明的重启要求。

Provider 凭据优先使用环境变量引用。工具输出可能遮蔽密钥值，应局部编辑，不要用遮蔽后的输出覆盖整个配置。专用文件工具仍禁止访问 Wuu 登录凭据存储，详见[权限](permissions.md)。

桌面运行期间，外部编辑导致配置无效时，Wuu 会报告错误并保留最近一次有效的模型列表用于显示；该列表不会绕过配置修改或执行所需的校验。修正文件后会自动恢复刷新，恢复原来的文件内容也会清除错误。内容未变化的无效文件不会反复产生刷新错误。

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
| `agent.model_roles`、`agent.model_aliases`、`agent.project_models` | 路由模型工作 |
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

## 工具加载

`agent.tool_loading` 默认为 `auto`。已支持的官方接口使用原生加载：
OpenAI Responses 上支持该功能的 GPT-5.4 及更新模型（不包括
[GPT-5.4 nano](https://developers.openai.com/api/docs/models/gpt-5.4-nano)）、Anthropic 上
[支持工具搜索的 Claude 模型](https://platform.claude.com/docs/en/agents-and-tools/tool-use/tool-search-tool#model-compatibility)
（包括 Haiku 4.5，不包括 Opus 4.1 及更早模型），以及 `https://api.moonshot.ai/v1` 或
`https://api.moonshot.cn/v1` 上使用 Chat Completions 的 `kimi-k3`。
Kimi 使用独立的消息级工具声明协议；这条规则不启用其 Responses 或 Anthropic 兼容接口。

原生加载通过 `tool_search` 按需把延迟工具的 schema 加入模型上下文，启用的内置浏览器
也按需加载。其他接口使用 `client`：通过普通 `tool_search` 结果按需加载 schema，
不依赖专有协议。初始目录预览最多 8 KiB，未显示的工具仍可搜索。加载后的 schema
追加在直接工具之后，因此发现操作可能影响提供方缓存复用。支持普通工具调用或
兼容 URL，并不等于支持原生延迟加载。

将 `agent.tool_loading` 设为 `client` 可始终使用普通发现，`flat` 明确声明全部工具，
`native` 可在兼容端点上显式启用已实现的协议。不支持的原生接口改用客户端发现
并提示加载方式变化；这是配置阶段的选择，不是 API 拒绝后的重试。模型的
`providers.<name>.models.<model>.options.native_tool_search` 可以显式允许兼容端点
在 auto 模式下使用原生加载，或用 `false` 关闭原生加载而保留客户端发现。只有端点确实实现了对应模型的原生协议
时才启用；仅接受未知字段并不够。

## Project Agent 模型选择

在启用 Project Agent 的构建中，主 Agent 使用会话模型。可以在设置 → 运行时中
分别选择 Side 和 Worker 的默认模型，也可以修改用户配置：

```json
{ "agent": { "project_models": {
  "side": { "provider": "anthropic", "model": "your-side-model" },
  "worker": { "provider": "openai", "model": "your-worker-model" }
} } }
```

使用已配置的服务名称和模型 ID。省略角色或留空时继承主 Agent 模型。服务支持时，
每个选择也接受 `effort` 和 `variant`。默认值只影响新建成员，已有会话保留保存的
选择；创建时明确指定的 `model_alias` 优先于角色默认值。参见
[app-server 协议](../automation/app-server.md)。

## 指令与插件设置

团队共享规则放在 `AGENTS.md` 中。核心 `instructions` 对象控制文件名、项目根标记、用户目录和可选的旧指令发现。顶层旧字段 `memory` 用于迁移指令发现设置，不是 Memory 插件设置接口。

[Memory](../customize/memory.md)、[Dream](../customize/dream.md) 和其他插件维护各自的产品设置与存储。核心的持久会话工作笔记也与插件记忆分开。请使用插件设置页，不要把过时的 `memory.dream` 或委派开关复制进核心配置。

## Worker 容量

`agent.max_parallel` 设置通用匿名 worker 的执行容量，默认 `5`；`0` 表示采用默认值，负数无效。

```json
{ "agent": { "max_parallel": 5, "project_max_parallel": 0 } }
```

排队或等待子任务的 worker 不占用通常的执行槽位。这是执行容量设置，不是要求每个任务都委派，也不是所有后台进程的总量上限。[子代理行为](../desktop/subagents.md)由已启用的委派插件负责。


`agent.project_max_parallel` 单独限制每个 Project Agent lead 的新受管 worker 准入数量。默认值 `0` 继承解析后的 `agent.max_parallel`，负数无效。Lead 与持久 side 会话不占用此 worker 容量；未明确角色的旧受管会话按 worker 计算。

限制依据共享持久会话成员关系和操作系统执行租约。启动前预留及短暂的元数据修改会保守地占用容量。停止 worker 后，只有执行清理结束才释放容量。降低上限或接管已经运行的会话不会取消当前工作；后续准入等待占用降到上限以下。

每次新准入都读取有效配置。共享会话存储的服务器须使用一致策略才能保证共同上限；独立存储或机器之间不共享全局容量。持久排队的项目输入通过合并且有上限的退避重试，在远端执行结束或进程退出后继续运行；等待时不发模型请求。跨服务器的 worker 顺序为尽力保证，并非全局 FIFO。手动启动在写入用户输入前返回容量已满错误。

## 显式自动化配置

`wuu exec --config /path/to/config.json` 只加载一个完整文件，不叠加正常项目层。`wuu exec --ignore-user-config` 则把项目 `.wuu.json` 或 `wuu.json` 当作可信基础，再应用两个项目设置层。

两种选择都会明确接受这些文件里的连接、凭据引用、指令路径、Hook 和 MCP 定义，只应使用已经检查的配置。将 `HOME` 留空不会隐式授予同样的信任。

## 更换用户状态目录

启动 Wuu 前设置 `WUU_HOME`，可以选择不同的用户状态根目录。它影响配置、认证状态、会话、记忆、插件和日志，而不只是配置文件。例如，`WUU_HOME=/data/wuu` 会选择 `/data/wuu/config.json`。请保护该目录，不要把它整体作为诊断材料分享。

Windows 命令执行需要 Git Bash，可通过 `WUU_GIT_BASH_PATH` 指定可执行文件。命令可用与沙箱支持是两个独立要求，详见[权限](permissions.md)和[命令系统](agent-command-system.md)。
