# 插件系统架构

本文解释 Wuu 插件系统当前的产品模型和架构边界：功能插件与外观插件如何组合，宿主与插件
分别负责什么，以及开发者应该在什么时候选择设置 Schema、View、UI Kit、Slot、Presenter、
Surface 或 Agent 能力。

这是一篇面向平台维护者和复杂插件作者的设计文档。第一次扩展 Wuu 时先阅读
[扩展 Wuu](index.md)；开发 UI 插件时先看[Desktop UI 扩展地图](desktop-plugins.md)。

如果你只想安装和管理插件，阅读[插件](plugins.md)；如果你准备编写插件，阅读
[编写插件](plugin-authoring.md)。本文说明这些 API 为什么存在、怎样协作，不重复完整 API。

## 北极星

> 让功能插件能够自由扩展产品，让外观插件能够统一改变这些扩展的视觉；两者正交、可组合，
> 并且不需要知道彼此的存在。

这意味着：

- 功能插件声明能力、入口和内容，不为某个主题编写专用版本；
- 外观插件作用于公开的语义 Token、UI Kit 和粗粒度界面边界，不逐个追踪私有 DOM；
- Wuu 宿主拥有布局、溢出、滚动、Tab、关闭、键盘、无障碍和系统安全区；
- 一方插件与第三方插件使用同一套机制，不通过产品 ID 特判；
- 允许牺牲任意像素级控制，换取插件组合、宿主升级和长期兼容。

插件系统的目标是让插件形成完整、强烈且一致的视觉语言，同时不破坏产品结构和恢复路径。

### 更上层的北极星：固定插件内核，可替换 Agent Loop

Wuu 的长期目标不是在现有 Agent runtime 外围增加一层插件 API，也不是把某一种 ReAct 循环永久
定义成核心。Wuu 固定的是一个小而强约束的 **Plugin Kernel**：服务发现、作用域生命周期、可靠
Session/Event 存储、输入队列、执行租约、取消、权限、Provider 与 Tool 协议网关、generation
事务和宿主 UI。当前默认 Agent Loop 已由 bundled `DefaultDriver` 驱动。
Driver 目前是进程内实验合同，还不是可由 Manifest 安装或在设置中选择的普通插件贡献。

Loop Driver 决定 Agent 怎样运行：如何消费输入，怎样组织 Prompt 和上下文，一次输入包含多少
Step，Tool 串行还是并行，何时重试、压缩、反思、停止或继续，以及使用单 Agent、Plan-Execute、
多 Agent 还是 Workflow。Kernel 不规定 Agent 应该怎样思考，只保证任何 Driver 都不能破坏消息
顺序、Tool call/result 配对、权限、持久化、取消和恢复。

```text
Wuu Plugin Kernel
  ├── Service / Event / Scope / Effect
  ├── Session/Event Store + Inbox/Outbox
  ├── Provider Gateway + Tool/Permission Gateway
  ├── Lease + Cancel + Checkpoint + Recovery
  └── Generation + Shell/UI Host
          ↓
Bundled default plugins
  ├── default Agent/Session services
  ├── default ReAct Loop Driver
  ├── default Prompt / Tools / TODO / Conversation UI
  └── Subagent / Automation / Memory / Dream
```

TODO 已作为 bundled 一方插件运行：runtime 拥有 Tool、参数校验和结果合同，Desktop 模块拥有
Tool Activity Presenter 与 Inspector section。宿主只保存普通 Tool call/result 事实，并依据公开的
`display.capability = "todo"` 生成版本化事件和只读 snapshot；它不维护或恢复
可变 TODO 状态、注入过期提醒或渲染原生 TODO 界面。TODO 不扩张成跨 Turn 自动续跑、定时唤醒或
长期目标。

Provider 协议不变量、取消、执行租约、持久化完整性、最终权限边界和崩溃恢复仍由 Kernel 保证；
memory、dream、Cron、Subagent 等高级产品不应作为默认循环里的产品分支。HelpMe 和 Goal 直接从
产品与代码中删除，不迁移成插件；协作比这些能力更高层，暂不纳入已完成的一方迁移范围。

这些高级产品应成为一方插件，而且与第三方插件使用同一套公开能力。插件拥有自己的业务模型、
提示、Tool、状态、后台策略和完整界面；Wuu 自己提供的插件只是生态的第一批实现，不是可以调用
私有接口的例外。插件平台是否足够，取决于这些真实产品能否在核心没有对应产品名和产品分支的情况下
完整运行、禁用、升级和卸载，而不只是公开了多少接口。

这不意味着牺牲现有产品体验。比如记忆设置页可以继续提供自动概览、手动重新总结、查看原文，
以及通过对话让 Agent 修改记忆。迁移后，这些交互、提示和数据格式属于 memory 插件；宿主只
负责设置页入口、View 生命周期、模型与执行安全、持久化原语和恢复路径。产品可以保持丰富，
核心不需要知道“记忆概览”是什么。

### 少量可组合链路，而不是工作流 API

Cron、Memory、Dream 和 Subagent 看起来是四套产品，实际都可以落在同一个闭环：

```text
注册 Tool / Prompt / View
        ↓
观察 Session / Turn 事件或插件自己的 Timer
        ↓
创建、选择或复用 Session
        ↓
向 Session 投递模型输入
        ↓
Kernel 可靠接纳输入，所选 Loop Driver 消费并执行
        ↓
插件观察结果，更新自己的状态和界面，必要时再次投递
```

这里的 Session 指一个可持续投递 Turn 的对话执行单元，当前代码内部主要称为 Thread。公开合同
最终采用哪个名字可以随 SDK 版本确定，但不能同时保留两套语义重复的执行系统。

插件平台需要的不是 `cron.run` 或 `subagent.spawn`，而是四组横向能力：

| 公共能力 | 最小职责 |
| --- | --- |
| 注册贡献 | 注册模型可见 Tool、提示与请求上下文、命令、设置、View 和产品入口 |
| Session 操作 | 创建、投递、取消和列出当前插件拥有的 Session；显式发现可共享的用户可见 Session |
| 生命周期事件 | 观察 Session 创建/关闭以及 Turn 排队、开始、完成、失败和取消；中断信号只通知插件，由插件决定是否转发 |
| 状态与资源 | 命名空间存储、受控文件/进程/workspace，以及插件 runtime 自己维护的 Timer 和业务状态机 |

创建 Session 时需要少量通用属性：`owner`、`visibility`、可选 `parent`、上下文来源
`fresh | fork(parent)`、workspace 隔离方式，以及模型/Tool/Profile 选择。`visibility=user` 的
Session 出现在普通会话列表、搜索和历史中；`visibility=plugin` 的 Session 不出现在这些产品入口，
只能由所有者插件管理，但仍由宿主持久化、恢复和审计。Dream 和 Subagent 可以使用插件私有
Session，Cron 可以创建或复用用户可见 Session。

Timer、Cron 表达式、错过触发后的补跑策略和运行记录属于 Cron 插件。插件进程存活时可以自行
计时，重启后依据持久状态重新计算；除非以后明确承诺“Wuu 未运行时也能唤醒”，否则不应先在
宿主建设一个 Cron 产品或万能 scheduler。若未来确实需要操作系统级唤醒，也应作为产品中立的
插件进程唤醒能力单独设计。

### 唤醒主 Agent 与“生成的 query”

Subagent 完成回投是尤其重要的一条公共链路：插件会向已有主 Session 投递
一次新输入，从而唤醒主 Agent。Cron 向用户可见 Session 投递时也可复用同一语义。产品表现统一
使用现有 query 气泡：用户发送和插件唤醒不需要两套布局、颜色或交互节奏。这里“不把它记录成
用户亲自发送的消息”只约束持久化来源和可编辑性，不要求前端把生成 query 画成另一种组件。

因此通用投递必须区分三件事：

- **模型输入**：真正送给 Agent 的 Prompt 或结构化上下文；
- **展示摘要**：前端 query 气泡里的简短说明，可以与完整模型输入不同；
- **来源**：用户、宿主或具体插件，以及稳定的 cause/request id；该来源不改变 query 气泡的
  基础视觉语义。

概念上的合同类似：

```text
session.send({
  session,
  input,
  presentation: { kind: query_bubble, text, name },
  cause,
  request_id
})
```

`query_bubble` 表示沿用标准 query 气泡展示，不表示作者一定是用户。宿主在持久化记录中盖章
`origin=user | host | plugin`，插件身份由 generation 绑定，不能由请求伪造；系统生成项默认只读、
可审计。Provider 适配层为了启动一次模型回合，应当把这类输入投影成普通 `user` role：对模型
而言它就是驱动下一回合的 query，不需要额外的“系统唤醒消息”协议。这里的 `user` 是 Provider
协议角色，不是声称真人亲手发送；产品数据里的可信来源仍是插件，前端也只能使用经过区分的
展示摘要，不能用完整内部 Prompt 冒充用户原文。宿主负责幂等、持久化、执行租约、排队、取消
和恢复，并保证真实用户工作优先；插件只决定何时投递、模型看什么以及用户看到什么。
Subagent 的完成通知可以把完整交接内容作为模型输入，只把“子任务 A 已更新”显示在气泡里；后台
插件可以把续跑提示作为模型输入，只显示“持续推进中”。前端只接收经过区分的展示摘要和
来源元数据，不直接拿完整内部 Prompt 生成气泡。这不需要两条产品专用唤醒链路。

这条能力也不是 Automation 的变相专用接口。任何插件只要需要把后台结果、定时触发、
审批恢复、重试结果或外部事件交还给一个持续存在的 Agent，都需要同一条“向 Session 投递普通
query”的链路。Kernel 因此保留的是 `session.send` 的可靠接纳、持久化、优先级和执行租约，
所选 Loop Driver 决定何时以及怎样消费；“定时唤醒”“自动续跑”或“子任务交付”等业务含义
完全属于插件。

### 插件自定义 Agent 编排与取消

Wuu 不规定插件必须采用哪一种 Subagent 模型。插件可以在自己的命名空间状态中维护树、DAG、
supervisor/worker、并行池、审议流程或完全不同的任务系统，也可以只把 Session 当作一个模型执行
端点。`ParentSessionID` 是来源、上下文和列表筛选信息，不是宿主自动递归取消的关系。

宿主给每次插件 Tool 调用提供当前 `TurnID`，并给插件提供产品中立的 Turn 中断通知。插件可以
用这个身份把中断信号转发给自己创建的某个或多个 child Turn，也可以让某些工作脱离当前 Turn
继续运行。取消指定 child 时应使用 `session.send` 返回的准确 `TurnID`；尚未开始的投递使用
`QueueID`。Session 本身不会因为 Turn 被取消而销毁，后续仍可通过 `session.send` 唤醒。

这是一种可信代码之间的协作式合同：Kernel 取消自己拥有的 Turn、保证终态和迟到结果不变量，插件
负责信号转发、内部请求/进程清理、重试、汇聚、恢复和卸载处理。Kernel 不强制插件 ACK，不递归
解释插件私有任务图，也不提供强杀、沙箱或通用工作流管理器。用户停止时，能被保证停止的是插件
选择传播的当前 Turn；插件有意脱离的后台工作由插件自己管理。

### 从产品需求提炼公共能力，而不是公开产品内部

一方产品迁移会暴露插件平台的缺口，但不能把当前实现直接翻译成 `host.memory.*`、
`host.automation.*` 或 `host.collaboration.*` 一类低频接口。新增公共能力时遵循以下顺序：

1. 先把一个产品作为完整纵向切片迁移，明确它自己的领域逻辑、状态和界面；
2. 到真实调用点时，判断缺少的是插件自己可以实现的逻辑，还是必须由宿主仲裁的通用原语；
3. 只有涉及宿主所有权、跨进程安全、共享资源或生命周期完整性时，才增加宿主能力；
4. 用至少另一个合理的一方或第三方场景检验抽象，避免接口只是在隐藏当前产品名；
5. 以最窄的版本化输入、输出和生命周期合同发布，不暴露私有 ThreadItem、React state、
   某个具体 Loop Driver 的私有回调或内部存储结构。

“通用”不等于预先建设一套万能框架。真实迁移尚未触达的 scheduler、后台任务或资源 API 不应
凭想象加入 SDK；目前已经由多个产品证明需要的是“创建/复用 Session”“向 Session 投递输入并
选择展示方式”“订阅生命周期事件”。它们应成为产品中立的能力，而不是分别保留一套 app-server
RPC。

当前代码给出了几个明确的迁移方向：

| 产品 | 如何由公共链路组合 | 插件应拥有 | 不应留在核心的产品接口 |
| --- | --- | --- | --- |
| Cron | Timer → 创建或复用用户可见 Session → 投递 Prompt；注册管理 Tool 和 View | Cron 表达、任务和运行记录、补跑策略、提示、Tool、Timer 与完整界面 | `automation/create`、`AutomationRunID` 和 Turn 中的自动化分支 |
| Memory | 注册提示与文件 Tool → 在合适时机读写文件；管理 View 可调用 Agent 修改文件 | 用户、工作区和会话记忆格式，读写 Tool、安全策略、概览与修改提示、审计和设置界面 | `memory/overview`、`memory/chat`、`session_memory` 核心 Tool 和核心配置字段 |
| Dream | Timer + Memory → 插件私有 Session → 整理后通过 Memory Tool 写回 | 候选选择、整合提示、失败退避、结果状态和管理界面 | `sessionDreamScheduler` 和 StreamRunner 的产品专用 AfterTurn Hook |
| Subagent | 创建私有子 Session → fresh/fork 上下文 → 投递任务 → 完成后向父 Session 回投生成的 query | `spawn_agent` 等 Tool、任务命名、worker 策略、主动委派设置与请求 Prompt、报告和界面 | `host.child_session.request` 的 spawn/send/close/list/await/report 产品动作，以及核心 Ultra 配置、Turn 快照、CLI/API 与 Composer 控件 |
| TODO | 注册带语义 capability 的 Tool → 普通 Tool 事实进入日志 → Presenter 与 Inspector 读取公开 snapshot | Tool schema、校验、结果合同、Presenter、Inspector section 和样式 | 核心可变状态、恢复、stale reminder 和原生展示 |

表中说明公共能力模型；五个一方迁移的当前完成情况见下文。字段和方法仍必须在真实调用点形成
版本化合同。例如 memory 概览和修改不需要宿主“受约束模型任务”：它可以创建或复用一个 Session
并发送 Prompt。只有这条公共链路确实无法保护宿主不变量时，才继续提炼更底层能力。

### Plugin Kernel 与可替换策略的边界

小 Kernel 不等于零能力宿主。以下是所有 Driver 和产品插件共同依赖的机制，继续由 Wuu 仲裁：

- Provider 消息顺序、Tool call/result 配对、流式响应和上下文窗口等协议正确性；
- Session/Event 的追加持久化、Inbox/Outbox、幂等接纳、执行租约、当前 Turn 的取消和恢复原语；
- Tool 执行、最终权限判定、工作区边界和用户确认；
- Service/Event/Scope/Effect、依赖解析、插件安装/信任、generation 原子替换和错误隔离；
- 原生窗口、系统安全区以及宿主拥有的导航、Tab、滚动、溢出和无障碍。

Kernel 保留的是机制，不是策略。消息不能丢、同一执行权不能被两个 generation 同时持有属于
Kernel；输入作为 steer、下一 Turn 还是中断来消费，何时开启 Step、重试或停止属于 Loop Driver。
Session 日志必须可重建属于 Kernel；Turn、Round、Plan Node 或 Worker Branch 怎样解释和展示属于
Driver 与其 UI 插件。Driver 只能通过版本化 Provider、Tool、Session、Permission 和 Checkpoint
端点工作，不能直接绕过这些网关操作核心数据库或 Provider。

其他能力默认属于插件。宿主公开少量可组合 Service：模型可见 Tool、系统提示与请求上下文、请求
变换、压缩决策、Session 创建与投递、生命周期事件、generation 绑定的 runtime 调用与事件、
命名空间设置和存储。公共 Service 应组合出多个产品和多种 Loop，而不是一项端点对应一个 Wuu
功能。

每次新增插件接口都应通过四个问题：

1. 去掉 memory、automation、collaboration 等产品名后，这个能力仍然自然吗？
2. 它是否保护了只有宿主才能保护的不变量，还是仅仅替插件省了几行领域代码？
3. 一方插件和外部插件能否在相同权限与生命周期下使用它？
4. 禁用插件后，Kernel 是否仍可管理、审计和只读打开 Session；默认分发是否仍可由 bundled
   Driver 组成一个可用 Agent，而不是留下半个产品状态或产品专用循环分支？

如果答案不成立，应继续把逻辑留在插件内部，或重新寻找更底层、更高复用的能力边界。

### Loop Driver 合同与恢复原则

当前 `internal/loopdriver` 提供版本 1 的实验合同：`Descriptor`、`create/resume`、`run`、
`checkpoint`、`cancel`、`shutdown`、Kernel Gateway 和 terminal outcome。默认多轮 Tool Loop 与单轮
无 Tool Driver 都只通过该 Gateway 请求模型执行，不读取 Session 数据库或 App Server 私有对象。
每次执行把 Driver ID、版本、合同版本和 opaque checkpoint 保存到 Session；Provider 发送前还会
保存最终 provider-neutral model-input receipt，包含稳定事实序号、最终消息、Tool surface、Prompt
section 元数据和 transform 后的实际内容。崩溃留下 `running` checkpoint；消息提交后才写终态
checkpoint。Driver 或版本不匹配时继续执行会 fail closed，但历史仍可读取。

未来把 Driver 做成可安装的一方或第三方贡献时，它仍不能获得核心内部对象。完整概念合同包括：

- `start`：为绑定 Driver 的 Session 启动执行；
- `deliver`：接收 Kernel 已可靠持久化的输入 occurrence；
- `cancel`：响应宿主取消并停止派生工作；
- `checkpoint`：在稳定边界保存 Driver 自有状态；
- `resume`：从 Session 事实与版本化 checkpoint 恢复；
- `shutdown`：停止接纳新工作，等待在途调用收敛并释放 Effect。

当前绑定点是 Session checkpoint 的首次执行，不提前决定全局、Workspace 或 Session 级选择 UI。
运行中不能静默切换 Driver。Driver 缺失、被禁用或拒绝旧 checkpoint 时，Session 仍可只读打开。
fork 继承和显式派生选择属于后续选择语义；当前高速迭代阶段不承诺复杂 checkpoint 向后兼容：
新版本可以明确拒绝旧版本，但不能猜测解释或悄悄丢弃。

通用事件流保存用户/插件输入、模型输出、Tool call/result、权限、错误、取消和 checkpoint 等宿主
事实。Driver 可以追加命名空间事件来表达 Plan、Research Round、Critic、Worker 或 Workflow Stage，
并注册自己的 Presenter/View；插件缺失或渲染失败时，宿主使用通用只读 fallback，而不是让整段
历史无法打开。凡是模型可见的内容都必须能从 Session 事实重建。

### 借鉴插件运行时的通用模型，而不是绑定 TypeScript 实现

TypeScript 插件生态的运行库提供 Context、Service、typed Event、Fiber、Effect 和 Loader EntryTree。它们展示了一种
以可替换 Service、显式依赖和可回收插件生命周期装配应用的运行模型；Wuu 借鉴的是这些通用
架构概念，而不是任何特定 Agent 产品的实现或未公开设计。

这些便利部分依赖 TypeScript/Node：动态 `import`、同进程对象 Service、声明合并和浏览器
bundle 加载。Wuu 不复制这些实现细节，而是在 Go core + 独立 runtime 进程 + Electron shell 上
实现同一运行模型：

| 参考概念 | Wuu 对应模型 |
| --- | --- |
| Context | generation-bound Plugin Scope |
| Service object | 版本化 RPC Service / Host endpoint |
| typed Event | 版本化事件目录与命名空间 payload |
| Fiber | runtime 进程、Renderer 模块和贡献的统一插件实例 |
| 上下文作用域的 `effect()` | 由 Scope 记录并等待的资源、订阅、子 Session 和后台工作 |
| `inject` | Manifest/handshake 中的 `requires` / `provides` |
| Loader entry tree | 经校验的 Activation Plan 与依赖图 |

Go 生态中最接近进程插件边界的是 HashiCorp `go-plugin` 一类 subprocess + RPC 方案；`plugin` 标准库
的进程内 `.so` 机制不适合作为跨平台桌面插件基础，`dig`/`fx`/`wire` 解决依赖注入但不提供运行时
安装、卸载和 generation。Wuu 已经有自己的双工插件进程协议、fingerprint 和原子 generation，
因此无需为追求这类运行库体验把核心改写成 TypeScript。当前 runtime 进程由 Go generation 拥有并在
关闭时限内 shutdown，Desktop generation 统一拥有注册项和 cleanup 并逆序释放；Manifest 的
`requires`、`breaks`、`conflicts` 已形成简单 Activation Plan；运行时组合已收敛为
generation-scoped Service Registry，内核服务、自省与执行作用域都走同一个 provide/consume
合同。尚未形成的是跨 Go/Desktop 的单一 Scope，以及跨外部副作用的事务承诺。

### 一方高级功能的迁移结果

下面按真实分发中的高级功能记录已经完成的纵向切片。验收依据是业务状态、Prompt、Tool、后台
链路和 Desktop 展示都由插件拥有，而不只是把代码移动到 `plugins/`：

1. **Goal 已从产品中移除。** Goal 能力和它的 bundled 插件直接删除，没有迁移成插件；自动续跑
   不再是产品功能。其余插件自行观察 `agent.turn.completed`，并通过 `host.session.send` 投递。
2. **Subagent 已迁移到公共 Session 链路。** 插件通过 `host.session.create/send/list/cancel` 创建和
   管理私有子 Session，用插件存储维护任务名与交付状态，观察 owner-scoped Turn lifecycle 后再向
   父 Session 回投只读 query。fresh/fork、共享目录/worktree、模型别名、所有权、取消与最终输出
   都是产品中立的 Session 合同；`host.child_session.request` 及其
   `spawn/send/close/list/await/report` switch 已删除。现有 `agentcontrol` 的租约和恢复代码仍可服务
   核心内部执行，但不再是 Subagent 插件的公开或私有调用入口。
   一方插件另外观察通用 Turn 中断信号，把父 Turn 的中断转发给它为该次调用创建的当前 child
   Turn；child Session 保留，下一轮仍可 `send_message`。这只是该插件的策略，不是 Core 根据
   `ParentSessionID` 建立的取消树。外部插件可以在相同 `TurnID`、中断事件和精确取消合同上实现
   任意自己的拓扑与传播规则。
   主动委派同样属于 Subagent 插件：插件用命名空间 storage 保存开关，通过
   `agent.pre_step` 在开关状态变化后的下一次模型步骤追加带来源的持久消息，并在
   `composer.toolbar` 注册自己的控件。核心不再保存 `agent.ultra_mode`，不再做 Turn 快照或
   注入委派政策，也不再暴露 Ultra app-server/CLI/IPC/原生 Composer 状态。
3. **通用 Session create/send 已取代 `host.turn.submit`。** 创建与投递是两个独立调用；创建持久化
   generation 绑定的 owner、`user | plugin` 可见性、parent、`fresh | fork` 和幂等 request id，
   投递则分离模型输入与 query 气泡摘要，并持久化真实插件来源、cause 和只读属性。Provider 仍按
   普通 user role 执行，私有 Session 不进入普通列表与搜索，真实用户排队工作优先于插件唤醒。
4. **HelpMe 已完整删除。** Tool、Schema、Prompt、内部 worker type、历史重写/压缩特判、桌面文案、
   测试和死代码均已移除；没有保留兼容入口，也没有把它重命名成另一种插件工作流。
5. **TODO 使用语义 Tool 事实链路。** bundled 插件拥有 Tool schema、参数校验、结果合同、
   Tool Activity Presenter、Inspector section 和样式。核心只把普通 Tool call/result 写入 Session
   日志，并按 `display.capability = "todo"` 投影事件和公开 snapshot；核心可变状态、恢复、
   stale reminder、原生 TODO section 与工具名特判均不存在，也没有兼容适配器。
6. **Automation 已迁移到公共 Session 链路。** 一方插件拥有 Cron 表达式、Timer、补跑、任务与
   运行记录、Prompt、`cron` Tool 和完整桌面 View；触发时只调用 `host.session.create/send`，并通过
   通用 Turn lifecycle 收敛运行状态。核心的 Automation RPC、Manager、scheduler、Turn 特判、
   原生页面、IPC 与 Tool 展示特判已经删除；generation shutdown 会先停止插件后台 Timer。
7. **Memory 已完整迁移到公共插件链路。** 一方插件拥有用户笔记本、工作区 `project_memory`、
   会话 `summary/checkpoint/notes` 的文件布局、安全过滤、`memory_*`/`session_memory` Tool、系统提示、
   概览/管理私有 Session 和完整桌面 View。核心不再读取用户 `MEMORY.md`，不再自动注入会话记忆，
   也不再提供 Memory RPC、原生页面、IPC、核心 Tool/capability、启停配置或专用模型角色。宿主只把
   已解析的 `workspace_state_dir` 作为通用初始化上下文交给插件，并继续拥有普通 Session/Turn
   持久化。旧 `memory` 指令发现配置只在加载边界迁移为产品中立的 `instructions` 配置。
   普通核心文件 Tool 也不再放行用户 Memory 或整个 `WUU_HOME`；命名 Agent 的身份笔记本属于
   暂不迁移的协作域，只在该 Agent 的显式文件范围内开放，不能据此扩张成 `host.memory.*`。
8. **Dream 已迁移到公共 Session 链路。** 一方插件观察标准 Turn 完成事件，用插件存储维护候选、
   Timer、间隔、失败退避和运行状态，再通过 `host.session.create/send` 创建 fork 私有 Session；Prompt
   和设置 View 也由插件拥有，并让该 Session 通过 Memory 插件提供的 `session_memory` Tool 写回。
   核心 `sessionDreamScheduler`、Dream 状态/锁、AfterTurn Hook、配置字段和原生设置已经删除。
9. **Desktop 生命周期已由真实一方模块证明。** Subagent、Automation、Memory、Dream、TODO 的
   bundled `desktop.js` 直接运行在 `WorkbenchController`/`PluginHost` 产品路径；View、Slot、导航、
   设置入口、Locale 和 Style 随 generation 原子激活与替换，禁用后全部撤回。测试不使用伪造的
   注册清单来代替模块执行。

### 改造顺序与完成标准

改造应复用已有可靠执行能力，先换公共边界，再删除专用入口：

1. 以现有 Turn submission 为基础定义通用 Session create/send/lifecycle；补齐 owner、visibility、
   parent、fresh/fork、workspace、来源、展示摘要和 request id，统一用户工作优先及幂等规则。
2. Subagent 已使用公共 API 创建和管理私有子 Session；`host.child_session.request` 已删除，任务
   提示、状态、桌面状态条和父 Session 回投由插件拥有。宿主不再识别 `spawn_agent` Tool 名、解析
   `<subagent_notification>`、生成专用 Tool item 或维护原生子任务面板；插件生成的回投只依赖通用
   `display_content/origin/cause/read_only` query 元数据，不改变公共合同。
3. HelpMe 全链路已删除；TODO 作为 bundled 插件运行，核心没有对应 Tool、状态、恢复和原生展示分支。
4. Cron、Memory 和 Dream 已分别完成“插件 Timer → 用户可见 Session”、“Prompt + Tool + 私有
   Session + View”和“Timer + Memory Tool + 插件私有 Session”的纵向切片，三者没有产品专用
   宿主服务。
5. 每项迁移都必须验证插件禁用、升级和卸载后不再唤醒、不残留 UI、Prompt、Tool、订阅或后台
   generation；核心删除旧协议、死代码和只为旧边界存在的测试。
6. Go runtime 和 Desktop 注册项已分别收敛到 generation owner，Manifest 已支持简单依赖与冲突；
   运行时平面的组合已收敛为 generation-scoped Service Registry：内核服务、自省与执行作用域均
   经同一注册入口（`host.service.call`），不再另造平行的固定宿主服务表。
7. 现有执行循环已包装成 Experimental v1 `DefaultDriver`，产品行为不变；Session 持久化 Driver
   身份、checkpoint 与最终 model-input receipt，恢复只从稳定边界进行。

插件链路的验收不是接口存在，而是：核心不存在 Cron、Memory、Dream、Subagent、TODO 的
产品专用执行或可变状态分支时，这五个一方插件仍能仅通过公开合同保持现有体验；外部插件在相同
权限和生命周期下也能组合出同类能力。

## 一个插件包，四类贡献

同一个插件包可以只包含一种贡献，也可以同时包含多种贡献。

| 层 | 贡献内容 | 运行位置 | 典型用途 |
| --- | --- | --- | --- |
| 声明层 | 主题、设置、入口、权限和元数据 | Manifest + 宿主 | 主题、开关、左侧入口、右侧工具、设置页 |
| Agent 层 | Tool、版本化 capability、请求变换、系统提示、压缩策略 | 独立 runtime 进程 | 搜索、策略控制、memory、上下文处理 |
| Workbench 层 | View、命令、状态项、Presenter、Slot、Surface | Electron Renderer | 协作页面、审查工具、消息呈现、复杂设置 |
| 外观层 | 主题 Token、UI Kit 样式、公开语义锚点 | Renderer CSS | 完整换肤、密度、字体、材质和控件视觉 |

四层可以组合在同一个插件包里。例如未来的协作插件可以同时注册 Agent 工具、左侧入口、
工作区 View、设置项和命令；另一个 Manga 外观插件只提供主题和样式。两者安装后自然叠加。

## 宿主、功能插件与外观插件的责任

| 参与方 | 拥有什么 | 不应该做什么 |
| --- | --- | --- |
| Wuu 宿主 | 窗口安全区、左右侧栏、标题栏、Tab、面板、滚动、溢出、持久化、无障碍和恢复路径 | 为某个插件写产品特判 |
| 功能插件 | 业务能力、内容、命令、设置定义、入口意图和插件内部状态 | 重画宿主导航、自己排列系统 Tab、依赖当前主题 |
| 外观插件 | 颜色、字体、边框、圆角、阴影、材质、有限密度和语义控件状态 | 移动交通灯安全区、改写私有 DOM、按功能插件 ID 逐个适配 |

核心规则是：**谁拥有布局，谁控制间距节奏。** 宿主容器决定页面边距、Tab 尺寸、系统安全
距离和大区域排列；插件在分配给自己的内容区里组织功能。功能插件使用 UI Kit 后，其公共
界面自动继承当前外观。

## 声明式优先，代码是逐级开放的逃生口

开发者应使用能表达需求的最窄接口。越靠下的接口自由度越高，兼容成本和信任成本也越高。

| 需求 | 优先使用 | 用户在哪里看到 | 宿主保留的控制 |
| --- | --- | --- | --- |
| 开关、文本、数字、枚举设置 | `contributes.settings` | 设置页的插件分组 | 表单布局、存储、校验和主题 |
| 左侧主功能入口 | `contributes.navigation` + View | 左侧栏可滚动的插件分组 | 选中态、排序基础和滚动 |
| 右侧上下文工具 | `contributes.workspaceTools` + View | 右侧工具选择器和原生工作区 Tab | 打开、关闭、Tab、面板宽度和持久化 |
| 复杂插件设置 | `contributes.settingsPages` + View | 设置页的插件分组 | 设置导航、页面框架和滚动 |
| 自有工作区页面 | `registerViewType` | 宿主管理的工作区区域 | View 生命周期、位置和持久化 |
| 普通插件界面 | `api.ui` | View 或自定义设置页内部 | 公共组件节奏和主题兼容 |
| 在已有区域追加少量内容 | `registerSlot` | 固定的宿主插槽 | 原生内容和插槽排列 |
| 改变消息、Composer、导航等产品概念 | `registerPresenter` | 对应语义组件 | 版本化快照、允许的动作和失败回退 |
| 包装或替换一个完整大区域 | `registerSurface` | Sidebar、Settings、Catalog 等 Surface | 边界外布局、故障隔离和恢复入口 |
| 强风格化装饰 | 主题 Token，必要时 `registerStyle` | 整个桌面 | 公开语义合同和结构安全 |

不要因为需要一个按钮就替换整个设置页，也不要因为需要全局换肤就给每一层容器发明一个
锚点。接口粒度应对应真实的视觉或功能责任边界。

## 功能入口由宿主管理

插件声明的是放置意图，不是绝对坐标：

- `navigation` 适合高频主功能，例如协作、项目管理或会话类功能；
- `workspaceTools` 适合文件、审查、终端、浏览器等上下文工具；
- `settingsPages` 适合标准 Schema 无法表达的复杂设置；
- 不需要常驻 UI 的插件可以只贡献 Tool、Hook、命令或后台能力。

插件提供标题、图标、排序偏好和 View，Wuu 负责入口选中、Tab、关闭、恢复和面板生命周期。
插件不能为了“多一个入口”替换整条导航或 Tabbar。

当前左侧插件入口位于可滚动分组；右侧工具以宿主工具选择器和原生工作区 Tab 打开。完整的
用户固定、取消固定、重排和“更多”溢出菜单仍是后续能力，见[当前边界](#当前边界与尚未完成的能力)。

## 设置页怎样与外观插件组合

标准设置使用 `contributes.settings`。宿主生成导航和控件，并按插件命名空间存储值。复杂设置
可以注册自定义 View，再通过 `contributes.settingsPages` 进入同一个设置导航。

无论设置来自 Wuu、插件 A 还是插件 B，外观插件都通过同一套设置语义和 UI Kit 作用于它们：

1. 功能插件只描述字段或渲染使用 UI Kit 的内容；
2. Wuu 提供设置页框架、页面宽度、滚动和公共控件；
3. 外观插件提供颜色、字体、边框、圆角和材质；
4. 三方不需要互相知道 ID，也不需要配对发布。

如果一个自定义设置页完全自绘，它仍会继承公开基础 Token，但只有使用 UI Kit 的区域才能获得
完整的公共组件兼容。画布、终端、Webview 和专用预览属于明确的主题边界。

## UI Kit 的覆盖范围

`api.ui` 当前提供 `Page`、`Panel`、`Card`、`Section`、`Stack`、`Row`、`Button`、
`ToolbarToggle`、`TextInput`、`TextArea`、`Checkbox`、`EmptyState`、`LoadingState`、`ErrorState`、
`LiveDuration` 和 `ComposerDrawer`。
`ComposerDrawer` 为 `composer.above` 提供共用的抽屉外壳，统一标题栏尺寸、滚动和 Escape 收起行为。
传入受控的 `expanded`、`onExpandedChange`、可访问标签 `toggleLabel`、摘要 `summary`，以及可选的
`icon`、`actions`、`notice` 和 `children`。`notice` 在收起时仍显示。
`tone` 支持 `default`、`muted`、`warning`；空状态是否显示和功能行为由插件决定。
`Page` 统一密度和响应式间距，三种状态组件统一 ARIA、焦点、错误和加载行为。它的目的有三个：

- 收敛页面、卡片、行和控件的公共节奏；
- 让功能插件自动继承任意兼容外观插件；
- 避免外观插件追踪每个功能插件的私有 class。

复杂 View 仍可使用 React 和自己的组件，只需把 UI Kit 用在
适合统一的公共区域，并明确自绘区域的主题边界。UI Kit 的组件集合会由真实插件需求推动扩展，
不会预先复制一套完整组件库。

## 外观合同：Token、组件和语义锚点

外观能力分三层，优先级从上到下：

1. **主题 Token**：语义颜色、字体、间距密度、圆角、边框、阴影、动效、内容宽度和语法色。
   注册表位于 `config/desktop-theme-contract.json`，并生成 Manifest、SDK 和桌面校验代码。
2. **UI Kit**：功能插件使用的宿主公共组件。外观插件修改 Token 后，这些组件自动变化。
3. **粗粒度语义锚点**：`data-wuu-component`、`data-wuu-layer`、`data-wuu-state` 和变体属性，
   用于 Token 无法表达的结构化装饰，例如消息气泡、面板、Tab 选中态或浮层。

锚点按视觉责任边界划分，通常到页面、面板、卡片、行、复合控件就停止。外观插件可以改变
Tab 的整体材质和选中 indicator，但不能拆开 Tab 与关闭按钮重新排列；可以统一装饰标题栏按钮，
但不能缩小 macOS 交通灯安全区。

可信桌面代码可以通过 `registerStyle` 注册任意 CSS，但私有 class、DOM 层级和偶然结构不属于
兼容合同。Manga Studio 的作用是压力测试公开合同，不是要求宿主为 Manga 增加特判。
Tool Card Skin 则只消费版本化 `ToolActivitySnapshot` 和原生 fallback，为 `command.bash` 注册
替换 Presenter；它不解析参数，也不读取任何 Driver 或产品插件私有状态。两个示例可以同时启用，
分别验证 Theme/Token/snippet 与 Presenter 的独立组合。

## Agent 能力与闭环

Agent 插件运行在 Wuu 管理的独立进程中，通过版本化协议注册能力。当前公共能力覆盖：

- 注册模型可见的 Tool；
- 通过 `agent.system_prompt.section` 贡献系统提示片段；
- 通过 `agent.pre_step` 在模型步骤前追加带来源、可持久化的隐藏消息；
- 通过 `agent.request.transform` 读取版本化请求视图并返回受校验的窄 patch；
- 通过 Experimental `agent.compaction` 替换摘要压缩结果；
- 通过 `agent.turn.completed` 和 owner-scoped `agent.turn.lifecycle` 观察 Turn；
- 通过 `plugin.client.request` 处理插件命名空间内的 Desktop/客户端请求。

实验能力已经包括进程内 `LoopDriver` 注入，但尚未开放 Manifest 注册或用户选择。Driver 不获得
私有 Go `Session`、数据库或 App Server 对象，只通过 Kernel Gateway、版本化输入和 Checkpoint
运行。普通功能插件不应通过修改默认 Loop 私有回调获得能力。

工具通过 initialize result 的 `tools` 注册，不是 capability。每个 capability 只可声明宿主已实现的
`observe`、`transform` 或 `decision` kind，并按稳定优先级组合；`guard` 与 `around` 不是当前公开
合同。插件包可另外通过 manifest 声明配置型 Hook，但它走 Hook 事件与命令/模型执行链路，不是
runtime capability。工具或能力出错时，宿主按公开错误策略传播、隔离或回退，不能靠吞掉异常
维持表面成功。

增加一个 LLM 可见能力时必须闭合两端：实现注册与执行路径，也要让提示或公开说明告诉模型
何时使用；同时保持消息顺序、Tool call/result 配对等 Provider 协议不变量。

## Generation：安装、激活、替换和卸载的原子单位

插件所有可执行贡献都属于一个 generation：

1. Wuu 读取 Manifest、记录来源身份，并确认插件在当前 workspace 中已启用；
2. 候选 generation 在外部构建和注册，尚未对用户可见；
3. 校验成功后一次性替换旧 generation；
4. 校验或激活失败时，旧 generation 保持运行；
5. 禁用、升级、卸载或开发热重载时，旧 generation 的 View、样式、命令、事件订阅和 runtime
   一起释放。

目标 Plugin Scope 会把 generation 内每个 Service、Event 订阅、Timer、子 Session、后台任务、
Renderer 贡献和子 Scope 都登记为 Effect。候选依赖满足并进入 ACTIVE 后才能对用户可见；关闭时
先停止接纳新工作，再等待在途 Effect 收敛并逆序释放。一个插件提供的 Agent Service 与 Desktop
View 必须来自同一份 Activation Plan，不能分别依赖偶然加载顺序。

React 组件可以在 generation 激活后订阅宿主事件，组件卸载或 generation 被替换时订阅会清理。
重复的同类诊断按 generation 去重；同一 generation 重新成功激活后，陈旧诊断被清除。

桌面 Slot、Presenter 和 Surface 都有局部错误边界。一个贡献渲染失败时，Wuu 隔离当前边界并
保留原生 fallback，不让整个 Renderer 白屏。插件管理、设置、禁用和恢复默认界面始终可达。

## Service Registry：唯一的组合原语

运行时平面的组合不再是一张固定的宿主中介表：插件与内核把能力以稳定名称 + 版本发布为
Service，其他插件按名称 + 主版本消费，所有调用由宿主经 `host.service.call` 网关按注册表
路由和校验。注册表是唯一的组合原语，合同、清单与自省接口都是它的构建产物；每个注册都随
generation 发布与撤销，不另设平行生命周期。

- **内核是第一个注册者，不是特例**：Storage、Settings、Session、执行作用域
  （`execution.update`）与注册表自省（`registry.introspect`）都作为 kernel services 注册，
  宿主与第三方走同一个 provide/consume 合同；
- **声明是调用权限的唯一来源**：消费方声明 `required_services` 才获得 authority，没有依赖
  求解器——要求未满足时该消费方激活失败并给出诊断，不静默降级；
- **调用在 wire 上被验证**：未注册或未授权的服务调不通；服务方收到的 caller 由宿主认证，
  不能伪造；
- **可自省**：`registry.introspect` 让程序查询注册表现状（服务、版本、提供方、generation），
  自进化工具与诊断共用这一入口。

执行作用域是注册表的第一个内核 Service 消费者：每次 tool/capability 分发获得唯一
`execution_id`，`execution.update` 报告进度、`execution.cancel` 精确取消单次分发，宿主不建立
任务树。跨插件组合按同一个词汇工作：插件 B 消费插件 A 发布的版本化 Service，A 升级后 B
重解析继续工作，A 卸载后调用权限撤销、在途调用收敛为带类型的错误。

## 多插件组合与冲突

正常组合不需要仲裁：多个 Slot 按稳定顺序追加，多个 `wrap` Presenter/Surface 依次包装，
功能插件和外观插件通过语义合同正交叠加。

只有多个插件都要求 `replace` 同一语义边界时才产生互斥冲突。Wuu 使用稳定默认顺序选择一个
生效者，并在插件设置中显示候选，让用户可以明确选择。冲突选择由宿主持久化，插件不能自行
覆盖另一个插件。

一个典型验收组合是：

- 插件 A 提供标准设置；
- 插件 B 提供右侧工具；
- 协作插件提供左侧入口、自己的 Tab 和复杂页面；
- 外观插件提供完整主题。

四者不互相认识，但 A、B 和协作插件的公共 UI 都采用当前主题；禁用外观插件后功能与布局
保持完整，禁用任一功能插件后对应入口、订阅和状态干净卸载。

## 一方插件与第三方插件同构

Subagent、Automation、Memory、Dream、TODO 已经通过与第三方插件相同的 generation、capability
和公开宿主合同运行。它们各自拥有 Prompt、Tool、状态、后台策略和 Desktop 贡献，专用宿主
执行 seam 与原生产品外壳已经删除，是当前“一方/三方同构”的纵向证明。协作暂不纳入当前改造
范围，不应为了它预建接口。

判断标准是：如果一项一方功能只能修改私有循环或私有 UI 才能实现，应先确认缺少哪一个通用
能力；只有真实需求证明公共合同不足时才扩展宿主。Wuu 自己的插件是生态的第一批使用者，不是
绕过规则的例外。

## 信任与恢复边界

插件是本地安装的应用代码，不承诺沙箱：

- 声明式主题和设置不执行插件代码；
- Agent runtime 进程与 Wuu 拥有相同用户权限；
- 桌面代码在 Renderer 中运行，并可注册任意 CSS；
- 安装或启用来源就是信任决定；同一来源身份的更新延续信任，来源身份改变时重新确认；
- runtime 只获得文档化的环境变量白名单，不直接继承全部宿主环境；
- Renderer 不读取插件绝对路径，Electron 通过内容寻址的 `wuu-plugin:` URL 加载；
- CSP 不开放 `unsafe-eval`。

插件管理、安全模式、原生窗口、app-server 生命周期、崩溃恢复和默认 UI
永远由 Wuu 控制。只能安装和启用可信来源的可执行插件。

## 当前边界与尚未完成的能力

以下内容不能写成已经完成的兼容承诺：

- 还没有 previous/current 的 SDK 与宿主兼容矩阵；插件发布时应声明
  `minimum_wuu_version`，Wuu 升级后重新验证；
- 左侧入口已有滚动，右侧 Tab 已有溢出，但用户固定、取消固定、重排和统一“更多”菜单尚未完成；
- 右侧工具和设置页已有声明式入口，通用底部面板贡献仍未形成稳定公开合同；
- UI Kit 仍然很小，结构化表单、多行输入、列表等应由真实插件需求继续收敛；
- 部分 Presenter 的 `replace` 快照和 Action 还不足以无损重建完整原生语义，优先使用 `wrap`；
- 画布、终端、Webview、PDF ShadowRoot 和专用预览仍是明确的主题边界；
- Marketplace、远程自动更新、排名、依赖解析和签名分发不属于当前本地优先平台；
- Subagent、Automation、用户/工作区/会话 Memory、Dream 和 TODO 已完成纵向迁移，并去除专用宿主执行 seam；
- HelpMe 和 Goal 已从代码和产品中删除；TODO 没有核心 Tool、状态、恢复与原生展示；
- Go runtime 与 Desktop contribution 已按 generation 统一回收，简单 Activation Plan、Default
  Driver、checkpoint 和 model-input receipt 已实现；Service Registry
  （内核服务 + 自省 + 执行作用域）已作为运行时组合原语落地；跨 Go/Desktop 的统一
  Plugin Scope、Driver Manifest/选择 UI 仍未完成。

新能力应由真实插件案例驱动，先确定责任属于宿主、功能插件还是外观插件，再选择最窄的公开合同。

## 开发和验收原则

开发插件时应验证真实组合：

1. 功能插件和外观插件同时启用；
2. 标准设置、自定义设置、左侧入口和右侧工具都能发现；
3. 禁用外观插件后布局与功能不变；
4. 禁用或更新功能插件后贡献干净卸载；
5. 失败 generation 不替换当前可用版本；
6. 强风格主题不会破坏交通灯安全区、Tab、滚动、关闭和无障碍；
7. 实现主要依赖 Token、UI Kit 和少量语义锚点。

仓库中的示例各自承担不同作用：

- [`examples/plugins/developer-loop`](../../../examples/plugins/developer-loop/)：公开 SDK、runtime、
  View、入口、设置、存储和 generation 开发闭环；
- [`examples/plugins/manga-studio`](../../../examples/plugins/manga-studio/)：强风格外观压力测试，
  验证原生界面与插件界面能否统一换肤；
- [`examples/plugins/tool-card-skin`](../../../examples/plugins/tool-card-skin/)：只依赖公开 Tool
  snapshot 与 fallback 的 Tool card Presenter，不理解具体 Loop 私有状态；
- [`examples/plugins/deep-ui`](../../../examples/plugins/deep-ui/)：Surface wrapper 和声明式主题的
  最小示例。

最终判断只有一句：**功能可以自由扩展，外观可以整体换肤，多插件可以自然叠加，宿主界面仍然稳定。**
