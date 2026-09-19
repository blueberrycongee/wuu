# Agent 插件快速上手

本教程为内置 Wuu 引擎添加一个 `greet` 工具。插件以 Node.js 进程运行，返回文本结果。需要 Wuu CLI、Node.js 22 或更高版本，以及与 CLI 匹配的 Wuu 源码检出目录，用于获取 SDK。

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

`dev` 授权当前目录，构建候选版本，并发布开发 generation。编辑期间保持它运行；保存后会再次构建。构建或包验证失败时，保留之前发布的 generation。仍有执行占用当前 generation 时，刷新可能推迟。命令成功发布不等于所有桌面或运行时贡献均已成功激活，还应检查插件的实际状态。

打开使用 Wuu 引擎的会话，让它调用问候工具向某个名字打招呼，确认工具结果包含问候语。这一步验证了契约测试未覆盖的实际行为。

## 分发插件包

```bash
wuu plugin pack .
wuu plugin install ./hello-plugin-0.1.0.zip
wuu plugin approve hello-plugin
```

当前 CLI 会先暂存本地包，再由批准操作启用代码执行。开发目录授权是独立的，不会随 zip 分发。插件以你的用户权限运行，包验证不等于安全审计。禁用和删除操作见[插件管理](plugins.md)。

持久状态、服务和生命周期回调见[编写插件](plugin-authoring.md)。如果要添加界面而非模型工具，请使用[桌面插件快速上手](desktop-plugin-quickstart.md)。
