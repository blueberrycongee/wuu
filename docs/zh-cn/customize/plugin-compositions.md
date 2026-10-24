# 独立 agent 组合

`host.composition` 版本 1 允许受信任插件创建独立的 Go 运行时，试跑 harness 组合，再显式绑定逻辑 agent。它复用主运行时的版本化服务、依赖图、激活检查和按依赖逆序清理机制。[组合实验室示例](../../../examples/plugins/composition-lab.ts) 将这些操作公开为 agent 工具和开发者客户端能力。

```bash
wuu plugin dev examples/plugins/composition-lab.ts
```

插件在 `required_services` 中声明 `requireCompositionService()`，并用当前回调的 host 调用 `compositionService(host)`。该服务要求选择 `kernel` 模块，准备阶段不可调用。单文件 TypeScript 开发仍不要求 manifest 或构建。

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

## 执行边界与所有权

| 方法 | 行为 |
| --- | --- |
| `create({selection, paths?, root?, driver_profile?})` | 在新的状态命名空间中准备并激活候选，返回 ID、工作目录、组合选择、模块和插件包。 |
| `inspect(id)` | 查看模块状态、已绑定的 agent 和正在执行的数量。 |
| `bind(agent, id, expected)` | 原子比较并替换绑定。`expected: ""` 表示当前未绑定，`id: ""` 解除绑定。不匹配返回 `binding_conflict`。 |
| `run({id, prompt})` | 直接试跑组合，不改变选择。 |
| `run({agent, prompt})` | 捕获 agent 当前的组合，开始一次新执行。返回 `id`、`execution_id`、`content` 和可选的推理 `error`。 |
| `dispose(id)` | 解除指向该组合的绑定，停止接收新执行。已有执行尚未结束时返回 `draining: true`；最后一次执行完成后释放 provider 和临时状态。 |

绑定与执行准入之间是原子边界。切换前已开始的执行保留原有 provider 图和 checkpoint 身份。重新绑定旧 ID 只恢复后续执行的选择，不撤销已经完成的效果。需要撤销选择时，应保留旧组合。

组合 ID 和 agent 名称属于调用插件、会话及所属 generation。注册表提供身份，输入不能指定其他调用者或会话。没有会话身份的开发者客户端回调使用独立命名空间。每次 `run` 创建新的 session 和驱动绑定；此 API 不迁移已有桌面对话，也不把旧 checkpoint 交给替换后的驱动恢复。组合及绑定是临时状态，不跨所属 generation 退休或进程重启保留。

调用者取消会传播到执行。插件退休会取消尚未完成的工作并关闭其组合；generation 关闭也会清理未显式释放的组合。消费者先完成清理，随后 provider 才关闭。清理失败会报告错误，并保留临时目录用于诊断。

## 隔离边界

每个组合有独立的状态目录、内存配置 provider、服务图、插件进程及默认的空工作目录。创建时复制所属 generation 的模型和运行时配置。进程共享的模型元数据由宿主初始化一次；创建组合不会替换它或再次启动刷新。不安装到全局插件目录，也不修改其他会话的配置。只有需要共享文件时才传入 `root`；相对源码路径和显式工作目录都相对于所属运行时的工作目录解析。

扩展仍是受信任代码。这里提供生命周期和状态隔离，不是操作系统安全沙箱。显式共享的目录、系统凭据、配置的外部服务，以及插件主动执行的效果仍可能共享。持久化 provider 替换和 checkpoint 迁移需要兼容协议或停止写入后的迁移；临时组合不会自动执行这些迁移。
