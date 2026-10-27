# 编写插件

一个 Wuu 插件包可以组合 agent 运行时、桌面 UI、技能、hook、MCP 服务器、命令、主题和设置。完整示例见 [agent 快速上手](plugin-quickstart.md)或[桌面快速上手](desktop-plugin-quickstart.md)。本页说明这些示例使用的包和运行时契约。

## 包结构和 manifest

包根目录包含 `plugin.json`，只需声明实际使用的入口和贡献：

```json
{
  "schema_version": 1,
  "id": "example-plugin",
  "name": "Example Plugin",
  "version": "0.1.0",
  "runtime": {
    "protocol": "wuu-plugin-v1",
    "command": "node",
    "args": ["dist/runtime.js"]
  },
  "desktop": { "entry": "dist/renderer.js" }
}
```

这个例子需要两个已声明的文件。纯桌面或声明式插件可省略 `runtime`；不需要 renderer 代码时省略 `desktop`。桌面入口必须是包内相对路径指向的、自包含的 ESM 模块。打包会排除 `node_modules`，因此运行时依赖也必须在打包后可用；适用时应将 JavaScript 依赖一起打入运行时入口。

| 字段 | 契约 |
| --- | --- |
| `schema_version` | 包 schema，目前为 `1` |
| `id` | 稳定的包身份，更新时应保持不变 |
| `version` | 插件自身版本，与 Wuu 产品版本分开 |
| `minimum_wuu_version` | 最低 Wuu CalVer，例如 `2026.9.1`，不能代替服务或 API 兼容性检查 |
| `platforms` | 可选的宿主平台限制 |
| `requires` | 兼容旧格式的必需包 ID，不含版本约束 |
| `dependencies` | `{ id, version?, optional? }` 形式的包关系；必需依赖必须可用且满足版本约束 |
| `breaks` | 硬不兼容关系；不兼容的包同时启用会拒绝激活计划 |
| `conflicts` | 软冲突，只报告，不自动禁用任何一方 |
| `skills`、`hooks`、`mcpServers` | 分别使用[技能](skill-authoring.md)、[hook](hooks.md)、[MCP](mcp.md) 契约的贡献 |
| `contributes` | 命令、主题、设置、UI 声明和视图入口 |

`dependencies` 在不改变包 schema 的前提下增加版本检查。例如：

```json
{
  "dependencies": [
    { "id": "text-analysis-example", "version": "^1.0.0" },
    { "id": "draft-export", "version": ">=1.2.0 <2.0.0", "optional": true }
  ]
}
```

省略 `version` 表示接受任意包版本。约束使用 SemVer 语法，包括精确版本、比较、`^` 和 `~`；预发布版本必须由约束明确允许。这些是插件包版本，不是 Wuu 的产品 CalVer。`requires` 仍受支持，并与必需的 `dependencies` 合并。不要将同一依赖同时声明为可选和必需。

必需依赖缺失、被禁用、未获信任或版本不兼容时，消费者及其下游必需消费者保持未激活。可选依赖不可用或不兼容时，不阻止激活。可用且版本匹配的依赖建立 provider 先于消费者的顺序；必需依赖环会拒绝激活计划。若可选排序边会构成环，则跳过该边并记录诊断。声明关系不会下载、安装、启用其他包，也不会授予信任。`breaks` 和 `conflicts` 仍为 ID 列表。`requested_permissions` 描述申请的权限，不会为插件进程建立沙箱。

包依赖与运行时服务要求用途不同。包依赖选择已安装的包并确定生命周期顺序；`provided_services` / `required_services` 协商命名 API 和服务主版本，不按包 ID 选择，也不继承包版本。功能需要特定包及其 API 时，应同时声明两者。仅依赖服务的消费者则可接受该服务的任意合格 provider。

## 不替换 provider 也能协作

新增插件可以消费已有服务，并向服务所属插件公开的 UI 插槽贡献内容，
无需修改该插件或选择替代 provider。例如，[计数器与控制插件](../../../examples/plugins/additive-counter/README.md)
组合了外部进程状态/操作 API、公开的类型化 helper、可取消订阅和嵌套桌面贡献。
这只是可复验的一个样本，不限制插件组合服务、hook、capability 和 UI 的方式。

使用 `api.invokeService` 前先声明 `desktop.required_services`；运行时消费者在
初始化时声明 `required_services`。宿主依据消费者身份和已捕获的 generation
路由调用。所属插件通过 `registerUISlot` 发布插槽，其他包通过
`registerUIContribution` 添加内容。使用公开 UI kit 和契约 context，
不要访问私有 DOM 或 controller。注册和调用取消由 generation 管理；
视图卸载时也应停止领域订阅。

交互安装缺少必需依赖的新插件时，候选会被拒绝。更新若会破坏已活跃消费者的
必需依赖，也会在发布前拒绝并保留原有可用图。可选依赖不保证服务可用：应处理
调用失败，只禁用受影响的功能。包版本约束与服务主版本是独立检查。
安装附加插件并不隐含替换现有 provider；存在 provider 冲突时，仍需明确选择组合。

## 声明式贡献

主题、外壳皮肤和设置不需要桌面模块。例如，将下面的片段合并到 manifest：

```json
{
  "contributes": {
    "themes": [{
      "id": "calm-night",
      "name": "Calm Night",
      "base": "dark",
      "tokens": {
        "--wuu-color-canvas": "#151820",
        "--wuu-color-accent": "#8fa7ff"
      }
    }],
    "settings": {
      "enabled": {
        "type": "boolean",
        "title": "Enable feature",
        "default": true,
        "scope": "workspace",
        "apply": "live"
      }
    }
  }
}
```

主题 token 来自[生成的主题契约](theme-surface-matrix.md)，不是任意宿主 CSS 变量。公共语义颜色为独立 UI 外壳提供可读取的默认值。在主题的 `tokens` 中声明颜色即可覆盖内置界面；未声明的颜色保留各组件的默认外观。清除或停用主题会恢复这些默认外观。设置支持 `boolean`、`string`、`number`、`enum`，作用域为 `user|workspace`，应用方式为 `live|next-operation|restart`。运行时通过 `host.settings.get/list` 服务读取，桌面 View 通过 `host.getSetting` 读取。禁用或删除包默认保留设置和存储。

`contributes.commands` 中的命令包含本地 ID 和 kind。`prompt_template` 引用包内有大小限制的 UTF-8 文件，提供宿主管理的输入框动作；`runtime_action` 指向桌面 generation 注册的命令，只有声明不会执行代码。`side_conversation` 引用包内提示词，打开宿主管理的附属会话界面；消息继续使用现有侧聊执行与历史机制，宿主保留当前权限边界。发送请求使用 `plugin.command` 公共 ID 标识命令；停用或移除所属插件会取消正在生成的回答，但不会删除历史。同 ID 更新从下一次回答生效，正在生成的回答保留其原有 generation。此类命令不需要桌面入口或辅助进程。

首次设置在完成前中断时，会保留已经应用的插件选择。清单区分实际 `enabled` 状态与可选的 `enabled_override` 用户设置；没有 override 时使用插件默认值。

manifest UI 声明使用 `contributes.presenters`、`navigation`、`workspaceTools` 或 `settingsPages`，描述放置位置和入口，实际渲染由桌面模块注册。对于已声明的 UI 区域，调用 `registerUIContribution` 并指定带所有者前缀的目标，无需 manifest 条目。旧 `contributes.slots` 和 `surfaces` 字段会被拒绝。组合规则见[桌面 UI 插件](desktop-plugins.md)。

### 配置快照与生效时机

修改配置不会重启插件或重建其资源。为每个非 secret 设置选择生效方式：

| `apply` | 运行时读取行为 |
| --- | --- |
| `live` | 每次读取或刷新采用最新的、完整验证通过的候选配置。 |
| `next-operation` | 经过宿主认证的工具、能力或服务执行首次读取快照或非 secret 设置时，固定全部操作级设置；同次执行后续读取保留这些值，新执行采用新值。 |
| `restart` | 在 generation 准备阶段之前固定；该 generation 及其保留的在途执行都使用旧值，成功重载或宿主重启后采用已保存的新值。 |

连接端点、池结构等资源配置使用 `restart`，操作参数使用 `next-operation`。
执行级读取要使用该回调收到的 `host`；后台 host 没有所属操作。对插件自定义的
多步骤操作，即使设置声明为 `live`，也可以只捕获一次，再把不可变快照传给每一步：

```ts
import { createPluginSettings, requireKernelService } from "@wuu/plugin-sdk";

// initialize(): required_services: [requireKernelService("host.settings.list")]
// 在 executeTool、invokeService 或 invokeCapability 中：
const settings = createPluginSettings(host, { signal: execution.signal });
const config = await settings.capture();
// 整次操作使用 config.values；后续刷新不会修改已有快照。
```

`host.settings.list` 继续接受 `{}` 并返回 `entries`，同时增加不透明的 `revision`、
已保存配置的 `scope_revisions`、以及待重启生效的本地键 `restart_required`。
可选参数 `{scope: "user" | "workspace"}` 过滤返回值及其元数据。修订标识是内容
令牌，只能比较相等，不能排序，也不是事件日志。每个 scope 只读取一次提供方文档，
所有已声明值通过验证后才发布。各 scope 独立提交；组合快照**不是跨 scope 事务**，
相互依赖的值应放在同一 scope。secret 条目始终为空字符串，修订标识不包含秘密，
也不随秘密是否已配置而改变。

读取器的 `refresh()` 只有在读取与可选的 `validate(values)` 跨字段校验成功后才
原子替换 `current`。存储值非法、后端失败或取消会拒绝刷新，已有快照不变。
`subscribe(callback)` 只通知新发布且修订不同的快照，返回取消订阅函数。
可选的 `watch(intervalMs)` 默认每秒刷新，允许 100–60,000 毫秒，并合并并发刷新。
必须等待其 Promise 或处理拒绝。订阅不会自动启动观察；轮询可能合并中间变化，
不是持久事件流。后台观察传入 generation 生命周期 signal；中止后清除监听器，
晚到结果不得发布。监听器异常会在**发布后**使刷新拒绝，不会回滚已发布配置。

现有 `get`/`getAll` 接口和 `host.settings.get/list` 调用形态保持可用。新读取器要求
快照元数据，面对旧宿主会明确拒绝，不把多次独立读取伪装成一致快照。
桌面 `host.getSetting` 用于配置编辑器，返回**已保存值**，不代表运行时 restart
设置的当前生效值。secret 读取仍不返回令牌。

带 fingerprint 校验的 `plugin/setting/set` 编辑入口在现有 `key`、`value` 外，
接受可选的 `patch`，原子修改主键所在 scope 内的其他非 secret 键。完整声明候选
验证通过后，通过所选提供方的一次 compare-and-exchange 提交。重复键、外来键、
跨 scope 修改以及 patch 中的 secret 都会在写入前拒绝；这也允许一次修复 schema
变更后多个非法存储值。已移除的声明仍保留存储以兼容降级，但不暴露给当前插件。
结果不确定的提供方写入失败不会自动重试；先重新读取，再决定是否重试。

执行 `node scripts/plugin-settings-e2e.mjs`，可用打包后的 SDK 子进程验收在途读取、
重载、资源稳定性、候选拒绝及退休。测试只用合成状态，可复查证据保存在
`.tmp/e2e/plugin-settings/`，也可通过 `WUU_SETTINGS_EVIDENCE` 指定目录。

### 外壳皮肤

使用独立于 `contributes.themes` 的 `contributes.skins` 声明皮肤。每套皮肤包含插件内唯一的 `id`、`name`，以及必填的 `light` 和 `dark` 两种方案。每种方案提供不透明的 `#RRGGBB` 格式 `background` 和 `foreground`，可选包内相对路径 `image`，以及可选的 `overlay`、`hover`、`active` 颜色（`#RRGGBB` 或 `#RRGGBBAA`）。`position` 可选 `center`（默认）、`top`、`bottom`、`left` 或 `right`。

```json
{
  "contributes": {
    "skins": [{
      "id": "ocean",
      "name": "Ocean",
      "light": { "background": "#234b60", "foreground": "#f3fafc", "image": "ocean.svg" },
      "dark": { "background": "#112e41", "foreground": "#e3f1f6", "image": "ocean.svg", "overlay": "#06172555" }
    }]
  }
}
```

图片支持 PNG、JPEG、WebP 和自包含 SVG，每张最多 8 MiB；渐变和纹理也通过图片提供。路径必须位于插件包内，SVG 不允许外部引用和脚本。桌面只提供所请求包指纹中声明的图片。图片以整个窗口为基准连续覆盖 ribbon 和外框。应用负责布局、裁切、交互和内容区边界，皮肤颜色不会覆盖内容主题变量。请搭配图片设计前景、悬停和选中颜色，保证控件清晰。

皮肤选择保存在桌面配置中，跟随浅色／深色模式，并在插件更新后保留。禁用、移除插件或进入安全模式会停止装饰外框；重新启用同一插件会恢复选择。图片加载失败时显示方案底色，并在预览中报告图片不可用。同一插件可以同时提供皮肤和内容主题，用户分别选择。可安装的完整示例见外壳皮肤示例（`examples/plugins/shell-skins`）。

### 秘密设置与认证请求

将凭据声明为 `secret` 设置，要求 `default: ""`、`scope: "user"`、
`apply: "live"` 和 `credential_origins: ["https://api.example.com"]`。
每项必须是完整 HTTPS 来源，不含路径、查询、片段或用户信息。宿主设置界面使用
掩码输入和明确的保存、移除按钮。凭据保存在 macOS 钥匙串或 Linux Secret Service，
不会回退到明文文件；存储不受支持或被锁定时会明确报错。秘密设置读取始终返回空字符串，
只有宿主设置界面收到 `configured` 存在标记；凭据不会写入普通设置或工具历史。

运行时声明 `requireKernelService("host.credentials.request")`，再调用
`host.call("host.credentials.request", { credential: "token", url, method,
headers, body }, { signal })`。凭据键只能指向本插件声明的秘密设置。宿主仅向声明的
来源添加 Bearer 认证，拒绝重定向以及调用方提供的认证、Cookie、路由和连接控制头，
返回 `{ status, headers, body }`。响应头使用小写，仅暴露内容类型、重试间隔和请求 ID。
请求超时为 20 秒，请求与响应正文各限 4 MiB。响应正文、头及传输错误会隐藏令牌。
不要把凭据作为工具参数，也不要复制到普通存储、日志或生成的插件包。

凭据绑定 Wuu 数据目录、插件来源身份、本地键和来源集合；项目插件还绑定项目根目录。
更改这些信息需要重新输入凭据。停用插件会阻止新调用，但保留凭据，直到用户移除。
凭据已保存不代表 API 权限已验证。令牌刷新和服务专用授权仍由插件负责。
写入失败或超时可能已经在远端生效，不要静默重试。

## 运行时进程和生命周期

宿主启动 manifest 中的命令，通过 stdin/stdout 交换每行一个 JSON 对象。stdout 专用于协议帧，诊断信息写入 stderr。manifest 的传输名称 `wuu-plugin-v1` 与协商的能力协议编号是不同字段；使用服务的新运行时应协商 `protocol_version: 3`。

官方插件编写 SDK 是 TypeScript 的 `@wuu/plugin-sdk`。实现 `RuntimePlugin` 并传给 `runJSONLRuntime`，SDK 负责消息分帧、请求 ID、取消信号和 lifecycle-v2 握手。其他语言的进程也可实现相同的 JSONL 契约，但 Wuu 不提供另一套编写 SDK。

添加能力（包括观察能力）时，应从 `initialize` 返回 `protocol_version: 3`。
这是**返回结果**中的字段：宿主的 `params.protocol_version: 1` 标识传输协议，
不能照搬为能力协议版本；`params.capability_protocol_version` 表示宿主支持的
能力协议。没有服务声明时，省略返回结果的版本意味着仅支持工具的旧版 v1，
不是最新版。SDK 会在返回声明前，以 `invalid_capability_protocol` 拒绝 v1 的
能力或模型适配器声明。显式 v2 能力仍受支持；声明服务时，SDK 仍选择 v3。

| 回调 | 职责 |
| --- | --- |
| `initialize(params, host, lifecycle)` | 返回工具、能力、提供的服务和需要的服务 |
| `prepare(host, lifecycle)` | 服务绑定完成后读取并验证状态，不写入或启动后台工作 |
| `activate(host, lifecycle)` | 运行时变更提交后，启动定时器、订阅或其他行为 |
| `executeTool(params, host, execution)` | 执行已注册工具，返回结果 |
| `invokeCapability(params, host, execution)` | 处理已声明能力 |
| `invokeService(params, host, execution)` | 响应一次宿主路由的服务调用 |
| `serviceChanged(params, host)` | 响应服务解析变化 |
| `shutdown()` | 停止插件拥有的工作并释放资源 |

`initialize` 只声明契约。Lifecycle v2 在全部声明绑定后增加独立的 `prepare` 请求。请将持久状态的读取和验证从 `initialize` 移到 `prepare`，使用配套 SDK 重新构建；手写适配器需返回 `lifecycle_version: 2`。旧生命周期适配器会在提交前被拒绝。provider 先于消费者准备；准备阶段只能调用已声明且标记 `preparation: true` 的方法，嵌套调用也遵守此约束。该标记承诺不进行持久化写入或产生外部效果；省略时仅可在激活后调用。写入和后台任务应推迟到 `activate`，shutdown 也应能安全处理部分初始化或部分激活。准备和持久化失败时保留旧运行时，不调用候选的 `activate`。变更提交后，激活失败会留下带失败诊断的降级新 generation，不恢复旧代，也不撤销外部效果。明确重试激活时，应避免重复产生效果。可信初始化代码和 MCP 准备仍可能在宿主服务边界之外产生外部效果。提交边界见[运行时 generation](plugin-system.md#运行时-generation)。

请求关闭或连接结束时，`lifecycle.signal` 会中止。激活后启动的后台工作应
使用此信号，并在 shutdown 中停止定时器。清理仍可使用不绑定执行作用域的
生命周期 host 保存状态或释放资源，不应依赖已经中止的信号完成清理。

## 工具和能力

模型工具注册在初始化结果的 `tools` 数组中，每个工具需要 `id`、`description` 和对象类型的 `input_schema`，宿主会生成带命名空间的公开名称。`execution_scopes` 可限制工具在 `root` 或 `child` 中可用；`activity` 描述只读性、并发安全、风险和是否编排子工具。应如实声明副作用，不要把写入工具标成只读来绕过调度或权限检查。这些声明属于可信扩展契约，不是沙箱。

TypeScript 运行时适配器按 `activity.concurrency_safe: true` 调度工具。相邻的显式安全工具可以重叠运行，每个运行时最多同时执行八个工具处理函数。省略或设为 false 的工具、未知工具 ID，以及普通能力和服务回调，仍按 FIFO 顺序独占执行；仅声明 `read_only` 不会启用并发。这些屏障同时适用于共享或不同的 execution ID。initialize、prepare、activate 和 shutdown 还会等待此前已接收的可重入回调结束。

启用前应保护共享可变状态。每次调用的 execution context 与带执行归属的 host，都应随该次调用传递，不要保存为共享的“当前调用”变量。取消在读取帧时注册，并在真正进入处理函数前再次检查，包括等待工具空位之后。取消某个 execution 会中止其所有仍活跃的回调，不影响独立 execution；已运行的代码仍需协作响应取消。显式可重入的能力和服务保留独立通道，使工具空位用尽时嵌套宿主回调仍能完成。该上限不限制这些回调或排队帧数量；TypeScript 并发也不会将同步 CPU 工作移至工作线程。

会话启用 PTC 时，普通工具默认作为程序内绑定。用户交互、显式交付或必须保留
为模型直调的生命周期控制可声明 `direct_only: true`。此类工具留在顶层工具
列表，并拒绝嵌套调用。这个可选标记不授予权限，也不改变执行作用域；未声明
时仍使用普通路由。不要将这项宿主元数据放入参数 schema。

`executeTool` 接收参数以及 `cwd`、调用 ID、可用的会话和轮次标识等上下文。即使有 schema，也应验证参数。结果格式为 `{ result: { content: [...] } }`，工具失败时设置 `is_error: true`。内容可包含文本和支持的富结果部分。需要成为会话产物的文件应使用 `importArtifact`，不要只返回可能消失的临时路径。

能力单独声明 ID、`kind` 和版本。当前支持的能力 ID 为：

| 能力 | 类型和用途 |
| --- | --- |
| `agent.system_prompt.section` | `transform`：添加提示词区块 |
| `agent.request.transform` | `transform`：在模型请求前添加系统消息 |
| `agent.pre_step` | `transform`：在步骤前追加带标识的消息 |
| `agent.step.policy` | `decision`：继续或停止默认循环的模型步骤，见 [Harness 实验](harness-experiments.md) |
| `agent.turn.completed`、`agent.turn.lifecycle`、`agent.turn.interrupted` | `observe`：响应支持的轮次事件 |
| `plugin.client.request` | `decision`：分派插件桌面 UI 发来的请求 |
| `agent.compaction` | `decision`：实验性的压缩契约 |

`agent.request.transform` 接收与 provider 无关的请求视图，公开输出是 `prepend_system_messages`，不是任意修改 provider 的传输请求。SDK 将压缩标为实验性，不应把它当作稳定的、可替换整个 agent 循环的通用接口。

能力可声明 `priority`、`depends_on` 和 `conflicts`。优先级高的先执行，同优先级按插件 ID 排序。`error_policy` 为 `propagate`、`isolate` 或 `ignore`；观察能力不能传播错误，只有观察能力能忽略错误。内置轮次观察能力默认隔离，其他能力默认传播。预期内的失败应在插件内部处理，不要只依赖统一错误策略。

## 调用宿主服务

当前宿主只通过 **`host.service.call` 运行时入口**路由服务。每项消费的服务都要在 `required_services` 中声明名称和主版本。旧的 `host.storage.get` 直接消息不是传输契约。TypeScript SDK 将 `host.call("host.storage.get", params)` 等带类型的简写转换为统一入口。服务要求只通过 `required_services` 声明；`requireKernelService` 等辅助函数用于构造这些带版本的条目。宿主不会维护第二套路由。

例如，下面的初始化结果申请读取存储：

```json
{
  "protocol_version": 3,
  "required_services": [
    { "name": "host.storage.get", "major_version": 1, "required": true }
  ]
}
```

激活后，通过统一入口调用：

```ts
import type { RuntimeHost } from "@wuu/plugin-sdk";

async function readCounter(host: RuntimeHost): Promise<string | null> {
  const result = await host.call("host.service.call", {
    service: "host.storage.get",
    method: "call",
    params: { scope: "workspace", key: "counter" },
  }) as { value: string | null };
  return result.value;
}
```

`requireKernelService` 可构造对应的版本 1 服务要求。`host.supports("host.service.call")` 只检查统一调用入口，不代表某个服务 provider 一定存在。可选服务也需要声明，并处理不可用时的错误。

| 服务族 | 用途 |
| --- | --- |
| `host.storage.get/set/delete/keys` | 插件用户或工作区命名空间中的字符串值 |
| `host.storage.compare-exchange` | 用 `{ scope, key, expected, value }` 条件替换，返回 `{ swapped, value }` |
| `host.file-lock` | 在宿主提供的数据根目录中获取和释放跨进程文件锁 |

| `host.model.request` | 使用宿主配置完成一次有界的纯文本模型请求 |
| `host.model.invoke` | 独立的完整逻辑模型调用与流式响应 |
| `host.model.emit` | 流式适配器或 middleware 发出的事件 |
| `host.background` | 调用插件自己服务的 generation 所属后台工作 |
| `host.client.invalidate` | 提示客户端重新读取插件状态，不携带业务内容 |
| `host.process` | 绑定会话的 shell 进程、输出读取和事件投递 |
| `host.settings.get/list` | 声明的设置值 |
| `host.credentials.request` | 使用宿主持有的 Bearer 凭据向限定 HTTPS 来源发送请求 |
| `host.session.create/send/list/inspect/cancel/control` | 按宿主生命周期规则创建和管理会话 |
| `host.session.history.read/search` | 使用序列快照和续读游标访问有限历史 |
| `host.workspace.status/apply/discard` | 检查或处理所拥有会话工作区中的改动 |
| `execution.update` | 报告活动执行的进展 |
| `execution.invoke-tool` | 在编排执行中调用子工具 |
| `host.artifact.import` | 将文件或字节导入为会话产物 |
| `host.user-question.ask` | 执行期间提出结构化问题 |

大多数核心服务使用 `call` 方法。存储不提供跨 key 事务；并发更新单个值时应使用 compare-exchange，检查 `swapped`，并限制重试次数。先读取再无条件写入会丢失并发更新。不存在的值为 `null`；结构化数据可编码为 JSON 等字符串存储。

创建会话时提供稳定的 `request_id`、`visibility=user|plugin` 和 `context_source=fresh|fork|seed`。发送也需要稳定的请求 ID 和 `input.prompt`。请求被接纳或排队不代表完成，应检查轮次或处理生命周期事件后再消费结果。模型提示、业务状态和重试策略由插件负责，执行、历史、工作区改动和恢复交给宿主。

`host.session.send` 支持可选的 `disable_tools: true`，仅对本轮禁用所有工具。宿主移除模型可见的工具并拒绝工具执行，包括 code-mode 分发；不会影响后续轮次，也不会授予权限。省略或设为 false 时保持普通行为。禁用工具的轮次不能使用 `if_running: "steer"` 或外部代理引擎，不支持的请求会在接纳前被拒绝。暂不支持按轮次配置通用工具允许／拒绝列表。

`host.session.history.read` 只读返回物理审计记录，包括隐藏的模型上下文和工具记录。记录带有可选的 `hidden`、`read_only` 和 `origin` 元数据；省略的布尔标志视为 false，旧记录省略的来源视为空字符串。仅凭 `user` 角色不能认定内容由用户输入。构建对话视图时应保留并使用这些字段：排除隐藏记录，只有非只读且来源为 `user` 或空字符串的用户角色记录才作为真实用户输入。不要按文字特征判断来源，也不要删改真实输入中的相似文字。对话筛选不删除审计记录，也不改变提供方上下文。

`host.session.history.search` 使用 `view: "conversation"` 时，在分页前对用户和助理记录应用这些可见性与来源规则，搜索显示的对话文字，不搜索推理或工具输出。省略 `view` 时保留默认审计／执行轨迹搜索。序号仍指向物理记录，客户端可以恢复原消息，不应将筛选结果重新编号。

`host.workspace.status` 和 `apply` 以创建工作区时冻结的提交为基线，比较已跟踪文件，包含已提交、已暂存和未暂存的改动。`apply` 将累计补丁写入父工作目录，不创建提交，随后移除独立工作区并重新绑定会话。存在冲突、未跟踪文件或基线不可用时，调用失败并保留工作区及其绑定。自动清理也会保留已提交成果和无法验证基线的工作区。

已完成的轮次可以没有 `final_output`：提供商正常结束不要求对外回复或调用确认工具。这只表示本轮执行结束，不证明用户目标已经达成；接收方仍须检查结果和证据，连接失败和异常终止仍会报错。

Peers 插件通过 `if_running: "steer"` 尽量把终态回执注入当前工作；来不及在本轮处理的回执保留到后续轮次，空闲会话仍会被自动唤醒。返回 `running` 和 `steered: true` 时，输入可能尚未被消费，不代表已持久送达。Peers 会保留待送达状态，直到持久记录确认送达；重启丢失的输入使用同一请求 ID 重试。

### 共享文件锁

文件的读改写需要跨插件进程协调时，声明
`{ name: "host.file-lock", major_version: 1, required: true }`。服务提供
`acquire` 和 `release`，SDK 对应帮助函数为 `acquireFileLock` 和
`releaseFileLock`。获取时指定相对锁文件路径，以及 `workspace_state`、
`wuu_home` 或 `project` 根目录。成功后返回不透明的 `lock_id`，释放返回
`{ released: boolean }`。所有写入者必须使用相同锁路径。宿主管理操作系统锁，
在插件 generation 关闭时释放其句柄。

为这项资源握手保留初始化时传入的不绑定执行作用域的 host。获取时传入当前
执行 ID，等待宿主明确回复，然后检查取消，并在 `finally` 中释放：

```ts
const { lock_id } = await acquireFileLock(lifecycleHost, {
  root: "workspace_state", path: "example/state.json.lock",
}, execution.executionId);
try {
  execution.signal.throwIfAborted();
  await updateStateFile();
} finally {
  await releaseFileLock(lifecycleHost, lock_id);
}
```

不要在本地放弃等待获取响应：取消可能与成功授予同时发生，使调用者拿不到
必须释放的句柄。执行 ID 让宿主能取消等待中的获取；进程丢失时由 generation
清理兜底。文件锁不将插件的任意文件访问变成沙箱，也不提供跨文件事务。

## 单次模型请求

在初始化结果的 `required_services` 中声明 `requireModelRequestService()`，再从宿主管理的活动工具、capability（包括 `plugin.client.request`）或服务回调中调用 `requestModel(host, input)`。使用该处理函数作用域内的 `RuntimeHost`，TypeScript SDK 会补充 execution ID。generation 所有的后台任务也会提供活动的服务回调。需要在 observer 或其他处理函数返回后继续工作时，应启动这样的后台任务，并在其回调中发起请求。激活阶段及脱离执行的定时器或任务，没有活动执行作用域时不能直接调用模型服务。

```ts
import { requestModel, type RuntimeHost } from "@wuu/plugin-sdk";

async function requestText(host: RuntimeHost, content: string) {
  return await requestModel(host, {
    messages: [{ role: "user", content }],
    max_output_tokens: 256,
    timeout_ms: 10_000,
  });
}
```

`host.model.request` 的版本为 `1.0.0`，使用 `call` 方法，schema 标识为 `host.model.request.input.v1` 和 `host.model.request.output.v1`。服务协商检查声明的主版本；SDK 中存在 helper 或 `host.supports("host.service.call")` 返回真，都不能单独证明旧宿主支持此服务。

| 输入 | 契约 |
| --- | --- |
| `messages` | 1–32 条消息，角色为 `system`、`user` 或 `assistant`；内容为非空白 UTF-8 文本，总计最多 256 KiB |
| `model_alias` | 可选的现有 `agent.model_aliases` 名称；省略或为空时使用已保存的有效配置中的主模型 |
| `max_output_tokens` | 整数 1–8192，默认 1024 |
| `timeout_ms` | 整数 1–45000，默认 45000 |

`max_output_tokens` 是请求的输出 token 上限，不是费用上限。已配置的 token 限制选项不能提高本次调用的上限。如果提供商适配器在准备请求时移除限制，或将其提高到请求值以上，调用会在提交推理前返回 `unsupported_limit`。这项宿主侧检查不保证远程服务器如何执行 token 限制或计费。

超时同时受 `timeout_ms` 和所属调用的剩余生命周期限制。运行时默认 capability 超时为 30 秒，因此请求可能在 45 秒之前结束。应明确配置包的运行时超时，不要认为模型服务会延长它。取消、所属调用结束或 generation 撤销都会取消尚未完成的提供商工作。

远程 Web 客户端对 `api.invokeRuntime` 另有独立的 30 秒 RPC 超时，增加包的超时时间不会延长它。该时限到达后，客户端会请求取消原来的宿主调用。

结果包含 `text`、`provider`、`model`、`finish_reason` 和 `truncated`。可选的 `usage` 包含输入、输出、缓存创建、缓存读取 token 数及 `cache_creation_unknown`；缺少用量不代表零费用。空或非文本响应，以及请求工具调用的响应都会报错，不会启动工具循环。

宿主复用已配置的凭据、提供商请求准备、容量协调、重试和推理用量记录。不自动加入对话历史、代理专用请求 hook 或工具，也不创建持久会话。上下文由插件自行选择和限制。此服务不接受凭据、端点 URL 或任意提供商覆盖。应说明哪些草稿或历史文本会发送给已配置的提供商，限制请求频率和输出；取消只能尽量避免后续工作，已提交的请求仍可能产生费用。

## 刷新插件拥有的客户端状态

声明 `requireClientInvalidationService()` 以使用 `host.client.invalidate` 版本 1。持久化变更后，调用 `invalidateClient(host, "reactions")`，或使用服务方法 `call`，参数为 `{ topic }`。此机制也适用于日历和其他插件记录的更新。topic 必须匹配 `[a-z][a-z0-9.-]{0,63}`；任意业务内容及调用方提供的身份字段都会被拒绝。

宿主发出 `plugin/client/invalidated`，包含 `plugin_id`（包的 subject ID）、`fingerprint`、`user_space_id`、`product_domain` 和 `topic`。身份来自活动 generation；已禁用、已关闭或已被替换的运行时不能发布。桌面消费者通过 `api.onHostEvent` 接收通知，核对包和上下文后，通过带作用域的运行时 API 重新读取状态。该事件仅提示状态失效，不发送消息、不创建轮次、不写入历史，也不请求用户注意。保留轮询或其他恢复读取路径，以应对通知丢失；提示失败不应撤销此前的持久化写入。

## 可组合的模型扩展

完整逻辑请求使用 `host.model.invoke` 版本 1；原有的 `host.model.request` 纯文本契约保持不变。在 `required_services` 中声明 `requireModelInvokeService()` 以获得服务调用权限，由活动的工具、capability、服务或后台执行提供生命周期。独立推理不会创建对话轮次，也不会执行模型返回的工具调用。

`LogicalModelRequestV1` 包含 `version: 1`、提供商和模型身份、有序消息与工具、缓存提示、模型选项、工具选择、媒体准入和原生回放状态。消息保留 reasoning block、提供商原生 item 及其回退历史、工具调用和结果、发现的工具 schema，以及已解析的图片和文件字节。富工具结果保留 artifact 和延迟媒体引用，以及经过预算限制的 `model_text`。不导出本地临时文件路径、凭据、宿主重试对象或执行权限。复制快照时应保留未知的提供商选项和不透明原生数据。

这是逻辑快照，不保证最终 HTTP 字节完全相同或缓存一定命中。上下文、路由、模型、适配器、原生状态兼容性或媒体变化时，插件需要使旧快照失效。已配置凭据、提供商请求准备、取消和推理用量记录仍由宿主负责。原生转发不能配置任意端点或凭据。

| 扩展点 | 契约 |
| --- | --- |
| `model.request.transform` | 版本 1 的 `transform` capability，接收 `{ request, origin }`，在提供商调用前返回 `{ request? }`。 |
| `model.request.observe` | 版本 1 的 `observe` capability，观察请求及终态响应或错误，并接收完整逻辑请求和宿主确认的来源。 |
| `model.middleware` | 版本 1 的 `decision` capability，错误策略为 `propagate`；可包装调用、返回替代响应，或执行一次性 continuation。 |
| `model.adapter` | 版本 1 的 `decision` capability，错误策略为 `propagate`；提供显式选中的适配器实现。 |

插件发起的独立请求绕过请求 hook 和 around-call middleware，因此观察普通请求后重放不会递归触发同一策略。`origin.kind`、`plugin_id`、operation ID 和可用的会话上下文由宿主填写；复制这些字段不能冒充普通代理请求。

### 独立调用与流式响应

`invokeModel(host, { request, model_alias?, max_output_tokens?, timeout_ms? })` 返回 `{ response, origin }`。比较第二个模型时，选择已配置的模型别名。显式别名提供其已配置的选项和 effort 默认值；请求中的显式 effort 选择该模型对应的 variant，显式 `provider_options` 字段覆盖这些默认值，`request.adapter` 优先于合并后的 `provider_options.adapter`。不指定别名时保留完整请求快照，不用当前配置刷新它。输出 token 和超时受宿主及所属执行约束；缺少用量不代表没有费用，取消也不能撤销已经提交的请求。完整请求保留自己的输出限制，除非显式通过 `max_output_tokens` 覆盖；独立调用默认超时 120 秒，最多 600 秒。输入和保留的流数据分别最多 16 MiB，每条流最多保留 4096 个事件。

`startModelInvocation` 返回 `invocation_id`。`readModelInvocation` 接收该 ID、`cursor` 和可选 `wait_ms`，返回有序事件、下一游标、`done` 和终态结果或错误。句柄绑定当前插件 generation 和执行。`cancelModelInvocation` 取消活动流；`streamModel` 将这些方法包装成异步迭代器，在消费者提前退出或抛错时取消未完成工作。应在处理函数的生命周期内消费流；需要跨越原调用生命周期时，使用后台任务。转发事件时保留不透明的 `provider_state` 和 `lifecycle` 数据；`reset_partial` 为真时，在接收重试的新事件前丢弃已累计的部分输出。 `assistant_item_done` 携带完整的 assistant `message` 检查点，供中断恢复使用；转发时须保留，不应额外追加可见消息。

middleware 接收 `ModelMiddlewareInput`，包含 `request`、`origin`、`stream` 和 `continuation_id`。通过 `invokeModel` 或 `startModelInvocation` 转发时，携带该 continuation ID 和未经修改的请求。continuation 只能由当前活动回调使用一次，不接受模型别名或输出限制覆盖；修改输入应使用 request-transform 扩展点。直接返回响应可以跳过后续调用链；先转发再修改响应则包装下游行为，不另建一次推理 operation。

### 可替换的适配器

初始化时注册 `model_adapters: [{ id: "example.adapter", version: 1 }]` 并实现 `model.adapter`。ID 是全局唯一的点分名称，最多 160 个字符，各段可含小写字母、数字、`_` 和 `-`；候选 generation 内任何重复注册都会失败。通过 `request.adapter` 显式选择实现，或在模型配置中设置 `options.adapter` 以用于普通对话；请求中的显式值优先，`"native"` 选择宿主协议。保留选项在原生传输前会被移除。选中的适配器缺失或被撤销时会报错，不会回退到原生实现。适配器可以生成合成响应，也可以通过 `host.model.invoke` 发起另一条有界请求，并用 `adapter: "native"` 转发给原生提供商。

回调接收 `{ adapter_id, request, stream }`。非流式调用返回 `{ response }`。流式调用可以直接返回该响应，也可以通过 `emitModelEvent` 发出有序事件，以恰好一个 `done` 或 `error` 结束，然后返回 `{}`。发事件前需声明 `requireModelEmitService()`。混合返回响应与事件、缺少终态事件，或在对应流式回调之外发事件都会失败。宿主不会向插件发送凭据。

适配器在注册时明确声明用量记录方式：

- `mode: "direct"`（默认值）：宿主在调用回调前记录一条可能计费的提交，并检查工作流预算。返回的用量是插件报告的本适配器用量；省略用量表示未知，不代表零。额外发起的 `host.model.invoke` 调用分别记录其提交和费用。
- `mode: "delegate"`：用于包装器，例如 `{ id: "example.forward", version: 1, mode: "delegate" }`。宿主保留逻辑操作记录，但不记录一条可计费的父提交。响应中的用量仅用于汇总；每个宿主模型子调用分别记录实际提供商提交和用量，因此转发响应不会重复计费。

这些模式是受信任扩展明确声明的用量契约。原生转发使用宿主配置的凭据和提供商传输。Extension API 不提供自动附加凭据的任意 HTTP 传输；对于插件代码在宿主服务之外自行发起的任意网络请求，Wuu 无法强制执行推理预算。

TypeScript 运行时适配器中的 `model.adapter` 和 `model.middleware` 处理函数可重入，能与等待它们的工具或回调并发运行，因此应保护共享可变状态。TypeScript 的其他 `concurrentCapabilities` 和 `concurrentServices` 仍须显式启用；生命周期屏障继续有效。

### 属于 generation 的后台工作

声明 `requireBackgroundService()`，提供有明确结束点的版本化服务处理函数，在激活后调用 `startBackground(host, { key, service, method, params?, timeout_ms? })`。目标必须是同一插件 generation 自己提供的服务。启动返回 `{ job_id, execution_id }`，回调获得普通的作用域 host 和取消信号，因此可以在原对话或观察回调返回后调用模型服务。

`inspectBackground(host, { job_id })` 返回 `running`、`completed`、`failed` 或 `cancelled`，以及结果或错误；`cancelBackground` 用于取消。活动 key 在每个插件内唯一，最多同时运行 64 个任务并保留 64 个终态结果；参数和结果分别最多 1 MiB。它不是持久调度器：重载、禁用、generation 退役或进程丢失会取消所属工作，旧 generation 不能再启动任务。计时器、调度策略、预算、快照失效和持久业务状态由插件负责，关闭时应清理计时器。

公开 SDK 的[有界重放 fixture](../../../scripts/fixtures/model-pipeline/replay.ts)和[影子比较 fixture](../../../scripts/fixtures/model-pipeline/shadow.ts)使用合成数据验证这些扩展点。它们用于验收，不是默认安装的缓存或比较功能。对源码构建的 core 运行 `node scripts/plugin-model-pipeline-e2e.mjs`，驱动会使用本地模拟提供商并保存可重复检查的结果，不产生付费推理。

### 绑定会话的 shell 进程

声明 `requireProcessService()` 以使用 `host.process` 版本 1。在活动的会话工具回调中调用
`startProcess(host, { key, command, cwd?, timeout_ms?, follow_parent? })`，沿用调用方的
本地 shell 权限、沙箱、工作目录和进程管理器，目前不支持远程执行环境。激活阶段或无关
capability 不能启动进程。同一 key 和相同参数返回已有记录，参数变化会冲突；每代最多
运行 64 个进程，宿主最多保留 256 条终态句柄记录。

`readProcess` 返回合并输出、字节游标、截断信息和进程状态。单次读取范围为 4 字节到
1 MiB，默认 64 KiB，最多等待 30 秒。应使用返回的 `next_offset`，因为 UTF-8 分块及日志
保留策略可能影响实际长度。`stopProcess` 停止整个进程树。超时为零表示无截止时间，
但会话归档或删除、generation 关闭、宿主退出仍会停止进程。`follow_parent: true` 让已完成
的私有子会话将接收方交给父会话，快照中的 `session_id` 返回当前接收方。

`sendProcessEvent(host, processID, message)` 沿用 `host.session.send` 的请求身份、显示和
`if_running` 契约，由宿主选择该进程绑定的接收会话。这样可以向启动它的私有子会话投递
事件，而不会获得任意私有会话的访问权。输出及进程退出本身不会唤醒会话。插件负责分行、
过滤、事件策略和持久历史；运行时重启后句柄失效。内置 [Monitor 插件](plugins.md#监控后台事件)
演示了这个接口。

## 提供服务

初始化时返回 `provided_services`，并实现 `invokeService`。描述包含小写点分名称、严格的 `MAJOR.MINOR.PATCH` 版本，以及带 `input_schema`、`output_schema` 标识的方法。这些标识命名契约，不是内嵌 JSON Schema 定义。

消费者声明服务名和主版本，再通过统一入口调用。宿主提供调用者身份并路由到活动 provider，不应直接寻址另一个插件进程或依赖其私有文件。适配插件可以同时提供和消费同名服务，通过显式的 consumer 绑定委托给另一个实现；已解析的依赖图拒绝自调用和循环。参见[局部扩展 provider](replaceable-providers.md#局部扩展-provider)。缺少必需服务会阻止消费者激活；同名同主版本的多个 provider 会产生诊断，不会合并实现。

[服务组合示例](../../../examples/plugins/service-composition/README.md)（英文）包含两个可构建的包：无状态的文本分析 provider，以及消费其版本 1 API 的写作报告工具。示例展示两种声明和真实的 gateway 调用。

组合验收不能只检查成功的服务调用：激活 provider 和带包内技能的消费者，再终止 provider，并让其下一次初始化失败。恢复检查后，验证已有和新建的消费者工具集都不能加载该技能或调用已撤销贡献，而独立包和普通工作区技能仍可用；同时验证已经创建的提示词保持不变。这个场景不需要模型请求：可用 JSONL 运行时测试进程和直接 `load_skill` 调用检查生命周期边界。

### 可重入服务处理函数

TypeScript 运行时适配器默认按顺序处理普通请求。对于能够安全地与其他处理函数重叠执行的服务，可将其名称加入 `RuntimePlugin.concurrentServices`：

```ts
const plugin: RuntimePlugin = {
  concurrentServices: ["example.text.analysis"],
  // initialize、invokeService 等回调……
};
```

只对可重入的处理函数启用：保护共享可变状态，并传递取消信号。在 A → B → A 服务调用链中，A 等待 B 时，其回调服务必须能够执行。此选项仅影响所列名称的入站 `service.invoke` 请求，不会让所有工具或能力并发，不会授予服务权限，也不会解决包依赖环。`RuntimePlugin.concurrentCapabilities` 为指定能力 ID 提供相同选项，例如在前台任务等待期间处理只读状态请求。仍须保护并发处理函数读取或修改的状态。初始化和激活仍是执行屏障；shutdown 停止接收新工作，等待已接收的工作结束后再清理。这是同一个 JavaScript 进程中的异步重叠执行，不是工作线程。

## 执行与取消

工具和能力调用具有宿主管理的执行 ID。SDK 提供 `execution.signal`，应传给可取消操作，在信号中止时停止工作。工具、服务和能力处理函数收到的 `host` 自动将执行 ID 和中止信号绑定到
服务调用。前台工作应使用这个 host，不要改用初始化时保存的不绑定作用域
host。`host.call(method, params, { signal })` 可附加取消信号，但不能移除执行
本身的信号。取消会拒绝尚未完成的调用，迟到响应会被忽略。调用返回后，执行作用域关闭；迟到的进度不能重新打开已完成执行，取消也不会等待插件确认。

TypeScript 运行时适配器会在进入处理函数前拒绝已取消的工具、能力和服务请求，包括等待其他工作或生命周期屏障的请求。取消某次执行不会跳过清理所需的生命周期回调，并会通知共享该执行 ID 的所有活跃回调。处理函数一旦开始，取消仍是协作式的：应将 abort signal 传给支持取消的操作。取消不能撤销已完成的副作用，也不能强制中断任意插件代码。

App-server 的只读 `plugin/executions/list` 返回 `{ executions: [...] }`，
包含当前连接运行时的当前 generation，以及仍被活动工作持有的旧 generation。
每行在宿主局部 `id`、所属 `plugin_id`、可用执行作用域和最新进度之外，增加
`generation_id` 与 `retained`。用 `(generation_id, id)` 作为稳定的诊断标识：
不同 generation 都可能存在 `exec-1`。原有 `id` 和取消路由保持不变。
结果按 generation ID、执行 ID 排序；调用结束后移除，读取列表不会延长
任何 generation 的寿命。这是即时快照，不是执行历史或跨工作区清单。
禁用插件也会取消其旧 generation 中的工作。

`reportExecutionUpdate`、`invokeTool`、`askUserQuestions` 和 `importArtifact` 都调用版本化服务，需要对应声明。编排工具使用 `activity.orchestrator=true` 和 `execution.invoke-tool`，通过正常工具链委托副作用。重试相同工具和参数时，保持子调用 `call_id` 不变；不要用同一个 ID 表示不同工作。

`security.authorize` 服务可以进一步限制操作，但不能放宽宿主的强制边界。`sandbox.process` provider 必须提供真实的文件系统约束，不是命令审批弹窗。实现前请阅读[权限](../reference/permissions.md)。

## Agent 管理的开发

内置延迟发现工具 `plugin_manager` 支持 `status`、`apply`、`enable` 和 `disable`。`apply` 接受工作区内的 TypeScript 文件或开发包目录，通过宿主选定的 CLI 桥接执行一次发布。不接受任意命令，也不允许调用者指定 Wuu 主目录。验证或导入源码前必须获得宿主代码执行授权，这与写入源文件的权限不同。完整流程见 [Agent 操作步骤](plugin-quickstart.md#让-agent-添加工具)。

状态结果包含 `plugins` 列表。比较 `published_fingerprint` 与 `current_conversation_fingerprint`，并检查 `adoption_pending`、`desired_enabled`、`trusted` 和 `current_conversation_runtime_state`；存在运行时诊断时由 `error` 返回。这些字段说明发布、策略和当前会话的绑定，不证明某个工具执行成功或桌面视图已渲染。

普通更新保留当前轮次及其未完成工作者或受管进程的原工具与提示词 generation。这些工作结束后，在下一次空闲轮次边界发现并调用更新后的工具。禁用或移除提交成功后，则无需等待空闲轮次，直接撤销各个仍被引用 generation 中的插件。发布不等于实际调用工具或检查界面，也不会自行安排后续轮次。

本机界面控制不沿用普通的 generation 保留规则。更新所有者时，宿主在替换版本激活前撤销其浏览器与电脑操作权限；禁用、移除或关闭它也会停止相应活动。绑定活动的 MCP 连接断开或替换后，不能继续控制本机界面。同一插件 generation 内重新连接后，可服务明确的新用户任务，但不会恢复旧调用的权限。权限撤销后，不要让旧任务改用其他本机控制后端重试。详见[运行时 generation](plugin-system.md#运行时-generation)。

## 开发和分发

`wuu plugin dev ./extension.ts` 直接运行默认导出实现 `RuntimePlugin` 的单文件。Wuu 提供匹配的 SDK 并在内部生成进程适配器；此开发路径不要求 manifest、安装、打包或构建。需要支持 `--experimental-transform-types` 的 Node.js 22.7 或更高版本，仅支持 Node 内置模块和 `@wuu/plugin-sdk` 导入。每次发布前保存候选快照，检查语法、导入和默认导出；加载失败则保留上次发布的源码。导入验证会执行模块顶层代码，不是无副作用的检查。`initialize` 在实际宿主中声明契约；随后由 `prepare` 通过所选服务验证状态。准备失败保留旧运行时；激活发生在提交之后，失败时可能留下新运行时的失败状态，契约见上文。完整示例见 [Agent 插件快速上手](plugin-quickstart.md)。

`wuu plugin create` 生成 `agent`、`desktop` 或 `full` 包骨架。对于目录，`wuu plugin dev` 使用其中的 `plugin.json` 和 `package.json` 构建脚本，再发布构建后的开发 generation。两条路径在构建或加载失败后都会继续监听，修复并保存即可恢复，不需要重启。文件事件经过防抖，并定期核对以补偿遗漏；`--poll` 必须为正数。

用 `validate` 检查包结构，`test` 检查可执行初始化和协商描述，再通过真实会话或界面验证行为。`pack` 生成本地 zip，不会上传到 registry。分发前应检查压缩包内容：准备阶段排除 `.git` 和 `node_modules`，不会排除所有可能包含私人数据的本地文件。

Monorepo 插件可在 `package.json` 声明额外构建输入：`"wuu": { "dev": { "watch": ["../shared/src", "../assets/prompt.md"] } }`。路径相对插件目录，支持相邻文件或目录；修改后会重新构建并发布，无需重启。绝对路径、插件祖先目录、经过符号链接的路径，以及超过 64 个额外输入会被拒绝。不要列入生成文件；输入目录中的 `.git`、`node_modules`、`dist` 仍被忽略。这只控制开发监听，不改变运行时权限。

install CLI 会确认信任并启用包。同一已记录来源的更新继承信任与启停状态；来源改变或尚未记录来源时需要确认。开发目录授权不会成为分发包的信任。命令见[插件管理](plugins.md)，信任边界见[安全模型](../reference/security-model.md)。

完整插件编写类型位于 [`packages/plugin-sdk/src/index.ts`](../../../packages/plugin-sdk/src/index.ts)。Go 宿主及其内部应用嵌入接口属于宿主实现，不是另一套插件编写 SDK。请与目标 Wuu 版本匹配；产品 CalVer、包 schema、能力协议、服务版本和桌面快照版本是不同的兼容性检查。

## 私有存储所有权迁移

插件可通过对应的 `storageTransfers.exports` 和 `storageTransfers.imports`
声明，将用户级存储前缀交给另一个插件。每条声明包含 `id`（稳定的迁移标识）、
`plugin`（对方插件 ID）、`scope`（仅支持 `user`）、`domain`（宿主选择的产品域）
和 `keyPrefix`（非空的合法存储键前缀）。导出方指定导入方，导入方指定导出方；
其余字段必须完全一致。

宿主解析实际发现的精确插件身份，并分别验证双方信任。已禁用的导出插件无需运行；
导入插件必须已被选中激活。找不到导出方时，不搜索旧身份或其他用户、产品域的数据。
声明参与包指纹计算，不提供跨插件存储 RPC 权限。

导入插件应在正式激活回调中读取迁入前缀，不应在初始化阶段缓存其内容。
预检不修改存储。启动或更新被接受后，宿主先验证整个目标文档，再持久化源前缀隔离，
然后原子保存原始值和导入回执。键冲突或存储配额超限会阻止迁移，不覆盖目标数据。
迁移失败会阻止导入插件运行；未完成迁移在下次接受激活时恢复。完成回执可防止目标
删除数据后再次导入。合并后的目标仍遵守原有的单插件条目数和字节配额。

源字节保留为非活动备份。持久化隔离后，普通读写（包括保留的旧运行代）无法访问该
前缀。源插件的其他前缀以及其他用户、产品域不受影响。宿主不解析插件业务记录。
回退插件版本不会撤销所有权迁移，也不会重新开放备份。

迁移文档使用存储 schema 2，使旧宿主明确拒绝读取，而不是忽略所有权元数据。
未迁移文档保持 schema 1。降级后的旧宿主不能使用已迁移状态；恢复需要支持迁移的
宿主和保留的原始备份，不会自动恢复旧所有者。
