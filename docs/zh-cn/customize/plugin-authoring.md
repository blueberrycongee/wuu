# Wuu Plugin 开发参考

本文是 Wuu Plugin 的完整开发参考：包结构、Agent 协议、Desktop API、生命周期、
本地开发闭环和信任边界。它适合在你已经选定插件类型后查字段和接口，不是推荐的第一篇。

先按目标选择入口：

- [扩展 Wuu](index.md)：比较 Skill、MCP、Hook 和 Wuu Plugin；
- [Agent 插件快速上手](plugin-quickstart.md)：注册第一个模型可见工具；
- [Desktop 插件快速上手](desktop-plugin-quickstart.md)：给 Composer 添加控件；
- [Desktop UI 扩展地图](desktop-plugins.md)：选择 View、Slot、Presenter 或 Surface；
- [插件场景教程](plugin-recipes.md)：查看常见能力怎样组合。

用户侧的安装、信任和管理见[Wuu Plugin](plugins.md)。

第一次写 Agent 插件或 Desktop 插件时，先走对应的快速上手，再回来看本页。

Wuu 插件平台当前是本地优先的：没有市场、没有中心仓库。插件以目录或 zip 包的形式
在本地安装，开发者通常在自己的 GitHub 仓库中维护，用户 clone 或下载后在 Wuu 中安装。
分发能力只有在这种自然生态出现后才会建设，插件作者现在不需要为任何平台账号或审核
流程做准备。

## 插件能做什么

一个插件包可以同时包含三种贡献，也可以只包含其中一种：

| 贡献 | 运行位置 | 需要代码吗 |
| --- | --- | --- |
| 声明式主题 | Renderer（CSS Token） | 否 |
| 声明式设置 | Renderer + app-server | 否 |
| Agent 插件 | 独立 runtime 进程（Node 等） | 是 |
| 桌面插件 | Wuu Renderer（ESM 模块） | 是 |

插件可以形成覆盖整个产品的强视觉语言，通过公开 Token、UI Kit 和粗粒度语义边界实现，
不接管私有 DOM。窗口安全区、导航结构、Tab、滚动、溢出、键盘与恢复路径由宿主管理。
Surface 替换等高信任能力是复杂结构定制的 escape hatch，不是普通控件的默认入口。
整体设计与接口选择原则见[插件系统架构](plugin-system.md)。

## 包结构与 manifest

```text
my-plugin/
├── plugin.json
└── dist/
    ├── runtime.js     # Agent 插件入口（可选）
    └── desktop.js     # 桌面插件入口（可选）
```

`plugin.json` 是清单。Wuu 在安装、每次加载前都会重新读取并校验它。最小示例：

```json
{
  "schema_version": 1,
  "id": "my-plugin",
  "name": "My Plugin",
  "version": "1.0.0",
  "description": "What this plugin does",
  "icon": "plug",
  "runtime": {
    "protocol": "wuu-plugin-v1",
    "command": "node",
    "args": ["dist/runtime.js"]
  },
  "desktop": {
    "entry": "dist/desktop.js"
  }
}
```

为兼容已有插件，`schema_version` 和 `schemaVersion` 都可用，但一个 manifest 只能选择
其中一个，同时声明会校验失败。新插件应沿用脚手架生成的 `schema_version`。

常用字段：

- `id` 是全局唯一标识，决定安装目录名和所有注册的命名空间前缀；一旦发布不应更改。
- `version` 是语义化版本。同一来源身份的更新延续信任，Wuu 不按文件变化重新审批。
- 顶层 `icon` 是插件品牌图标，用于插件目录和详情，不自动进入宿主导航。优先填
  公共语义图标名，这样标记会跟随当前主题。自绘资源也可写成
  `{ "path": "assets/icon.svg" }` 或
  `{ "light": "assets/icon-light.svg", "dark": "assets/icon-dark.svg" }`。
- `runtime` 声明一个长驻的外部进程，通过标准输入输出与 Wuu 通信（Agent 插件）。
- `desktop.entry` 指向包内的 `.js` 或 `.mjs` 浏览器 ESM 文件，路径使用 `/`、不能逃出
  插件包，文件最大 10 MiB。
- `contributes.themes` 声明式主题；`contributes.settings` 声明式设置。
- `skills`、`hooks`、`mcp_servers`、`commands` 可以让插件直接提供这些能力，与用户
  手动配置的效果一致。
- `minimum_wuu_version` 声明所需的最低 Wuu 版本；不满足时插件不会被激活。
- `requires` 列出必须同时启用的插件 ID；缺失时当前插件保持未激活。`breaks` 声明明确
  不兼容的插件，Host 会拒绝同时启用；`conflicts` 只在插件目录显示警告，由用户决定
  停用哪一个。三者都是简单 ID 数组，不支持版本范围或自动求解。

完整字段定义以 [`internal/plugin/manifest.go`](../../../internal/plugin/manifest.go) 和
[`packages/plugin-sdk`](../../../packages/plugin-sdk/) 为准。

## 声明式贡献

### 主题

无需任何代码，在 `contributes.themes` 中声明即可。启用的插件主题会出现在
"设置 → 外观"；禁用插件或切回内置主题时，Wuu 会移除该插件设置的全部 Token。

```json
{
  "contributes": {
    "themes": [
      {
        "id": "my-dark",
        "name": "My Dark",
        "base": "dark",
        "tokens": {
          "--wuu-paper": "#111827",
          "--wuu-ink": "#f9fafb"
        }
      }
    ]
  }
}
```

公开 Token 由 [`config/desktop-theme-contract.json`](../../../config/desktop-theme-contract.json)
统一定义，并生成 Manifest、公开 SDK 和 Desktop 校验代码。稳定类别包括语义颜色、字体、
间距、密度、圆角、边框、层级阴影、动效、内容宽度和 `--wuu-syntax-*` 语法色。早期的
`--wuu-paper`、`--wuu-ink`、`--wuu-accent` 与 `--hljs-*` 等名称继续兼容，并在应用时
映射到当前语义 Token；新主题应优先使用 `--wuu-color-*`、`--wuu-font-*` 等当前名称。

宿主的常用中性界面通过少量粗粒度语义 Token 统一换肤，无需依赖私有 DOM。完整的
Token 清单、说明与宿主接入状态由脚本从主题合同和宿主样式依赖图生成，见
[主题 Token 参考](theme-surface-matrix.md)。文字和字体继续
继承对应的公共颜色与字体 Token；紧凑控件的边框宽度由宿主持有，避免面板级强边框改变其盒模型
或破坏文字换行。

### 设置

`contributes.settings` 声明生成式控件，支持 boolean、string、number 和 enum 四种类型。
每个设置都有 `scope`（`user` 或 `workspace`）和 `apply`（`live` 或 `restart`）。用户
在设置界面修改后，Agent runtime 可以通过版本化的 `host.settings.get/list` Service 读取；桌面
View 则通过渲染参数中的 `host.getSetting` 访问。设置和 Storage 按插件命名空间存储，桌面 View
使用 `host.getStorage` / `host.setStorage` 读写 Storage。禁用、升级和卸载插件都默认保留这些数据，
便于重新安装后恢复；当前没有“卸载时同时删除数据”的隐式行为。

```json
{
  "contributes": {
    "settings": {
      "enabled": {
        "type": "boolean",
        "title": "Enable counter",
        "default": true,
        "scope": "user",
        "apply": "live"
      }
    }
  }
}
```

## Agent 插件

Agent 插件是 `runtime` 声明的外部进程。安装或启用插件即授予该进程与 Wuu 相同的
用户权限，因此只能安装你信任的插件，并在启用前检查其来源。

### 进程与协议

runtime 进程由 Wuu 启动，是一个长驻进程，通过标准输入输出上的逐行 JSON 与 Wuu 通信。
它先协商协议与能力，然后持续接收事件和调用。开发者不需要手写协议：

- TypeScript 侧使用公开的 `@wuu/plugin-sdk` 包；
- Go 侧导入 `github.com/blueberrycongee/wuu/packages/plugin-go`。

协议通道是异步双工的：宿主调用插件能力时，插件可以通过传入的 `RuntimeHost` 反向调用宿主服务；
一次能力调用返回后，插件启动的后台工作也可以继续主动调用已经协商的宿主服务。SDK 按 request id 路由并发响应，
插件不能直接读写标准输入输出或假设请求与响应严格交替。后台 timer、watcher 或进程应在会话
生命周期开始后启动，并在 generation 关闭时停止，不能越过禁用、升级和卸载边界继续运行。

initialize 是只读 prepare 阶段，只能读取 Storage、Settings 和已有 Session 摘要。共享 Storage 写入、
Session create/send 和后台 effect 在 generation 的包与策略提交后才开放，并通过 `activate(host)` 启动。
旧 SDK 没有声明 lifecycle version，不能进入候选 generation。

宿主能力通过 Service Registry 发布：内核把服务以稳定名称 + 版本注册进注册表，插件在
initialize 里用 `required_services` 声明消费项，声明是获得调用权限的唯一途径；调用统一走
`host.service.call` 网关帧，由宿主按注册表路由和校验。当前可调用的内核服务（kernel services）：

| 服务 | 用途 |
| --- | --- |
| `host.storage.get` / `set` / `delete` / `keys` / `compare-exchange` | 命名空间存储；每次调用都必须传 `scope: "user" | "workspace"` |
| `host.settings.get` / `list` | 设置，运行时只读 |
| `host.session.create` / `send` / `list` / `cancel` | 插件拥有的 Session 创建、投递、列表与取消 |
| `host.data.query` | 读取一个 thread 的版本化只读事件快照 |
| `registry.introspect` | 只读自省：注册表里有哪些服务、什么版本、由哪个 generation 提供 |
| `execution.update` | 为一次正在执行的调用报告进度（见"执行作用域"） |

服务名、参数和结果类型以 SDK 的 `KERNEL_SERVICE_NAMES`、`kernelServiceCall` 与
`HostServiceContracts` 为准。`host.session.info`、`host.workspace.*` 和 `host.diagnostics.log`
不属于生产合同。

`host.data.query` `1.0.0` 在不暴露文件系统路径或宿主私有对象的前提下提供持久化 Session
事实。插件先在 `required_services` 中声明主版本 `1`，再经 `host.service.call` 调用其 `call`
方法。参数是 `{ thread_id, types?, turn_id?, limit? }`，结果是
`{ version, thread_id, events }`；每个事件使用稳定 envelope
`{ type, thread_id?, turn_id?, created_at?, data? }`。当前事件类型包括 `turn`、`step`、
`model_call`、`tool_call`、`tool_result`、`streaming_chunk`、`context_requests`、
`provider_states`、`compact_attempts`、`barrier_tool_batch_rejected`、`tool_inventory`、
`tool_records` 和 `final`。请求未知类型会得到空匹配，不会报错。这是快照 API；
`host.data.subscribe` 尚未注册成生产可用的插件 Service。

需要在后台发起普通 Agent 工作时，插件组合产品中立的 Session 服务：`host.session.create` 创建具有明确
owner、visibility、parent、fresh/fork、workspace（`shared` 项目目录，或基于当前 HEAD
创建的独立 `worktree`）和可选模型别名语义的 Session；创建结果会返回实际生效的
`workspace_root`，便于插件记录 Session 的真实运行目录。`host.session.send` 向已有
Session 投递输入。send 请求包含插件生成的 `request_id`、模型输入、有大小上限的 request-only
context blocks、稳定 cause，以及可选的 `presentation: { kind: "query_bubble", text, name }`。
插件通过 `if_running: "queue" | "steer"` 决定目标繁忙时的投递方式：`queue` 在当前 Turn 之后
再启动一个 Turn，`steer` 则把输入注入当前 Turn。默认仍为 `queue`；steer 成功时返回
`steered: true` 和当前 `turn_id`。steer 不会创建第二组生命周期事件，也不能携带
`context_blocks`；完成事件仍关联当前 Turn 的原始请求。

`host.session.list` 默认只返回当前 generation 所属插件拥有的 Session；传入 `scope: "shared"`
时，则返回跨 owner、跨 workspace 的未归档用户可见 Session 元数据，用于只读发现，插件私有 Session
仍不会暴露。即使 Session 来自 shared scope，`host.session.cancel` 也始终执行原有的所有权校验。
cancel 可以用 send 返回的 `turn_id` 精确取消当前 Turn，或用 `queue_id` 移除尚未
开始的投递；两者都不删除 Session。省略两者表示插件有意取消该 Session 当前拥有的执行。生命周期
完成事件包含最终模型输出，但只发送给原始提交插件，便于插件在不读取宿主私有历史结构的前提下
更新自己的状态或向父 Session 交付结果。

`presentation.text` 是前端 query 气泡显示的安全摘要，不是完整内部 Prompt。用户输入和插件唤醒
统一复用标准 query 气泡；宿主仍在持久记录中区分 `origin=user | host | plugin`，并把插件生成项
标成只读、可审计。Provider 适配层会将它投影为普通 `user` role 来驱动下一回合；这个协议角色
不等于真人作者身份，也不能覆盖产品记录中的插件来源。插件不应把完整内部 Prompt 复制到展示
摘要，桌面端也不应自行从模型输入生成气泡内容。
目标 Session 忙时默认进入同一条普通 Turn 队列，除非请求选择 `if_running: "steer"`。插件若声明
`agent.turn.lifecycle` observe 能力，
宿主会把后续 running、completed、failed、interrupted、discarded 状态只发给原提交插件，并带回
原样的 `request_id`；终态还包含最终模型输出。Cron、重试、错过触发恢复、并发合并和业务状态都必须由插件持有；核心不
提供 timer tick，也不解释 `request_id` 或 cause。

每次插件 Tool 调用都包含拥有它的当前 `turn_id`，以及该次分发的唯一 `execution_id`（进度上报
与精确取消见"执行作用域"）。插件若声明 `agent.turn.interrupted` observe
能力，还会收到产品中立的 Turn 中断信号。宿主不会根据 `parent_session_id` 建立取消树；插件可以
把信号转发到它记录的任意 child Turn，也可以让工作脱离当前 Turn。树、DAG、worker pool、汇聚、
重试和恢复等编排语义全部属于插件。

进程生命周期由 Wuu 管理：启用时启动，禁用、升级或卸载时终止。插件不能重启自身或
绕过宿主对进程的监督。

### 提供与消费 Service

插件之间、以及插件与内核之间，通过同一个注册表组合能力。提供方在 initialize 结果里声明
`provided_services`：稳定名称（如 `search.provider`）、严格 semver 版本和带版本化 schema 标识的
方法列表；消费方声明 `required_services`：名称 + 主版本号。声明在 prepare 阶段收集，调用只在
提供方与消费方两个 generation 都激活后流动。

- 没有依赖求解器：消费方要求的主版本没有可解析的提供者时，该消费方的激活会被阻塞并给出明确诊断；
- 调用由宿主认证：服务方收到的 `ServiceCall` 里的 caller 是宿主核实的消费方插件 ID，不能伪造；
- 提供方升级后，消费方按名称 + 主版本重解析继续工作，宿主会发送 `service.changed` 通知；
  提供方卸载或替换后，调用权限随之撤销，在途调用收敛为带类型的错误。

把内核服务迁入注册表之后，宿主与第三方使用完全相同的 provide/consume 合同，不存在只能由
一方插件调用的私有入口。

### 自定义授权与进程沙箱

安全策略和进程隔离是两个独立扩展点：

- 提供 `security.authorize@1` 的 `authorize` 方法，可以读取工具的稳定身份、参数、风险分类、
  调用者、工作区和当前权限模式，并返回 `allow` 或 `deny`。该策略只能进一步收紧权限，不能
  绕过 Wuu 的工作区硬边界。
- 提供 `sandbox.process@1` 的 `confine` 方法，把原始 argv 以及 `read-only` 或
  `workspace-write` 文件策略转换成 Wuu 实际应执行的 argv，并报告 `full` 或 `partial` 隔离级别。
  该扩展点覆盖模型发起的 shell 和托管进程；插件与 MCP runtime 的准入仍由包权限和 grant 管理。
  提供者还可以返回自己的 `denial_signatures` 和 `runner_failure_signatures`；Wuu 只使用本次调用
  返回的诊断方言归因执行失败。

Go SDK 提供 `AuthorizationService()` 和 `ProcessSandboxProviderService()`；TypeScript SDK
提供对应的 Service descriptor 和请求/结果类型。没有提供者时，Wuu 使用内置策略和平台沙箱；
一旦选中自定义提供者，失败不会退回无限制执行：未知授权结果会被拒绝，沙箱提供者报错、返回
空 argv、相对执行器或部分隔离时，进程都不会启动。授权提供者只返回策略决策；交互式审批属于
独立的宿主能力。

这两个合同刻意保持窄小：授权不负责执行命令，沙箱也不决定动作是否允许。容器、虚拟机和远程
执行应提供完整执行 Service，而不是伪装成同机 argv 包装器。

### 执行作用域

每次 `tool.execute`、`capability.invoke` 或 `service.invoke` 分发都是一次 execution，宿主为它生成唯一的
`execution_id` 并随调用帧下发。open 语义由调用帧本身携带、close 随其响应返回，
`execution.cancel` 是唯一的 mid-flight 帧：

- 插件在处理自己的 tool、capability 或 service 调用时，可以调用 `execution.update`（TypeScript
  SDK 也提供 `reportExecutionUpdate`）报告进度
  （`execution_id` + `message` + 任意插件自有 `detail`）；宿主校验调用方必须是该 execution
  的所有者；
- 宿主取消本次分发时发送 `execution.cancel`。Go 处理函数通过 `context.Context` 接收取消，
  TypeScript 处理函数通过第三个参数中的 `AbortSignal` 接收取消；插件负责把信号转成自己拥有的
  任何本地取消原语；
- cancel 是 fire-and-forget：宿主终态由 invoke 返回决定，不等待插件确认；迟到或越权的 update
  会得到 `execution_not_found` / `service_not_authorized` 错误，且不会重新打开已结束的执行。

宿主不基于 execution_id 构建任务树——树、DAG、worker pool 等编排仍然是插件自己的状态。

### 扩展 Agent 的能力

runtime 插件可以注册工具和挂钩 Agent 生命周期。SDK 提供以下能力（以 SDK 导出为准）：

| 能力 | 作用 | 语义 |
| --- | --- | --- |
| `agent.system_prompt.section` | 贡献一段系统提示 | transform |
| `agent.pre_step` | 在模型步骤前追加带来源、可持久化的隐藏消息 | transform |
| `agent.request.transform` | 读取 `ModelRequestViewV1` 并返回受校验的窄 patch | transform |
| `agent.compaction` | 替换摘要压缩结果 | decision；Experimental |
| `agent.turn.completed` | 观察已提交的成功/失败 Turn 摘要 | observe |
| `agent.turn.lifecycle` | 接收本插件投递 Turn 的 owner-scoped 生命周期 | observe |
| `agent.turn.interrupted` | 接收任意 Turn 的非阻塞中断信号，由插件决定是否传播 | observe |
| `plugin.client.request` | 处理插件命名空间内的 Desktop/客户端请求 | decision |

工具通过 initialize result 的 `tools` 注册，不是一个名为 `agent.tool.register` 的 capability。
每个 capability 都属于一个 generation，并声明已经实现的 `kind`（observe / transform /
decision）与 `priority`。`guard`、`around` 没有宿主实现，不是可声明的公开 kind。

旧 `hook.invoke` 已删除；Host→plugin 只通过上表的版本化 capability 或工具执行，plugin→Host 只通过 Host Service。
候选 prepare 失败时旧 generation 继续工作；durable commit 后的单插件 activate 失败会在 inventory 中显示
`failed/last_error`，不会伪装成 active。

插件不会拿到私有 ThreadItem、协议消息、宿主 React 树或任意回调。快照、输入与输出
都是冻结的公开结构，具体类型以 SDK 的 `index.ts` 为准。

`agent.pre_step` 是状态型模型上下文的首选入口。宿主按 capability priority 稳定调用，校验
`append_messages`，并把每条消息标成隐藏、只读和 `origin=plugin` 后随 Turn 持久化。插件可以从
输入里的 `origin_id` 找到自己此前追加的消息，自行实现只注入一次、状态变化时追加 tombstone，
或每轮追加。宿主不替插件维护状态，也不替插件选择缓存策略；该接口只允许在历史尾部追加，
不会改写已有前缀。

`ModelRequestViewV1` 只公开模型、消息摘要、工具 schema 和少量跨 Provider 选项；不包含重试对象、
cache hint、媒体字节、Provider 原生 replay state 或 Go 字段名。当前 request transform 唯一可写字段是
`prepend_system_messages`，使用它意味着插件选择改变请求前缀并承担相应缓存影响。需要新的可写
能力时，必须新增版本化字段和宿主校验，不能恢复成任意 `ChatRequest` 透传。

### 工具注册示例

```ts
import { runJSONLRuntime, type RuntimePlugin } from "@wuu/plugin-sdk";

const plugin: RuntimePlugin = {
  initialize() {
    return {
      protocol_version: 2,
      tools: [{
        id: "my_search",
      description: "Search a private index",
        input_schema: {
          type: "object",
          properties: { query: { type: "string" } },
          required: ["query"],
        },
      }],
    };
  },
  async executeTool({ arguments: input }) {
    const { query } = input as { query: string };
    return { result: { structured_content: { matches: await search(query) } } };
  },
};

await runJSONLRuntime(plugin, { input: process.stdin, output: process.stdout });
```

### 返回生成产物

工具通过富 `content` part 返回图片、文档、音频或资源链接。Wuu 将这些 part 保留在
可恢复的工具结果中，并投影到消息流，而不是伪装成 assistant message。图片默认按结果
顺序显示在过程与回答之间；HTML、PDF 等文档型产物默认汇总在 turn 末尾的产物卡片中。

```ts
const report = await importArtifact(host, {
  path: "output/report.html",
  name: "report.html",
  mime_type: "text/html",
});

return {
  result: {
    content: [
      { type: "text", text: "已生成请求的内容。" },
      {
        type: "image",
        mime_type: "image/png",
        name: "cover.png",
        data: pngBytes.toString("base64"),
      },
      {
        type: "file",
        mime_type: report.mime_type,
        name: report.name,
        uri: report.uri,
        artifact: {
          placement: "turn_end",
          ref: report.id,
          sha256: report.sha256,
          size_bytes: report.size,
        },
      },
    ],
  },
};
```

在 `required_services` 中声明 `requireArtifactImportService()`，并在工具执行仍存活时
调用 `importArtifact()`。宿主会把文件复制到 thread-owned storage，并返回绑定摘要的
Renderer URI。`report.id`（以及 content part 的 `artifact.ref`）才是不透明 Artifact
身份；URI 是宿主能力，可能在 fork 或解析时被重写，不能作为身份比较或持久化。删除所属
thread 会撤销该 Artifact；HTML 本地预览只读取这种受管 URI。
内联 `data` 使用 base64，适合较小产物；远程 `https` 资源仍可直接返回。
`artifact.placement` 只是可选展示提示，支持 `inline` 和 `turn_end`；省略时由 Wuu
根据媒体类型决定。Desktop 插件可以为对应 MIME 类型注册 `tool-result` Renderer；
插件被禁用或渲染失败时，宿主默认 Renderer 仍可显示和打开产物。生成的 HTML 会在
隔离预览中打开，不会直接进入宿主 DOM。
`artifact.ref`、`artifact.sha256` 和 `artifact.size_bytes` 会继续附着在有序 content
part 上；Wuu 不会另外维护第二套顶层 artifact 列表。不要把绝对路径、`file:` 或
`wuu-file:` URL 持久化为 Artifact 引用。

ToolResult 是 JSON 线协议，不是任意 JavaScript 对象。循环引用和 `BigInt` 会导致序列化
失败，`undefined`、函数和 symbol 会被丢弃，宿主也会忽略未知字段。扩展元数据应放入
已声明的 `meta` 或内容级 `artifact` 字段。Capability 的消息视图只暴露文本与
`has_tool_result` 等存在性标志，不会暴露 `tool.execute` 返回的完整富结果。

完整的 TypeScript 双工示例位于 `examples/plugins/stateful-runtime`：activate 后台使用 Storage CAS，
capability handler 使用 Storage，并按显式客户端请求创建和投递 Session。

## 桌面插件

桌面插件是在 Wuu Renderer 中运行的受信任代码，可以注册全局样式，并替换或包装宿主
提供的稳定 UI Surface，用于形成统一视觉体系或做有边界的结构调整。它继续调用 Wuu
提供的会话与导航动作，不需要依赖 DOM monkey patch 或私有 React state。

桌面入口导出 `activate(api)`。不要打包另一份 React；应使用 `api.react` 提供的宿主
React：

```js
export async function activate(api) {
  const React = api.react;

  api.registerStyle({
    id: "visual-language",
    css: `
      :root {
        --wuu-accent: #7c5cff;
        --wuu-accent-press: #6847ed;
      }
    `,
  });

  api.registerSurface("conversation.timeline", {
    id: "timeline-frame",
    mode: "wrap",
    render(_context, fallback) {
      return React.createElement("section", { className: "my-frame" }, fallback);
    },
  });
}
```

`mode: "replace"` 接管完整语义边界；`mode: "wrap"` 包装当前结果。渲染失败只回退当前
边界，宿主始终保留设置、插件禁用和默认 UI 恢复路径。

如果 manifest 声明了 Slot、Surface 或 Presenter，激活时必须逐项精确注册，包括 target、
mode、order 或 priority。注册未声明贡献，或漏掉任何已声明贡献，都会拒绝候选 generation。
manifest 中的 View 入口也必须引用同一 generation 注册的 View。

### 可用 API 概览

- `react`、`ui`、`pluginId`、`generation`：宿主 React、UI Kit、稳定插件 ID 和当前原子
  generation ID。
- `invokeRuntime`：调用当前插件活跃 Agent runtime 提供的方法；`onHostEvent` 观察宿主
  生命周期通知。两者都受当前 generation 约束。
- `registerStyle`：注册 CSS；任意 CSS 只提供给受信任的桌面代码插件。
- `registerSurface`：替换或包装短小、带原生 fallback 的语义项 Surface；App Shell、
  基础 Session、Composer、Settings、插件管理和各 Region 容器都是 protected roots。
- `registerSlot`：在原生 UI 的稳定位置插入内容（`sidebar.primary`、
  `workspace.header`、`composer.toolbar` 等）。复杂设置应声明独立的 `settingsPages` View。
- `registerViewType` + `registerViewPlacement`：注册可持久化的 View，并请求宿主把它
  首次放到 `navigation`、`primary`、`auxiliary`、`inspector`、`settings` 或 `overlay`
  语义区域。`priority` 只在区域尚无用户选择时
  决定初始激活；用户后续的切换和关闭优先并持久化。落位 API 不暴露宿主 DOM、任意
  父节点、分割树或面板尺寸。`registerViewPlacement` 是唯一的落位 API。
- View 的入口由 manifest 声明，插件不自己绘制导航和 Tab：
  - `contributes.navigation` 出现在左侧栏可滚动的插件分组；
  - `contributes.workspaceTools` 出现在右侧工具选择器，并以原生工作区 Tab 打开；
  - `contributes.settingsPages` 出现在设置页的插件分组。
  每项使用 `{ id, view, title, description?, icon?, order? }`，且必须引用同一插件通过
  `registerViewType` 注册的 View。选中、关闭、持久化和溢出由 Wuu 管理。普通
  `contributes.settings` 会自动获得宿主渲染的设置页，只有标准 schema 表达不了的内容
  才使用自定义设置 View。
  入口 `icon` 与顶层品牌图标相互独立：填宿主公开的语义图标名，或使用与顶层 `icon`
  相同的包内资源对象；省略时回退宿主默认图标，不继承品牌图标。插件可从
  `@wuu/plugin-sdk` 导入 `PUBLIC_ICON_NAMES`、`PublicIconName`
  和 `PluginManifestIcon`。自绘资源只接受 SVG、PNG、WebP，单文件不超过 256 KiB；路径
  必须留在插件包内，不能是符号链接。SVG 会拒绝脚本、事件属性、外部引用与嵌入式文档。
  Wuu 统一负责尺寸、选中态、无障碍、主题切换与加载失败兜底，插件不通过桌面模块注入图标组件。
- `registerInspectorSection`：在宿主环境信息面板注册短摘要。输入是冻结且版本化的
  Session、Turn、Workspace、Git 和 TODO 公开 snapshot；Host Action 只允许打开已注册
  View 或执行已注册 Command。可选的 `when(snapshot)` 在当前摘要没有有意义内容时返回
  `false`，宿主不会渲染该 Section 的标题或容器。宿主负责每个 Section 的独立错误边界、限高和溢出。
  长列表、编辑器和复杂交互必须进入 `primary` 或 `auxiliary` View。
- `api.ui`：宿主提供的小型 UI Kit，包括 `Page`、`Panel`、`Card`、`Section`、
  `Stack`、`Row`、`Button`、`ToolbarToggle`、`TextInput`、`TextArea`、`Checkbox`、`EmptyState`、
  `LoadingState`、`ErrorState` 和 `LiveDuration`。`Page` 支持 `comfortable`/`compact` 密度；
  `LiveDuration` 根据累计毫秒和可选运行起点显示实时更新的紧凑时长；状态组件由宿主
  统一处理 ARIA、加载动画、错误视觉、响应式间距和溢出。普通插件页面优先使用这些组件，
  因而会自动继承当前外观插件的颜色、字体、边框、圆角、阴影和密度。复杂 View 仍可使用
  任意 React，画布、终端和专用预览可以保留自己的主题边界；插件不应覆盖 UI Kit 的内部
  class，也不应重新接管宿主的页面边距和公共控件节奏。Composer 工具栏中的二态开关应使用
  `ToolbarToggle`，由宿主统一 `aria-pressed`、命中区域、焦点和激活态。
- `registerPresenter`：替换具体产品概念而不是宽泛区域。目标包括
  `conversation.item`、`conversation.process`、`conversation.tool-activity`、
  `conversation.composer`、`header.conversation`、`header.workspace`、
  `navigation.primary`、`app.status`、`content.preview`、`settings`。Presenter 收到
  冻结、带版本且经过脱敏的 snapshot、原生 fallback，以及只包含当前边界可用动作的
  host；只有出现在 `host.actions` 中的 Action 才能调用。
  `registerToolActivityPresenter` 继续作为兼容的 Tool 专用入口。
- `registerCommand`、`registerStatusItem`、`registerLocale`：命令、状态项和本地化。
- `showConversationCard`：在指定会话底部显示由插件渲染的临时交互卡片。省略
  `threadId` 时使用当前会话；返回的 handle 可以更新状态或关闭卡片。卡片不会写入
  会话历史，也不会进入模型上下文；插件禁用、卸载或应用重启后由宿主清理。
- View 渲染参数中的 `host.getSetting`、`host.getStorage`、`host.setStorage`：读取声明式设置，
  读写插件命名空间的持久化存储。
- `registerRenderer`：按类别（`message`、`tool-result`、`document`、`file-preview`）注册内容
  渲染器，用 `match` 匹配具体内容并接管渲染；`priority` 决定多个插件竞争同一内容时的顺序。
- `registerThemeTokens`：以代码方式为指定主题应用公开 Token 覆盖（声明式主题的运行时版本），
  同样只能修改公开语义 Token。
- `registerCSSSnippet`：注入按插件作用域管理的 CSS 片段，随 generation 卸载时移除。
- `registerCleanup`：在 generation 卸载时执行清理回调（释放外部资源、取消订阅等）。
- View 作为设置页挂载时，渲染参数中的 `host.settings` 提供 `SettingsPageHostAPI`
  （`contractVersion: 1`），当前可读写宿主 `runtime.modelAliases` 设置。

### 斜杠动作与临时卡片

`contributes.commands` 中的 `runtime_action` 会与桌面入口通过 `registerCommand` 注册的
同 ID 命令匹配。只有插件已获批、启用且桌面命令完成注册时，命令才会出现在 Composer
的斜杠菜单中：

```json
{
  "contributes": {
    "commands": [{
      "id": "show-status",
      "title": "Show status",
      "description": "Inspect the current plugin status",
      "kind": "runtime_action",
      "aliases": ["status"]
    }]
  }
}
```

```js
export function activate(api) {
  let backgroundCard;

  api.registerCommand({
    id: "show-status",
    title: "Show status",
    execute(input) {
      const card = api.showConversationCard({
        threadId: input?.threadId,
        title: "Plugin status",
        state: { status: "ready" },
        render({ state, dismiss }) {
          return api.react.createElement(
            api.ui.Stack,
            { gap: "small" },
            api.react.createElement("span", null, state.status),
            api.react.createElement(api.ui.Button, { onClick: dismiss }, "Close"),
          );
        },
      });
      backgroundCard = card;
    },
  });

  api.onHostEvent((event) => {
    if (event?.kind === "notification" && event.message?.method === "turn/completed") {
      backgroundCard?.update({ status: "last turn completed" });
    }
  });
}
```

`onHostEvent` 收到的是 app-server 通知，结构为 `{ kind, workdir, message: { method, params } }`；
`method` 使用真实方法名，例如 `turn/started`、`turn/queued`、`turn/completed`、`turn/error`，
方法名常量见 `internal/appserver/protocol.go` 的 `Notification*` 定义，事件类型见
`packages/protocol`。示例中的 `turn/completed` 在通知到达时才会触发更新，不要依赖任何
文档未列出的自定义事件名。

桌面插件也可以在后台事件或自身异步任务中直接调用 `showConversationCard`，不要求先执行
斜杠命令。显式 `threadId` 可把卡片放入已加载的其他会话；省略时卡片进入当前会话。
宿主负责底部位置、外壳、关闭按钮、错误边界和插件生命周期，插件负责卡片内部 UI 与状态。

### 声明式 CSS 锚点

常用宿主 Dialog、菜单、Popover、Tooltip、Notice 和浮动导航统一渲染到受保护的
Layer Host，并带有稳定的 `data-wuu-component`、`data-wuu-layer` 和 `data-wuu-state`
属性。拖拽预览、PDF ShadowRoot 内容和插件 View pane 仍是专用渲染边界。

主要界面区域和控件带有公开的 `data-wuu-component` 锚点，让逐元素微调可以走 CSS
snippets 而不是新增主题 Token：`app-shell`、`sidebar`、`conversation-pane`、
`settings-shell`、`settings-sidebar`、`settings-content`、`settings-page`、
`skills-catalog`、`workspace-panel`、
`workspace-tool-tab`、`workspace-tool-tab-close`、
`launch-view`、`turn`、`message`（区分 `data-wuu-variant="user" | "agent"`）、
`composer`、`composer-input`、`composer-send`（区分 `data-wuu-state="send" | "stop"`）。
工作区 Tab 使用 `data-wuu-active="true" | "false"` 表示选中状态，并在关闭或拖拽时通过
`data-wuu-state="closing" | "dragging"` 暴露瞬时状态。
消息操作区只公开宿主管理的 `message-actions` 组锚点，通过
`data-wuu-placement="persistent" | "overlay"` 区分常驻与悬浮。主题可使用
`--wuu-message-actions-block-gap`、`--wuu-message-actions-overlay-gap`、
`--wuu-message-actions-control-gap`、`--wuu-message-actions-inline-offset`、
`--wuu-message-action-size` 和 `--wuu-message-action-radius` 调整节奏；键盘顺序、点击区域、
响应式收纳和两种布局语义仍由宿主负责。插件应把直接子按钮作为同一控件族统一处理，
不按 copy/like/edit 动作建立私有样式。user query 的实际气泡表面另有
`message-bubble` + `user` 变体，并由 `--wuu-message-user-*` Token 控制视觉属性。
侧栏导航（主栏、插件入口、项目行、会话行、设置栏和返回按钮）的 hover 共享同一套公开
Token：`--wuu-nav-item-hover-background` 绘制悬停/展开行的底色，
`--wuu-nav-item-hover-ring` 调整内描边颜色，两者都回退到宿主玻璃质感，
主题无需依赖私有行级 class。收起侧栏后滑出的浮层抽屉通过
`--wuu-color-surface-muted` 上色，与停靠态侧栏保持同一材质。
插件 UI Kit 公开 `plugin-ui-page`、`plugin-ui-panel`、`plugin-ui-card`、
`plugin-ui-section`、`plugin-ui-stack`、`plugin-ui-row`、`plugin-ui-button`、
`plugin-ui-field`、`plugin-ui-input`、`plugin-ui-empty-state`、`plugin-ui-loading-state` 和
`plugin-ui-error-state`、`plugin-ui-live-duration`；外观插件应优先改公开 Token，确需结构化装饰时
再按这些粗粒度语义处理。生产语义锚点测试会约束上面的宿主清单，以及截至
`plugin-ui-empty-state` 的核心 UI Kit 锚点；loading、error 和 live-duration 是当前公开输出，
但尚未纳入该 inventory 测试。

可信代码插件补充 CSS 时，应只使用这些公开属性和 Token，不应依赖私有 class 名或
DOM 层级。依赖私有 class 名可以用于本地实验，但不属于兼容性承诺。

Raw CSS 会原样进入宿主 document，不会自动改写 selector，也没有 ShadowRoot/iframe 隔离。
`:root`、`body`、通配符或宿主 selector 都可能影响全局界面，因此这是 high-trust Desktop
能力，不是面向未知第三方代码的安全样式沙箱。

## 本地开发闭环

### 脚手架与构建

```bash
wuu plugin create my-plugin      # 生成骨架
wuu plugin validate .            # 校验 manifest 与包结构
wuu plugin build .               # 运行必需的 package.json scripts.build
wuu plugin test .                # 校验包，并在有 runtime 时校验协商描述符
wuu plugin pack .                # 打成可分发 zip
```

`wuu plugin create NAME` 默认生成 agent 骨架；使用 `--type desktop` 或 `--type full`，并可
用 `--output DIR` 指定输出。名称必须以小写字母开头，只含小写字母、数字、`-`、`_`，
最长 64 个字符。

`wuu plugin build` 要求存在 `package.json` 和非空 `scripts.build`；可用
`--package-manager` 覆盖 packageManager 字段和 lockfile 探测。`wuu plugin test` 会启动
已声明 runtime，校验初始化、协议协商、capability 描述符和
工具注册；只有 Desktop 入口的包会跳过 runtime 测试，命令不会导入或渲染 Desktop 入口。
可用 `--timeout` 修改默认 30 秒超时。任何检查失败都会返回非零退出码，适合接进 CI。

### 开发模式热重载

```bash
wuu plugin dev .
```

`wuu plugin dev .` 授权**参数指定的路径**（这里是 `.`）为开发目录：保存后自动构建、校验候选、发布原子 generation，
并保留活跃 generation 的租约直到切换完成；构建或激活失败时保留上一代。目录授权
是开发专用，绝不转移到从下载包里安装的普通插件。

### 安装与发布

```bash
wuu plugin pack .                    # 打包后分发
wuu plugin install ./my-plugin-1.0.0.zip
wuu plugin approve my-plugin
wuu plugin dev .                     # 开发期修改
```

批准并启用就是信任决定：插件以你的用户权限执行。当前安装器只接受本地目录或 zip，尚不
支持直接 npm/Git source。CLI 会暂存每个新包 fingerprint 直至 `approve`；Desktop 插件目录
则把批准与启用合并为一次确认。

### 示例

仓库中的 [`examples/plugins/deep-ui`](../../../examples/plugins/deep-ui/) 是一个可以直接
安装的自包含示例：包含一个 `conversation.timeline` wrapper 和一个声明式主题。

[`examples/plugins/developer-loop`](../../../examples/plugins/developer-loop/) 是只依赖
公开 SDK 的跨 Surface 验收示例，覆盖 Agent runtime（request transform、工具注册）、
Host Actions、generation 替换、失败恢复、disposal 和卸载，并演示完整开发闭环
（install → build → test → dev → pack）。

[`examples/plugins/manga-studio`](../../../examples/plugins/manga-studio/) 是强风格外观压力
测试：它同时覆盖应用壳与设置页，验证主题 Token、UI Kit、语义锚点和宿主布局所有权，
不应被当作 Wuu 默认视觉规范。

[`examples/plugins/tool-card-skin`](../../../examples/plugins/tool-card-skin/) 演示外观 Presenter：
它只读取版本化 `ToolActivitySnapshot`，保留原生 fallback，不解析 Tool 参数或访问 Loop 私有状态，
可与 Manga Studio 独立启用和禁用。

## 版本兼容

插件跨具备兼容性的 Wuu 产品版本继续工作的承诺（开发者不 fork 也能跟上更新）是当前平台的
release gate，尚未完成验证：协议与 manifest 兼容锚点已存在，但还缺少
previous/current 的 SDK 与宿主兼容矩阵。在矩阵验证完成前，不要承诺
插件会跨产品版本无条件工作；发布插件时声明 `minimum_wuu_version`，并在 Wuu 升级后
重新验证。

## 信任边界与安全内核

- 插件代码以用户权限执行，Wuu 不提供沙箱。Renderer 不会读取插件的绝对路径，
  Wuu 在加载前由 app-server 记录已安装包身份和 fingerprint，
  并确认插件在当前 workspace 中已启用；Electron 主进程通过内容寻址的
  `wuu-plugin:` 协议加载模块；CSP 不开放 `unsafe-eval` 或任意本地脚本。
- 插件管理、安全模式、崩溃恢复、原生窗口与 app-server 生命周期，以及用户逃生路径
  （设置、禁用插件、恢复默认 UI）始终由 Wuu host 控制，**永不**通过公开接口暴露给插件。
- 使用 `WUU_SAFE_MODE=1`、`wuu app-server --safe-mode` 或 Desktop 的 `--safe-mode` 启动时，
  Wuu 只发现 manifest 供插件管理展示，不激活任何插件 runtime、Tool、Skill、用户自动化 Hook 或 Desktop 模块。
- 声明式主题只能修改公开语义 Token；`registerStyle` 可以使用任意 CSS，因此只提供给
  受信任的桌面代码插件。
- runtime 进程与 Wuu 同权限，安装第三方 runtime 与直接运行第三方本地命令具有
  相同风险；安装前检查来源。同一来源身份的更新延续信任，来源身份改变时重新确认。
