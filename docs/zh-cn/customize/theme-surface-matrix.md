# 主题 Token 参考

Wuu 桌面端把界面外观收敛为一组公开的 CSS 自定义属性（design token）。插件可以通过
`contributes.themes` 声明主题并覆盖这些 token，也可以用 `registerThemeTokens`
在运行时按主题应用覆盖；机制见[插件主题](plugin-authoring.md)。

本页由 `scripts/generate-theme-surface-matrix.ts` 从
[`config/desktop-theme-contract.json`](../../../config/desktop-theme-contract.json)
与宿主样式依赖图生成（`make generate-theme-surface-matrix`），请勿手改；逐声明
级的覆盖明细见
[`config/desktop-theme-surface-matrix.json`](../../../config/desktop-theme-surface-matrix.json)。

## 覆盖方式

覆盖就是设置变量本身；宿主为每个 token 保留默认值，未覆盖的部分维持内置外观：

```css
:root {
  --wuu-color-canvas: #f6f6f4;
  --wuu-color-text: #1c1c1a;
}
```

## Token 一览

合同共定义 **82 个公开 token**（其中 **7 个**
旧名称兼容别名）与 **16 个语法高亮 token**；当前 **54 个**
已接入宿主界面。「已接入」表示宿主样式已在引用该 token，覆盖会立即生效；
「未接入（预留）」表示 token 已声明但宿主尚未引用，覆盖暂不改变任何界面。

### 颜色

| token | 说明 | 宿主 |
| --- | --- | --- |
| `--wuu-paper` | 旧名称，请使用 `--wuu-color-canvas` | 已接入 |
| `--wuu-ink` | 旧名称，请使用 `--wuu-color-text` | 未接入（预留） |
| `--wuu-ink-soft` | 旧名称，请使用 `--wuu-color-text-muted` | 已接入 |
| `--wuu-hairline` | 旧名称，请使用 `--wuu-color-border-subtle` | 已接入 |
| `--wuu-surface-muted` | 旧名称，请使用 `--wuu-color-surface-muted` | 未接入（预留） |
| `--wuu-accent` | 旧名称，请使用 `--wuu-color-accent` | 已接入 |
| `--wuu-accent-press` | 旧名称，请使用 `--wuu-color-accent-pressed` | 未接入（预留） |
| `--wuu-color-canvas` | 窗口与面板层的基础背景 | 已接入 |
| `--wuu-color-surface` | 内容区表面背景（消息、列表等） | 已接入 |
| `--wuu-color-surface-muted` | 弱化的表面背景，用于悬停与分区 | 已接入 |
| `--wuu-color-surface-elevated` | 抬升表面（输入框、浮层、卡片） | 已接入 |
| `--wuu-color-overlay-backdrop` | 模态遮罩背景 | 已接入 |
| `--wuu-color-text` | 主文字颜色 | 已接入 |
| `--wuu-color-text-muted` | 次级文字颜色 | 已接入 |
| `--wuu-color-live-highlight` | 实时状态高亮（流式输出等） | 已接入 |
| `--wuu-color-border-subtle` | 弱边框 | 已接入 |
| `--wuu-color-border-strong` | 强边框 | 已接入 |
| `--wuu-color-accent` | 强调色，用于主操作与选中态 | 已接入 |
| `--wuu-color-accent-pressed` | 强调色的按下态 | 未接入（预留） |
| `--wuu-color-focus` | 焦点环 | 已接入 |
| `--wuu-color-success` | 成功语义色 | 已接入 |
| `--wuu-color-warning` | 警告语义色 | 已接入 |
| `--wuu-color-danger` | 危险语义色 | 已接入 |
| `--wuu-color-info` | 信息语义色 | 已接入 |
| `--wuu-color-link` | 链接文字 | 已接入 |
| `--wuu-color-link-hover` | 链接悬停态 | 已接入 |
| `--wuu-color-on-accent` | 强调色表面上的内容色（图标、文字） | 已接入 |
| `--wuu-control-secondary-background` | 次级控件背景 | 已接入 |
| `--wuu-control-field-background` | 输入域背景 | 已接入 |
| `--wuu-control-icon-background` | 图标按钮背景 | 已接入 |
| `--wuu-badge-neutral-background` | 中性徽标背景 | 已接入 |
| `--wuu-inline-code-background` | 行内代码背景 | 已接入 |
| `--wuu-message-user-background` | 用户消息气泡背景 | 已接入 |
| `--wuu-message-user-color` | 用户消息文字颜色 | 已接入 |
| `--wuu-nav-item-hover-background` | 导航项悬停背景 | 已接入 |
| `--wuu-nav-item-hover-ring` | 导航项悬停描边 | 已接入 |
| `--wuu-skill-mark-browser-a` | 浏览器技能标记渐变起始色 | 未接入（预留） |
| `--wuu-skill-mark-browser-b` | 浏览器技能标记渐变中段色 | 未接入（预留） |
| `--wuu-skill-mark-browser-c` | 浏览器技能标记渐变终点色 | 未接入（预留） |
| `--wuu-skill-mark-commit-a` | 提交类技能标记渐变起始色 | 未接入（预留） |
| `--wuu-skill-mark-commit-b` | 提交类技能标记渐变中段色 | 未接入（预留） |
| `--wuu-skill-mark-commit-c` | 提交类技能标记渐变终点色 | 未接入（预留） |
| `--wuu-skill-mark-presentation-a` | 演示类技能标记渐变起始色 | 未接入（预留） |
| `--wuu-skill-mark-presentation-b` | 演示类技能标记渐变中段色 | 未接入（预留） |
| `--wuu-skill-mark-presentation-c` | 演示类技能标记渐变终点色 | 未接入（预留） |
| `--wuu-skill-mark-creator-a` | 技能创作器标记渐变起始色 | 未接入（预留） |
| `--wuu-skill-mark-creator-b` | 技能创作器标记渐变中段色 | 未接入（预留） |
| `--wuu-skill-mark-creator-c` | 技能创作器标记渐变终点色 | 未接入（预留） |
| `--wuu-skill-mark-plugin-a` | 插件类标记渐变起始色 | 未接入（预留） |
| `--wuu-skill-mark-plugin-b` | 插件类标记渐变中段色 | 未接入（预留） |
| `--wuu-skill-mark-plugin-c` | 插件类标记渐变终点色 | 未接入（预留） |
| `--wuu-skill-mark-default-a` | 默认技能标记渐变起始色 | 未接入（预留） |
| `--wuu-skill-mark-default-b` | 默认技能标记渐变中段色 | 未接入（预留） |
| `--wuu-skill-mark-default-c` | 默认技能标记渐变终点色 | 未接入（预留） |

### 排版

| token | 说明 | 宿主 |
| --- | --- | --- |
| `--wuu-font-family-ui` | 界面字体族 | 未接入（预留） |
| `--wuu-font-family-mono` | 等宽字体族（代码） | 未接入（预留） |
| `--wuu-font-size-ui` | 界面字号 | 未接入（预留） |
| `--wuu-font-size-body` | 正文（消息等大段文字）字号 | 未接入（预留） |
| `--wuu-line-height-body` | 正文行高 | 未接入（预留） |

### 间距

| token | 说明 | 宿主 |
| --- | --- | --- |
| `--wuu-space-unit` | 基础间距单位 | 未接入（预留） |
| `--wuu-message-actions-block-gap` | 消息操作区之间的间距 | 未接入（预留） |
| `--wuu-message-actions-overlay-gap` | 消息操作浮层间距 | 未接入（预留） |
| `--wuu-message-actions-control-gap` | 消息操作按钮间距 | 未接入（预留） |
| `--wuu-message-actions-inline-offset` | 消息操作的行内偏移 | 未接入（预留） |
| `--wuu-message-action-size` | 消息操作按钮尺寸 | 未接入（预留） |

### 密度

| token | 说明 | 宿主 |
| --- | --- | --- |
| `--wuu-space-density` | 界面密度缩放 | 未接入（预留） |

### 圆角

| token | 说明 | 宿主 |
| --- | --- | --- |
| `--wuu-message-action-radius` | 消息操作按钮圆角 | 未接入（预留） |
| `--wuu-message-user-radius` | 用户消息气泡圆角 | 未接入（预留） |
| `--wuu-radius-inner` | 内层高亮与嵌套圆角 | 未接入（预留） |
| `--wuu-radius-control` | 控件圆角 | 未接入（预留） |
| `--wuu-radius-panel` | 面板圆角 | 未接入（预留） |
| `--wuu-radius-overlay` | 浮层圆角 | 未接入（预留） |

### 边框

| token | 说明 | 宿主 |
| --- | --- | --- |
| `--wuu-message-user-border` | 用户消息气泡边框 | 已接入 |
| `--wuu-border-subtle` | 细分场景的弱边框 token | 已接入 |
| `--wuu-border-strong` | 细分场景的强边框 token | 已接入 |

### 阴影

| token | 说明 | 宿主 |
| --- | --- | --- |
| `--wuu-message-user-shadow` | 用户消息气泡阴影 | 已接入 |
| `--wuu-elevation-panel` | 面板阴影层级 | 已接入 |
| `--wuu-elevation-overlay` | 浮层阴影层级 | 已接入 |

### 动效

| token | 说明 | 宿主 |
| --- | --- | --- |
| `--wuu-motion-duration-fast` | 快速动效时长 | 未接入（预留） |
| `--wuu-motion-duration-normal` | 常规动效时长 | 未接入（预留） |
| `--wuu-motion-easing-standard` | 标准缓动曲线 | 未接入（预留） |

### 内容

| token | 说明 | 宿主 |
| --- | --- | --- |
| `--wuu-content-max-width` | 主内容区最大宽度 | 未接入（预留） |

## 语法高亮

代码高亮颜色由 `--wuu-syntax-*` 控制；`--hljs-*` 是早期的兼容名称，两者等价。

| token | 说明 | 宿主 |
| --- | --- | --- |
| `--hljs-keyword` | 关键字 | 已接入 |
| `--hljs-function` | 函数名 | 已接入 |
| `--hljs-string` | 字符串 | 已接入 |
| `--hljs-number` | 数字 | 已接入 |
| `--hljs-comment` | 注释 | 已接入 |
| `--hljs-tag` | HTML 标签 | 已接入 |
| `--hljs-literal` | 字面量 | 已接入 |
| `--hljs-meta` | 元信息 | 已接入 |
| `--wuu-syntax-keyword` | 关键字 | 已接入 |
| `--wuu-syntax-function` | 函数名 | 已接入 |
| `--wuu-syntax-string` | 字符串 | 已接入 |
| `--wuu-syntax-number` | 数字 | 已接入 |
| `--wuu-syntax-comment` | 注释 | 已接入 |
| `--wuu-syntax-tag` | HTML 标签 | 已接入 |
| `--wuu-syntax-literal` | 字面量 | 已接入 |
| `--wuu-syntax-meta` | 元信息 | 已接入 |
