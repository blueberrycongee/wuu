# 生成式 UI（实验性）

生成式 UI 让模型在对话中返回可交互布局、图表、计算器和表单。内置插件
**默认关闭**。打开 **Skills & Plugins**，选择 **Generative UI (Experimental)**
并启用，模型才能使用 `render_ui` 工具。禁用后会撤销工具和渲染器，历史结果
仍保留文字摘要。

插件沿用 Wuu 的工具调用链路，不需要另一个 API key 或独立 UI 生成服务。
当前模型和服务商必须支持该工具 schema。外部引擎需要接入 Wuu 插件工具才能
使用它；启用插件不会自动给无关引擎增加工具。

## 交互

- 组合行、网格、卡片、折叠内容，以及条件和重复内容。
- 编辑文本、数字、日期、选项、复选框和滑块，通过表达式在本地重新计算，
  无需为每次调整发起模型请求。
- 筛选和排序表格；查看柱状图、折线图、面积图、散点图、饼图和环形图。
  系列图例和可访问数据表用于检查原始数值。
- 表单沿用原生提交和字段验证，包括必填和无效输入；提交需要明确操作。
- 本地复制、重置。源码格式或 v2 的按钮和表单还可以明确向产物所属会话发送后续消息，附上来源和
  当前数值作为上下文。发送需要真实用户操作，沿用普通会话链路，不清空已有
  输入草稿和附件。预览、不可用会话或正在运行的会话不能提交后续消息。

工具参数生成过程中，渲染器预览已经完整且通过验证的节点。不完整或无效更新
保留上次可用预览。预览状态是临时的；最终结果的状态保存在本设备插件存储中，
按会话、工具结果部分和完整 spec 隔离。v2 将声明值与表格筛选、排序、图例
显隐、折叠展开分别保存，完整快照合计不得超过 16 KiB。组件视图状态不会
附加到后续消息上下文。存储失败或
草稿超限会提示，当前视图仍可使用。状态不跨设备同步。输入属于本地对话数据，
请勿填写密码或其他秘密。

## 源码格式（v3）

新调用使用 `{ "spec": { "version": 3, "title": "…", "fallback": "…", "source": "…" } }`。
面向模型的 [schema](../../../plugins/generative-ui/schema.json) 描述支持的语法与组件。
把 `source` 放在最后，让标题和文字摘要先于界面到达。

源码由可选的开头 `{@body ...}` 声明块和 JSX 组成。下面是 `source` 的内容：

```jsx
{@body
  const [quantity, setQuantity] = DIL.useState(2);
  const total = quantity * 12;
}
<Stack>
  <Input label="数量" inputType="number"
    value={quantity} onChange={setQuantity} />
  <Metric label="合计" value={total} />
  <Button onClick={() => DIL.sendMessage(`解释合计 ${total}`)}>
    解释这个结果
  </Button>
</Stack>
```

状态 setter 接收新值或更新函数。普通表达式、局部函数、数组映射、筛选、归约和
条件 JSX 用于计算视图。控件的 `onChange` 接收新值，不接收 DOM 事件。
重复组件需要唯一 `key`，以在重排后保留状态。函数组件可以返回 JSX。
正文放在 `Text` 或 `Caption` 中，行内支持 `strong`、`em`、`code` 和 `br`。

布局、图表、表格和控件沿用共享主题组件。自定义图解使用明确的 SVG 元素，
包括形状、路径、文字、渐变和箭头标记。颜色支持 `accent`、`muted`、
`surface`、`success`、`danger` 等语义值。引用仅限视图内部，
不支持外部图片、脚本、任意样式和嵌入 HTML。

插件解析并解释执行有界语言，不在桌面环境直接执行生成的 JavaScript。
不支持 import、DOM API、网络、异步函数和无限制循环。
参数最多 128 KiB，其中源码最多 120 KiB；最多 4,096 个语法节点、
512 个展开组件、每个集合 200 项、每次渲染或交互 30,000 步计算、16 KiB 状态。
展开后的表格、图表和控件共用 10,000 个计量单位的渲染预算；文本与属性值
合计不超过 1 MiB。同一份数据用于多个组件时，按每次实际渲染分别计入预算。
工具运行时与桌面端验证同一份源码和初始视图。

外层 JSX 尚未闭合时，已到达的文字和完整子组件就能显示。不完整表达式保留上次
可用预览；最终源码必须完整解析。预览到最终结果保持组件身份，
新内容短暂淡入并尊重减少动态效果设置。预览保持只读。
交互计算失败会保留上次有效值。重新打开同一结果时恢复输入值和组件偏好；
后续消息仅附带声明值。

## 历史 v2 协议

`render_ui` 接收 `{ "spec": { "version": 2, "title": "…", "fallback": "…",
"state": [], "body": [] } }`。state 每项包含 `id` 和 JSON `value`；body 节点
使用唯一 ID 及对应类型的明确属性。v2 保留用于历史产物和运行时直接调用兼容，
新的模型调用使用 v3。

v2 是有界的数据程序。表达式读取已声明状态，支持算术、比较、格式化、条件、
数组构造、映射、筛选和聚合；重复内容只能在对应作用域内读取当前项。动作支持
设置状态、重置、复制文本，以及明确发送后续消息。未知节点、属性、状态引用和
越界作用域会被拒绝。`each.key` 可选表达式在当前项作用域内产生稳定身份，
支持最多 80 字符的非空字符串或正常范围内的有限数字，兄弟重复键在运行时
拒绝。不指定 key 时按完整项值区分，修改项内容可能重置该项局部视图状态。
不执行生成的 JavaScript，也不接收任意 HTML、CSS、URL、
网络访问或 React 属性。

TypeScript 工具运行时与桌面端共用程序验证器。每个 spec 最多 128 KiB，最多 32 个状态项、
初始状态 16 KiB、256 个声明节点及 12 层节点嵌套、2,048 个表达式节点及 16 层
表达式嵌套。运行时还限制计算和展开规模，超限时停止而非继续无限工作。
v1 历史产物仍可阅读和交互。

下面的计算器和复杂表单使用同一套控件与表达式：

```json
{
  "spec": {
    "version": 2,
    "title": "估算",
    "fallback": "选择数量，按每项 12 计算合计。",
    "state": [{ "id": "quantity", "value": 2 }],
    "body": [
      { "id": "quantityInput", "type": "input", "inputType": "number", "state": "quantity", "label": "数量" },
      { "id": "total", "type": "metric", "label": "合计", "value": { "op": "multiply", "args": [{ "op": "state", "key": "quantity" }, 12] } }
    ]
  }
}
```

## 历史 v1 协议

`render_ui` 接收包含 `spec` 的对象。spec 必须包含 `version: 1`、非空
`title`、有意义的非空文字 `fallback`，以及 1–16 个 ID 不重复的 `blocks`。
该格式保留用于历史产物和运行时直接调用兼容。
工具运行时验证后发布 MIME 为 `application/vnd.wuu.ui+json`、
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

最终 spec 通过工具 resource 发布。流式预览仅提供给明确接收该能力的当前
MIME 渲染器，在最终结果接受前保持只读。Wuu 的 React 组件使用当前主题渲染
已经验证的组件词汇。插件本身与其他 Wuu 插件一样属于受信任扩展代码；数据
程序限制不构成对其他已安装插件的沙箱。

工具参数无效时返回可读错误，模型可以修正重试或用文字回答。不兼容的历史
spec、禁用或缺失插件、渲染异常会降级为 resource 随附的普通文字。其他客户端
也可直接阅读这段文字，无需实现交互渲染器。

## 插件热更新

工具运行时与桌面渲染器使用普通的公开扩展生命周期。首次构建后，运行
`wuu plugin dev plugins/generative-ui` 即可监听开发目录，包括 schema、
共用验证器、桌面入口和 SDK 输入。修改会重新构建并发布新一代插件，
无需重新编译 Go 或重启 Wuu，生成产物不会触发重复构建。

渲染器更新立即替换已挂载的视图。工具定义和 schema 在下一轮空闲边界采用；
正在执行的对话轮次保留原运行时快照。新版本加载失败时保留上一可用版本。
禁用会撤掉交互渲染并撤销工具，包括被旧轮次保留的运行时代次。历史结果保留
带版本的数据与文字降级，重新启用后恢复兼容的已保存输入。更新必须保留
v1/v2/v3 结果与存储兼容性，或明确提供迁移。

## 开发验证

运行 `npm --prefix plugins run build -- --plugin=generative-ui` 构建插件运行包，再在图形环境中
运行 `npm --prefix desktop run test:e2e:generative-ui`。无头 Linux 可给同一
Electron 脚本加 `--no-sandbox --ozone-platform=headless
--ozone-override-screen-size=1440,1400`。
`WUU_GENUI_OUTPUT` 指定产物目录。脚本通过桌面自带的 Node 运行时执行
JavaScript 工具，不需要独立的辅助程序。

E2E 用合成数据连接真实 JavaScript 工具进程和生产用 artifact/插件渲染组件，检查
运行时与桌面验证一致性、控件、状态隔离和恢复、禁用与错误降级、存储失败，并
记录工具结果、JSON 报告及深浅主题、宽窄窗口、默认和大字号截图。其存储适配器
是合成的，不代表实际模型调用或打包应用验收。

完整应用链路可运行 `npm --prefix desktop run test:e2e:generative-ui-app`。
该脚本构建并启动真实 App/main/preload/core，在临时 home 中使用本机合成
HTTP provider，检查默认关闭的工具发现、目录启用、插件工具结果进入消息流、
生产插件存储写入和刷新恢复，以及禁用和重新启用后的降级恢复。还检查交错工具参数的预览、取消，
以及明确发送后续消息时保留输入框草稿和附件。
实际 IPC、provider 请求、原生存储、截图和构建哈希保存在
`desktop/out/e2e/generative-ui-app`，可用 `WUU_GENUI_APP_OUTPUT` 改目录。
不使用外部模型或用户凭据；这是源码构建的应用验收，不是安装包验收。

在仓库根目录运行 `go run scripts/generative-ui-lifecycle-e2e.go`，可使用独立
临时 home 验证插件工具发现、执行、保留前台快照的热替换、失败更新恢复、
禁用、重新启用和安全模式。先构建插件
运行包，并确保 `PATH` 中有 Node.js；`WUU_GENUI_LIFECYCLE_OUTPUT` 指定
JSON 证据文件。该检查还会编译经 Wuu provider 规范化后的工具 schema，
不调用 provider，也不代表所有 provider 或模型都支持此 schema。
