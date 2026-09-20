# 桌面 UI 维护

修改桌面界面时，使用 renderer 的共享组件和设计变量，并在真实浏览器或 Electron 窗口中检查结果。类型检查和 jsdom 测试无法证明间距可读、滚动效果正常，或键盘焦点清晰可见。

## 预览真实组件

完成[开发环境设置](development.md)后，启动 renderer 预览服务：

```bash
cd desktop
npx vite --host 127.0.0.1
```

使用 Vite 显示的端口打开 `/dev/design-system/` 或 `/dev/button-standards/` 等路径。[`desktop/dev`](../../../desktop/dev/) 包含特定组件与状态的夹具，使用合成数据，不会复现所有产品桥接和生命周期。

新手引导有独立的 Electron 入口：

```bash
npm --prefix desktop run dev:onboarding
```

它渲染真实的新手引导组件，但不使用产品 preload、app-server 或持久化 profile。选择不会保存，模型凭据请使用虚构值。按 Cmd+R 或 Ctrl+R 重新开始；正常退出时会删除临时 profile。这个预览检查呈现，不验证登录或设置持久化。

[小球实验室（英文）](../../../desktop/dev/mascot/README.md)使用 `npm --prefix desktop run lab:mascot`。修改依赖原生行为、IPC 或真实会话状态时，使用完整的 `make dev` 路径。临时截图放入已忽略的产物目录，提交的夹具使用合成内容。

## 共享字体与尺寸关系

[`base.css`](../../../desktop/src/renderer/styles/base.css)定义字体、颜色、圆角、焦点和层级等基础角色；[`spacing.css`](../../../desktop/src/renderer/styles/spacing.css)定义间距角色、密度边界和控件最小尺寸。优先使用已有角色，不为每个组件另设常量。

尊重用户分别设置的 UI 与代码字号。行高随内容增长，为末尾操作和状态标记预留空间，同级标签的对齐不应随运行或未读状态改变。密度调整留白，不移除最小点击尺寸；粗指针设备使用更大的控件尺寸下限。

紧凑菜单使用 `--menu-inset`、`--menu-item-gap` 和 `--menu-shell-radius`。外层圆角由内层圆角加内缩距离得到，使嵌套圆角保持对应关系；面板和对话框使用各自的圆角角色。给每个有 padding 的层设置同一个圆角数值，并不能得到相同的几何关系。

公开插件主题 token 的契约范围小于所有内部 CSS 变量。暴露新 token 或建议插件作者依赖内部变量前，请查看[主题参考](../customize/theme-surface-matrix.md)。

## 滚动边缘渐隐

[`scroll-fade.css`](../../../desktop/src/renderer/styles/scroll-fade.css)为有限高度的工具/推理检查区和导航列表提供按需启用的渐隐。将属性加在已有的垂直滚动节点上：

```tsx
<div className="existing-scroll-region" data-scroll-fade="compact" ref={scrollRef}>
  {content}
</div>
```

密集检查区使用 `compact`，导航列表使用空值。消息、设置、文档等主要阅读区保留普通裁切。输入框、终端、编辑器、图片/PDF 画布和横向滚动区不适用。固定标题、输入区和菜单应留在遮罩节点外。

该工具使用自身滚动时间线和透明度遮罩，不增加覆盖层或 React 滚动更新。只有边缘外还有内容时才渐隐，没有溢出就没有渐隐。嵌套滚动节点相互独立，每侧渐隐最多占视口一半。不支持相关特性的浏览器、减少动态效果、强制颜色和打印模式都回退为普通裁切。接入前检查已有的 `animation` 和 `mask-image` 声明，因为该工具会控制两者。

## 检查受影响状态

检查浅深色主题、默认和大字号、宽窄窗口、空白和长内容，以及键盘焦点。组合检查可以同时出现的状态，例如选中、运行、未读、悬停、禁用和拖动。注意裁切、重叠、移动的点击目标，以及被隐藏操作或占位元素挤偏的标签。

滚动修改要检查短内容与溢出内容在顶部、中间和底部的表现。追加流式内容、手动上翻、收起再展开折叠区、切换会话，并调整窗口大小，确认跟随/暂停、文字选择、菜单和滚动条仍可使用。报告实际检查过的条件；一张截图或单元测试通过不代表完整的视觉验收。
