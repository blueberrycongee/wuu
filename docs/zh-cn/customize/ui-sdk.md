# 应用 Shell 与 TSX 直接预览

应用 Shell 负责布局、导航、会话、输入区、活动、设置和资源预览。默认 Shell
与外部 Shell 都使用 `@wuu/plugin-sdk` 的 `ApplicationServiceV1`：不可变快照、
订阅和类型化 actions。Shell 不应导入 desktop 私有源码、抓取另一个 Shell 的
DOM，或维护第二份应用状态。

## 在仓库外开始

使用与 Wuu 构建匹配的 SDK 包。维护者可以执行
`npm pack ./packages/plugin-sdk`，把生成的 tarball 交给作者。作者目录不需要
Wuu 源码 checkout：

```bash
mkdir my-research-ui
cd my-research-ui
npm install /path/to/wuu-plugin-sdk-0.1.0.tgz
```

将以下内容保存为 `panel.tsx`：

```tsx
import type { PluginGenerationApi } from '@wuu/plugin-sdk';

export function activate(api: PluginGenerationApi) {
  const React = api.react;
  const { Button, Section } = api.ui;

  function Panel() {
    const state = React.useSyncExternalStore(
      api.application.subscribe,
      api.application.getSnapshot,
    );
    return <Section title="Research">
      <p>{state.conversation.title ?? 'No thread selected'}</p>
      <Button onClick={() => {
        void api.application.invoke('thread.create', {});
      }}>New thread</Button>
    </Section>;
  }

  api.registerViewType({ id: 'panel', title: 'Research', render: Panel });
}
```

执行 `npx wuu-plugin-preview panel.tsx`，打开输出的本地回环地址。保存文件会
自动检查 TypeScript 并更新预览，无需插件 manifest、安装插件或手动构建。
`--check` 执行相同的公开类型检查但不启动服务器。`--port` 指定端口，默认选择
可用端口。传入多个源码文件可以同时加载协作插件。

组件选择器列出注册的视图和明确声明的 Shell。单独预览组件时，可导出返回 JSX
的 `preview(api)`。在 activation 或 preview 函数中定义
`const React = api.react`，并在该作用域内定义组件。这条 classic JSX 路径使用
宿主 React 实例。单文件预览支持从 `@wuu/plugin-sdk` 和 `@wuu/plugin-sdk/ui`
导入，其他依赖使用包构建路径。不要把第二个 React runtime 打包进桌面扩展。

预览使用合成应用数据和本地 actions，不发送模型请求，也不持久化设置。
`--state snapshot.json` 可在启动时加载 `ApplicationSnapshotV1` fixture。
主题、字号和浏览器窗口宽度可用于检查布局。语法与类型错误显示源码文件、行列，
并保留最后一次成功构建。激活错误保留原预览；渲染错误显示组件诊断。修复保存后
会重新加载。重载会重置组件本地状态，预览应用状态则保留。不支持的产品 API
明确报错。

需要在 Wuu 服务上直接执行 `.ts` 运行时插件时，使用
[单文件运行时开发](plugin-quickstart.md)。组件预览与生产推理、迁移、原生集成
及持久化验收是不同的验证范围。

## 注册被选中的 Shell

Shell 包在 `plugin.json` 中明确声明服务：

```json
{
  "schema_version": 1,
  "id": "research-workbench",
  "name": "Research workbench",
  "version": "1.0.0",
  "desktop": {
    "entry": "dist/shell.js",
    "services": [{
      "name": "ui.shell",
      "version": "1.0.0",
      "methods": [{
        "name": "describe",
        "input_schema": "ui.shell.describe.input.v1",
        "output_schema": "ui.shell.describe.output.v1"
      }]
    }]
  }
}
```

模块调用 `api.registerApplicationShell({ id: 'ui.shell', render })`。
`render` 接收 `{ application, slots }`。配置组合通过插件 ID、provider ID 与
generation 选择一个提供者。选中提供者缺失、禁用或失败时，应显示诊断，不能
暗中换回另一个 Shell。直接预览时导出
`descriptor = { id: 'research-workbench', services: [APPLICATION_SHELL_SERVICE] }`，
用明确声明区分 Shell 和普通组件文件。

`application.getSnapshot()` 在 revision 改变前保持对象引用不变。使用
`api.react.useSyncExternalStore(application.subscribe,
application.getSnapshot)` 渲染快照。快照包括工作区、导航、会话条目与活动、
输入区状态、设置摘要、外观、预览和状态，不包含凭据。`thread.select`、
`thread.create`、`composer.set-draft`、`composer.submit`、`composer.stop`、
`appearance.set`、`preview.open` 等 actions 检查输入类型。向用户显示 action
拒绝的错误，不要自动重试发送。消费者 generation 被释放后，不能发起新 action
或接收迟到结果。

## 嵌套插槽与第三个插件

Shell 声明自己的插槽，并挂载公开组件：

```tsx
api.registerUISlot({ id: 'evidence', cardinality: 'many' });
// Inside the shell component:
const Slot = slots.Slot;
return <Slot name="research-workbench/evidence"
  context={{ threadId: application.getSnapshot().conversation.threadId }} />;
```

另一个插件调用 `api.registerUIContribution`，提供本地 `id`、限定命名空间的
`target` 和类型化组件。组件得到自己 generation 绑定的 `application`、`slots`
及插槽所有者传入的 `context`。贡献组件还可以声明并挂载下一层插槽。本地 ID
属于注册插件。多个提供者占用 `one` 插槽时产生冲突；`many` 按 order 与稳定
身份排序。循环嵌套明确失败。移除贡献或卸载 generation 会移除其 UI；卸载插槽
所有者会移除对应挂载点。释放旧 generation 不能删除后继者的注册。

完整示例是[研究工作台](../../../examples/plugins/research-workbench/shell.tsx)
和[研究证据](../../../examples/plugins/research-evidence/evidence.tsx)。将两个文件
复制到仅安装打包 SDK 的独立目录，然后执行：

```bash
npx wuu-plugin-preview --check shell.tsx evidence.tsx
npx wuu-plugin-preview shell.tsx evidence.tsx
```

工作台实现线程导航、创建、改名、归档、会话与活动、发送和停止、外观设置与
资源预览。第三插件捕获可见回答，并在组件内保存各线程的工作笔记；这些笔记
明确为临时状态。它也拥有下一层 details 插槽。两者读取同一应用订阅并调用公开
actions。发布时可以使用示例 package scripts 打包桌面入口；作者预览无需构建。

## 服务错误

运行时提供者可以抛出
`new RuntimeHostServiceError({ code: 'state_conflict', message: 'revision changed' })`。
JSONL 响应保留 code 与 message，嵌套服务调用同样如此。具有非空字符串 `code`
的错误也遵守该传输契约；其他字段和堆栈不会被序列化。普通异常没有领域错误码。
消费者应判断 code，不应匹配 message 文案。外部效果的结果不确定，不代表可以
重放该效果。
