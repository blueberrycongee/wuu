# 生成式 UI（实验性）

生成式 UI 让模型在对话中返回可交互表格、图表和小型本地表单。内置插件
**默认关闭**。打开 **Skills & Plugins**，选择 **Generative UI (Experimental)**
并启用，模型才能使用 `render_ui` 工具。禁用后会撤销工具和渲染器，历史结果
仍保留文字摘要。

插件沿用 Wuu 的工具调用链路，不需要另一个 API key 或独立 UI 生成服务。
当前模型和服务商必须支持该工具 schema。外部引擎需要接入 Wuu 插件工具才能
使用它；启用插件不会自动给无关引擎增加工具。

## 交互

- 表格：本地筛选，按列排序，数字按数值排序。
- 柱状图、折线图：查看数值，切换全部、最近 10 个或最近 25 个数据点。
  展开 **Chart data** 可用表格读取精确数值。
- 表单：编辑文本、数字、选项和复选框，预览、复制或重置。它们都是本地草稿。
  按钮不会发送模型消息、提交表单、调用其他工具或打开网站。

视图状态保存在此设备的插件专属存储中，按对话、工具结果部分和完整 spec
隔离。重开同一结果可以恢复筛选、排序、图表范围和表单草稿。每个结果最多
保存 16 KiB 状态；存储失败或草稿过大时会提示，当前交互仍可继续。状态不跨
设备同步。表单草稿属于本地保存的对话数据，请勿在其中输入密码或其他秘密。

## v1 协议

`render_ui` 接收包含 `spec` 的对象。spec 必须包含 `version: 1`、非空
`title`、有意义的非空文字 `fallback`，以及 1–16 个 ID 不重复的 `blocks`。
完整工具 schema 位于
[`plugins/generative-ui/schema.json`](../../../plugins/generative-ui/schema.json)。
原生工具验证后发布 MIME 为 `application/vnd.wuu.ui+json`、
`placement: inline` 的 resource；桌面展示历史内容前再次验证。

| 类型 | 必填内容 | 限制 |
| --- | --- | --- |
| `text` | `text` | 4,000 字符 |
| `table` | `columns`（`key`、`label`），`rows`（单元格数组） | 1–8 列、0–200 行；每行单元格数与列数相同 |
| `chart` | `chartType`（`bar` 或 `line`），`points`（`label`、`value`） | 1–100 个点；标题和轴标签可选 |
| `form` | `fields`（`id`、`label`、`kind`） | 1–12 字段；类型为 `text`、`number`、`select`、`checkbox`；可提供初始值 |

ID 以 ASCII 字母开头，最多 48 个字母、数字、连字符或下划线。标题最多
160 字符，标签 80，表格文本单元格和文本输入 500，摘要 8,000。数值必须有限，
范围为 −10¹² 到 10¹²。select 字段必须提供 1–20 个不重复选项，其他类型拒绝
options。初始值必须匹配字段类型。未知属性或组件类型会被拒绝。工具输入和
最终 spec 各自不得超过 128 KiB。

示例：

```json
{
  "spec": {
    "version": 1,
    "title": "各团队任务数",
    "fallback": "设计 12 项，工程 24 项。",
    "blocks": [{
      "id": "tasks",
      "type": "chart",
      "chartType": "bar",
      "xLabel": "团队",
      "yLabel": "任务数",
      "points": [
        { "label": "设计", "value": 12 },
        { "label": "工程", "value": 24 }
      ]
    }]
  }
}
```

`render_ui` 是直接呈现工具，开启程序化工具调用时也保留在顶层。
应直接调用它，不可在 `run_code` 内嵌套调用。

## 边界与降级

v1 在工具调用完成后发布完整 spec，不流式展示半截 spec 或补丁。不支持模型
编写 JavaScript、HTML、CSS、URL、网络请求或可执行表达式。固定组件词汇由
Wuu 的 React 组件使用当前主题渲染。插件本身与其他 Wuu 插件一样属于受信任
扩展代码；这些数据限制不构成对其他已安装插件的沙箱。

工具参数无效时返回可读错误，模型可以修正重试或用文字回答。不兼容的历史
spec、禁用或缺失插件、渲染异常会降级为 resource 随附的普通文字。其他客户端
也可直接阅读这段文字，无需实现交互渲染器。

## 开发验证

运行 `npm --prefix desktop run build:core` 构建核心和 helper，再在图形环境中
运行 `npm --prefix desktop run test:e2e:generative-ui`。无头 Linux 可给同一
Electron 脚本加 `--no-sandbox --ozone-platform=headless
--ozone-override-screen-size=1440,1400`。
`WUU_GENERATIVE_UI_PLUGIN_HELPER` 可指定已构建的 helper，
`WUU_GENUI_OUTPUT` 指定产物目录。

E2E 用合成数据连接真实原生工具进程和生产用 artifact/插件渲染组件，检查
原生与桌面验证一致性、控件、状态隔离和恢复、禁用与错误降级、存储失败，并
记录工具结果、JSON 报告及深浅主题、宽窄窗口、默认和大字号截图。其存储适配器
是合成的，不代表实际模型调用或打包应用验收。

完整应用链路可运行 `npm --prefix desktop run test:e2e:generative-ui-app`。
该脚本构建并启动真实 App/main/preload/core，在临时 home 中使用本机合成
HTTP provider，检查默认关闭的工具发现、目录启用、原生工具结果进入消息流、
生产插件存储写入和刷新恢复，以及禁用和重新启用后的降级恢复。
实际 IPC、provider 请求、原生存储、截图和构建哈希保存在
`desktop/out/e2e/generative-ui-app`，可用 `WUU_GENUI_APP_OUTPUT` 改目录。
不使用外部模型或用户凭据；这是源码构建的应用验收，不是安装包验收。

在仓库根目录运行 `go run scripts/generative-ui-lifecycle-e2e.go`，可使用独立
临时 home 验证原生工具发现、执行、禁用、重新启用和安全模式；
`WUU_GENERATIVE_UI_PLUGIN_HELPER` 指定 helper，`WUU_GENUI_LIFECYCLE_OUTPUT`
指定 JSON 证据文件。该检查还会编译经 Wuu provider 规范化后的工具 schema，
不调用 provider，也不代表所有 provider 或模型都支持此 schema。
