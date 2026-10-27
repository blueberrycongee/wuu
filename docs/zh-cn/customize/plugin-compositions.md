# Agent 组合

固定任务、独立评分、进程隔离和结果保留见[本地 Harness 实验](harness-experiments.md)。单独运行一个新组合不等于定义了 benchmark。

`host.composition` 版本 1 允许受信任插件创建独立的 Go 运行时，试跑 harness 组合，再显式绑定逻辑 agent。它复用主运行时的版本化服务、依赖图、激活检查和按依赖逆序清理机制。[组合实验室示例](../../../examples/plugins/composition-lab.ts) 将这些操作公开为 agent 工具和开发者客户端能力。

```bash
wuu plugin dev examples/plugins/composition-lab.ts
```

插件在 `required_services` 中声明 `requireCompositionService()`，并用当前回调的 host 调用 `compositionService(host)`。该服务要求选择 `kernel` 模块，准备阶段不可调用。单文件 TypeScript 开发仍不要求 manifest 或构建。

## 配置升级

组合版本 1–3 仍可读取。版本 4 将资源授权设为显式 provider 边界，并引入最小会话宿主。
旧选择包含 `kernel`、`wuu.session-state` 或 `wuu.session-execution` 时，解析会补入
`wuu.resource-access`；未绑定其他 `resource.authorization@1` provider 时，还会补入
`wuu.resource-policy.default`。检查结果用 `migration:vN-to-v4` 标注这些新增项，
不会改写源配置。显式排除仍然有效，空选择仍为空。

版本 3 保留原有产品目录：不会悄悄加入新的可选共享 provider，也不会重新应用版本 1/2
的执行 provider 迁移。版本 4 不会补足缺失的授权组件。将旧配置改成版本 4 前，应显式
保留投影后的模块或选择替代实现。缺失或禁用的策略仍会拒绝访问；迁移不是运行时回退。

## 最小会话宿主

组合版本 4 提供 `minimal` 配置。它与 `headless`、`desktop` 共用
`wuu.session-host` bundle：内核路由适配器、配置与会话状态消费者、本地配置、
SQLite 文档、会话域语义、引擎分发，以及显式选择的所有者资源策略。
它**不启动**循环、模型适配器、工具、凭据、插件设置、产物或 trace provider、
界面和业务插件，也不需要模型连接、占位 URL 或 API key。

通过公开契约添加引擎。在源码目录中，先为隔离的 `$WUU_HOME/config.json` 写入
`{"composition":{"version":4,"profile":"minimal"},"skills":{"enabled":false}}`。
使用 `wuu plugin dev --watch=false /absolute/path/to/engine.ts` 发布
[单文件 echo 引擎](../../../examples/compositions/minimal/engine.ts)，然后把命令报告的
模块 ID 加入配置：

```json
{
  "composition": {
    "version": 4,
    "profile": "minimal",
    "patches": [{
      "modules": {"YOUR_ENGINE_MODULE_ID": true},
      "services": [{"service": "engine.wuu", "major": 1, "provider": "YOUR_ENGINE_MODULE_ID"}]
    }]
  },
  "skills": {"enabled": false}
}
```

保留发布过程写入的 `extensions` 设置。运行
`wuu app-server --config "$WUU_HOME/config.json" --workdir /your/workspace`，
通过普通的 [app-server 协议](../automation/app-server.md) 初始化、创建会话并发消息。
该开发路径不要求 manifest 或构建；`/wait` 会保持本轮运行，直到收到中断。

配置本身只是会话和状态宿主，不隐含任何引擎：未选择可用引擎时，发送会失败。
删除显式绑定的 provider 不会改用内置实现。未选择相应 provider 时，设置、凭据、
产物和模型调用也会失败。声明了设置的插件必须选择设置 provider，不会忽略缺失的
持久化设置。替换状态或授权 provider 仍使用默认产品相同的服务绑定与生命周期规则；
有状态替换仍须兼容 checkpoint，或先停止写入者再迁移。

[可重复的独立包验收](../../../examples/compositions/minimal/README.md) 通过这些公开入口
运行，并保存依赖图、协议与清理证据。它使用确定性引擎，不代表真实推理或桌面视觉验收。

## 试跑、选择与撤销

```ts
const scopes = compositionService(host);
const baseline = await scopes.create({
  selection: { version: 2, profile: "headless" },
});
const candidate = await scopes.create({
  paths: ["./my-loop"],
  selection: {
    version: 2,
    profile: "headless",
    patches: [{
      modules: { "wuu.default-loop": false, "example.my-loop": true },
      services: [{ service: "driver.default", major: 2, provider: "example.my-loop" }],
    }],
  },
});
const trial = await scopes.run({ id: candidate.id, prompt: "验证这个 harness。" });
if (trial.error) throw new Error(trial.error);

await scopes.bind("writer", baseline.id, "");
const changed = await scopes.bind("writer", candidate.id, baseline.id);
await scopes.run({ agent: "writer", prompt: "使用选定的 harness。" });
await scopes.bind("writer", changed.previous, candidate.id); // 撤销选择
await scopes.dispose(candidate.id);
await scopes.dispose(baseline.id);
```

`example.my-loop` 必须实际提供 `driver.default` 主版本 2。其他替换使用对应的公开协议，例如 `agent.prompt` 主版本 1。所需 provider 模块必须显式包含。缺失依赖、provider 歧义、激活失败或运行时能力不可用都会拒绝创建，不会改选替代实现，也不会改变已有绑定。Profile、bundle 和补丁规则见[组合配置](plugin-system.md)。

`paths` 接受目录、zip 包和单个 `.ts` 文件。安装只在新组合内授予信任。候选目录包含所属 generation 已激活的插件包，但只启动明确选择的模块。源码会被复制到执行快照，创建后修改源文件不会改变已有组合。单文件模块 ID 与 `wuu plugin dev` 一致：`local-` 加绝对源文件路径的 SHA-256 前 24 位十六进制字符。可用 Node 的 `createHash("sha256").update(resolve(path)).digest("hex").slice(0, 24)` 计算；插件目录则使用 manifest 的 ID。创建结果返回实际激活的包 ID 和指纹，便于检查。

`create({ inherit: true })` 复制父 generation 实际选择的模块和服务绑定。可通过 `selection` 覆盖模块、服务及补丁；继承不能同时选择 profile，因为 profile 会建立自己的选择。返回值中的 `parent_generation` 和 `model` 分别标明来源 generation 和实际模型。模型配置会被复制，持久状态默认隔离：

```ts
const scoped = await scopes.create({
  inherit: true,
  model: { provider: "research", model: "my-model", effort: "high" },
});
```

省略 `model` 会保持捕获的父模型选择。显式提供模型对象时，缺省字段采用所选连接的默认值。嵌入宿主提供的原生 provider 也会使用候选自己的上下文重新构造。嵌套组合遵循同一规则：父组合导入了祖先状态时，隔离的后代会在自己的上下文中重新构造选中的状态 provider；嵌套的 `state: "parent"` 组合则共享捕获的同一状态权威，并可绑定其中的产品会话。

## 产品会话

产品会话绑定仅支持原生 Wuu 引擎。外部引擎会话不能绑定组合；已绑定会话后来切换到外部引擎时，新轮次会在派发前失败，必须先显式解除组合绑定。

使用 `state: "parent"` 的组合导入父级选中的 session、document 和 artifact 服务。这项显式导入优先于状态服务的选择补丁。服务图、模型配置和插件进程仍独立。通过已有产品会话的 ID 绑定：

```ts
const candidate = await scopes.create({
  inherit: true,
  state: "parent",
  model: { provider: "research", model: "my-model" },
});
const previous = await scopes.inspectSession(sessionID);
await scopes.bindSession(sessionID, candidate.id, previous.id);
// 后续产品轮次使用 candidate；已接纳轮次保留捕获的服务图。
await scopes.bindSession(sessionID, "", candidate.id); // 恢复工作区选择
await scopes.dispose(candidate.id);
```

候选模型优先于会话保存的默认模型；会话的权限和速度选择继续生效。产品工具使用会话工作目录。已接纳的父轮次创建子会话时，子会话捕获父轮次的组合；后续修改父绑定不会重定向子会话。内置 subagent 的前台执行和后台完成通知使用同一会话契约。

旁路会话捕获正在运行的父轮次已接纳的组合；父会话空闲时则读取当前持久绑定。即使父轮次结束、改绑或释放组合，旁路会话也保留该组合直到自身完成。工具使用父会话的工作目录，原生工具集保持只读；选中服务图的步骤和请求策略同样生效。组合或显式模型不可用时，接纳失败，不会退回工作区默认实现。

后台生命周期回调返回发起请求的插件实例，即使父轮次已经结束，或父会话的下一轮已选择另一组合。已接纳的子任务与回调投递会在释放期间保留原组合，完成后再清理其私有资源。请求 ID 在共享状态权威中按插件唯一；另一组合或另一目标会话复用该 ID 会在接纳前失败。若重启后原组合不可用，终态观察仍保留在持久状态中，重放不能把它交给默认插件实例。

产品选择的权威记录保存在 `state.documents`，跨释放、generation 退休和进程重启保留。运行资源是临时的；资源缺失或父状态 generation 改变时，新轮次在推理前报错。`inspectSession` 返回 `default`、`active` 或 `unavailable`、选中 ID、选择者，以及实时组合或绑定时保存的快照。调用者可显式绑定新建组合，或以旧 ID 解除绑定。存储错误和提交结果未知会直接返回；重试写入前先查询。隔离状态的组合不能绑定产品会话。

## 执行边界与所有权

| 方法 | 行为 |
| --- | --- |
| `create({selection?, inherit?, model?, paths?, state?, root?, driver_profile?})` | 准备并激活候选，默认使用隔离状态，返回 ID、工作目录、组合选择、模块和插件包。 |
| `inspect(id)` | 查看模块状态、已绑定的 agent 和正在执行的数量。 |
| `bind(agent, id, expected)` | 原子比较并替换绑定。`expected: ""` 表示当前未绑定，`id: ""` 解除绑定。不匹配返回 `binding_conflict`。 |
| `bindSession(session, id, expected)` | 比较并替换产品会话的持久选择。 |
| `inspectSession(session)` | 查询持久选择及当前运行资源的可用性。 |
| `run({id, prompt})` | 直接试跑组合，不改变选择。 |
| `run({agent, prompt})` | 捕获 agent 当前的组合，开始一次新执行。返回 `id`、`execution_id`、`content` 和可选的推理 `error`。 |
| `dispose(id)` | 解除逻辑 agent 绑定，停止接收新执行。产品选择保留不可用的 ID。已有执行尚未结束时返回 `draining: true`；最后一次执行完成后释放 provider 和临时状态。 |

绑定与执行准入之间是原子边界。切换前已开始的执行保留原有 provider 图和 checkpoint 身份。重新绑定旧 ID 只恢复后续执行的选择，不撤销已经完成的效果。需要撤销选择时，应保留旧组合。

组合 ID 和 agent 名称属于调用插件、会话及所属 generation。注册表提供身份，输入不能指定其他调用者或会话。没有会话身份的开发者客户端回调使用独立命名空间。每次试跑 `run` 创建新的 session 和驱动绑定。产品绑定选择已有会话后续轮次的实现，不把已接纳的 checkpoint 迁给替换驱动。逻辑 agent 绑定是临时状态；产品绑定按上文规则持久保留。

调用者取消会传播到执行。插件退休会取消其组合中的主轮次、原生子 Agent 和直接模型调用；generation 关闭也会清理未显式释放的组合。显式释放允许已接纳工作排空。消费者清理和推理记账完成后，provider 才关闭。清理失败会报告错误，并保留临时目录用于诊断。

候选在插件激活前就拥有自己的组合身份。激活时可以检查捕获的状态，但只有创建流程发布就绪组合后才能接纳执行。准备失败不能把候选工作归到默认运行时。

用带有产品版本信息的构建运行 `WUU_E2E_CORE=/path/to/wuu node scripts/composition-e2e.mjs`，可验证并行组合、模型路由、原生文件工具、准备失败、绑定冲突、执行中的释放、前台子会话继承、改绑和释放期间的旁路会话、重绑定和释放期间的后台投递、资源排空及进程重启后的产品选择。请求、app-server 消息、诊断及结果保存在 `.tmp/e2e/composition/run-*`。模型响应为合成数据；该检查不证明模型收益或实际 UI 渲染。

## 隔离边界

默认 `state: "isolated"` 下，每个组合有独立的状态目录、内存配置 provider、服务图、插件进程及默认的空工作目录。创建时复制所属 generation 的模型和运行时配置。进程共享的模型元数据由宿主初始化一次；创建组合不会替换它或再次启动刷新。不安装到全局插件目录，也不修改其他会话的配置。只有需要共享文件时才传入 `root`；相对源码路径和显式工作目录都相对于所属运行时的工作目录解析。

扩展仍是受信任代码。这里提供生命周期和状态隔离，不是操作系统安全沙箱。显式共享的目录、系统凭据、配置的外部服务，以及插件主动执行的效果仍可能共享。持久化 provider 替换和 checkpoint 迁移需要兼容协议或停止写入后的迁移；临时组合不会自动执行这些迁移。

## 发现已接纳的服务图

`registry.introspect` 返回不可变运行时身份 `graph_generation`，同时保留已有的持久目录
`generation` epoch。保留中的执行和并行候选可以具有相同目录 epoch，但不共享服务图。
每个服务条目的 `descriptor` 包含完整方法 schema，以及准备和清理阶段的准入标记。
`requirements` 包含尚未满足的声明；`dependencies` 就是激活时先提供方后消费者、清理时
逆序执行的同一依赖图，其中包含服务主版本。提供方退出后不再可调用，但诊断保留依赖边。

组合的 `create` 和 `inspect` 在 `services` 中返回此快照。记录实际加载的实现时应使用
该快照，不要从更新的目录重建提供方选择，也不要将 profile 名称视为执行身份。
