# Agent 插件快速上手

从一个 TypeScript 文件和 Wuu CLI 开始。此路径需要支持 `--experimental-transform-types` 的 Node.js 22.7 或更高版本。Wuu 提供匹配的官方 TypeScript SDK，不需要源码检出、npm 安装、manifest 或构建命令。

## 运行一个文件

将以下内容保存为 `hello.ts`：

```ts
import type { RuntimePlugin } from "@wuu/plugin-sdk";

export default {
  initialize() {
    return {
      tools: [{
        id: "greet",
        description: "Return a friendly greeting",
        input_schema: { type: "object", properties: {}, additionalProperties: false },
        activity: { read_only: true, concurrency_safe: true, risk: "low" },
      }],
    };
  },
  executeTool() {
    return { result: { content: [{ type: "text", text: "Hello from my plugin!" }] } };
  },
} satisfies RuntimePlugin;
```

执行：

```bash
wuu plugin dev ./hello.ts
```

此操作授权这个具体文件并监听变化。默认导出遵循公开的 `RuntimePlugin` 契约。Wuu 保存文件快照，检查语法、导入和默认导出，不调用 `initialize`，再发布独立快照的开发 generation。修改问候语并保存即可重载。语法、导入或导出失败时保留上次发布的 generation；修复并保存后会自动重试，包括首次加载失败的情况。正在执行轮次时仍可发布；该轮次及其未完成工作者或受管进程继续使用已有 generation。这些工作结束后，会话在下一次空闲轮次边界接入更新。实际宿主随后提供读取阶段服务并初始化候选。准备失败保留旧运行时；原生激活在运行时变更提交后进行。此后若激活失败，新 generation 仍保持发布并记录失败诊断，不会回滚。发布成功不等于每个已打开会话都已激活新版本，请检查实际状态及 [generation 契约](plugin-system.md#运行时-generation)。

请使用普通 `.ts` 文件，不支持符号链接。单文件路径仅支持 Node 内置模块和 `@wuu/plugin-sdk` 导入。其他本地文件、npm 依赖、JSX 或自定义 `tsconfig` 转换需要下方的插件包流程。直接执行 TypeScript 不进行类型检查。stdout 保留给协议，诊断信息使用 `console.error`。导入模块会执行顶层代码，验证期间也一样。开发授权表示信任这个来源，不会沙箱化代码，也不代表已批准分发包。

源码变化经过防抖，每两秒核对一次，即使文件监听器出错也会继续核对。`--poll 500ms` 可修改这个必须为正数的间隔；`--watch=false` 发布一次后退出。Ctrl+C 停止监听，已发布的开发 generation 仍然可用。可在 Skills & Plugins 中禁用，或使用命令打印的 ID 执行 `wuu plugin disable <id>`。

## 让 Agent 添加工具

在使用 Wuu 引擎的会话中，延迟发现工具 `plugin_manager` 提供限定范围的开发流程：

1. 用 `write_file` 将 `hello.ts` 保存到会话工作区。
2. 发现 `plugin_manager` 后，调用 `{"action":"apply","path":"./hello.ts"}`，也可以传入插件目录。Apply 与 `wuu plugin dev --watch=false` 一样只发布一次，不启动监听器。
3. 检查结果，然后结束当前轮次。该轮次的工具定义仍绑定旧 generation。
4. 在下一次空闲轮次中发现并调用新的问候工具。用 `{"action":"status"}` 或带准确 `id` 的 status 比较已发布状态与当前会话的 generation。确认实际工具结果后，才能判断它可用。

当前会话接入更新前，未完成的会话工作者和受管进程必须先结束。不要让当前轮次持有持续运行的 `wuu plugin dev` 监听器；请使用上面的一次性 apply 流程。

Apply 可能在宿主上执行模块顶层代码和包构建脚本。在验证或导入之前，必须使用 `unconfined` 会话，或在 `standard` 模式下由已启用并配置的权限审核器返回允许决定。只读模式拒绝变更。工作区写入权限本身不授权宿主代码执行。可执行程序和插件存储由宿主选择；该工具不授予对 `$WUU_HOME` 的通用文件系统访问权限。不支持远程执行环境中的源码路径。

`{"action":"disable","id":"<plugin-id>"}` 和 `{"action":"enable","id":"<plugin-id>"}` 修改已有插件的启用状态；启用不会补充缺失的信任。禁用提交成功后，无需等待下一次空闲轮次，就会撤销各个仍被引用 generation 中的插件。应用更新不会自动开始下一轮。已发布或已安装、当前会话可调用工具、桌面实际渲染是不同的检查。包含桌面贡献时，还应检查实际界面与运行时状态。

## 扩展为插件包

需要多个源文件、桌面贡献或分发时，使用插件包。以下示例为内置 Wuu 引擎添加 `greet` 工具，使用与 CLI 匹配的 Wuu 源码目录获取 SDK。

## 准备 SDK 和插件包

使用源码目录中的 SDK，不依赖 npm registry 是否已发布对应版本。将第一行改成源码目录的绝对路径，然后在准备存放插件项目的目录中执行：

```bash
WUU_SOURCE=/absolute/path/to/wuu
npm ci --prefix "$WUU_SOURCE/packages/plugin-sdk"
npm run build --prefix "$WUU_SOURCE/packages/plugin-sdk"
wuu plugin create hello-plugin
cd hello-plugin
npm pkg set "devDependencies.@wuu/plugin-sdk=file:$WUU_SOURCE/packages/plugin-sdk"
npm install
npm install --save-dev esbuild
npm pkg set 'scripts.build=tsc --noEmit && esbuild src/index.ts --bundle --platform=node --format=esm --outfile=dist/index.js'
```

生成器会创建 `plugin.json`、`package.json`、`tsconfig.json` 和 `src/index.ts`。manifest 使用 `wuu-plugin-v1` 运行时传输协议启动 `node dist/index.js`。上面的构建命令会把 SDK 打进同一个文件：Wuu 准备安装包时会排除 `node_modules`，仅做 TypeScript 编译会让安装后的包缺少运行时依赖。

## 添加工具

将 `src/index.ts` 替换为：

```ts
import { runJSONLRuntime, type RuntimePlugin } from "@wuu/plugin-sdk";

const plugin: RuntimePlugin = {
  initialize() {
    return {
      protocol_version: 3,
      tools: [{
        id: "greet",
        description: "Return a greeting for a name",
        input_schema: {
          type: "object",
          properties: { name: { type: "string" } },
          required: ["name"],
          additionalProperties: false,
        },
        activity: { read_only: true, concurrency_safe: true, risk: "low" },
      }],
    };
  },
  executeTool(params) {
    const name = (params.arguments as { name?: unknown })?.name;
    if (params.tool_id !== "greet" || typeof name !== "string" || !name.trim()) {
      return {
        result: {
          is_error: true,
          content: [{ type: "text", text: "A non-empty name is required." }],
        },
      };
    }
    return { result: { content: [{ type: "text", text: `Hello, ${name}!` }] } };
  },
};

runJSONLRuntime(plugin, { input: process.stdin, output: process.stdout }).catch(error => {
  console.error(error);
  process.exitCode = 1;
});
```

`initialize` 声明模型可见的说明、输入 schema 和调度元数据，宿主会为工具 ID 添加命名空间。`executeTool` 仍需检查输入，工具失败时返回 `is_error`。stdout 专门用于 JSONL 协议，诊断信息应写入 stderr。

## 构建并试用

```bash
npm run build
wuu plugin validate .
wuu plugin test .
wuu plugin dev .
```

`validate` 检查包结构。`test` 启动运行时，检查初始化、协议协商、能力描述和工具注册，但不会调用 `greet`，也不能证明工具在会话中的行为正确。

`dev` 授权当前目录，构建候选版本，并发布开发 generation。编辑期间保持它运行；保存后会再次构建。构建或包验证失败时，保留之前发布的 generation，并继续监听修复后的保存。运行中的工作保留原 generation，更新可同时发布，供后续空闲轮次使用。命令成功发布不等于所有桌面或运行时贡献均已成功激活，还应检查插件的实际状态。

打开使用 Wuu 引擎的会话，让它调用问候工具向某个名字打招呼，确认工具结果包含问候语。这一步验证了契约测试未覆盖的实际行为。

## 开发内置插件

在源码 checkout 中先构建一次宿主，并准备内置插件工作区：

```bash
npm ci --prefix plugins
npm --prefix plugins run build
make build-go
WUU_HOME="$PWD/.wuu-dev" ./bin/wuu plugin dev plugins/todo
```

保持最后一条命令运行，并让开发应用使用相同的 `WUU_HOME`。编辑
`plugins/todo/src/index.ts` 并保存，普通包监听器就会重建并发布候选版本。
这个经过明确授权的开发包会替换内置 `todo` 候选，但不会获得内置包的信任或
来源标记。仅修改插件 TypeScript 无需重新构建 Go；修改 Go 宿主或公开服务
仍需要重新构建。

八个内置插件均使用此流程。源码目录中的 `plugin.json` 和 `dist/` 是生成的
开发文件，应编辑源码或 `internal/plugin/bundled/<id>/` 下的规范 manifest。
只构建一个运行时可运行 `node scripts/build-bundled-plugins.mjs --plugin=todo`；
检查已提交内置运行时是否与源码一致可运行
`npm --prefix plugins run build:check`。

当前轮次及其未完成工作者或受管进程保留原来的实现和文件。这些工作结束后，
同一会话在下一次空闲边界采用新版本。准备或提交前失败时，上一代仍然可用；
提交后激活失败则留下失败的新 generation。应在接入更新后检查实际工具结果，
构建成功不代表已经激活。

在 checkout 中运行无需凭据的进程验收：

```bash
node scripts/bundled-plugins-e2e.mjs
WUU_SELF_EXTENSION_BINARY="$PWD/bin/wuu" \
  WUU_SELF_EXTENSION_EVIDENCE="$PWD/.tmp/e2e/plugin-generation" \
  go test -count=1 -run '^TestAgentSelfExtensionProcess$' ./cmd/wuu
```

第一条命令通过 JSONL 驱动实际分发的插件可执行文件，使用一次性宿主服务，
覆盖工具、持久状态、后台任务、取消和关闭。协议记录、运行时哈希和结果保存到
`.tmp/e2e/bundled-plugins/`。第二条通过本地合成模型端点驱动真实 Go app-server，
验证运行中轮次内发布、下一轮采用、保留最后可用版本，以及禁用清理，无需模型
账号。两条命令都不检查桌面贡献的实际渲染。

## 分发插件包

```bash
wuu plugin pack .
wuu plugin install ./hello-plugin-0.1.0.zip
wuu plugin approve hello-plugin
```

当前 CLI 会先暂存本地包，再由批准操作启用代码执行。开发目录授权是独立的，不会随 zip 分发。插件以你的用户权限运行，包验证不等于安全审计。禁用和删除操作见[插件管理](plugins.md)。

持久状态、服务和生命周期回调见[编写插件](plugin-authoring.md)。如果要添加界面而非模型工具，请使用[桌面插件快速上手](desktop-plugin-quickstart.md)。

各包还声明监听共享 SDK、构建脚本和规范化的插件作者资源。提示词和桌面资源修改也会触发同一热更新流程；生成的运行时文件不会被监听，避免重复构建循环。
