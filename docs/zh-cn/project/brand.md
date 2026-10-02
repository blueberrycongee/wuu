# Wuu 品牌规范

[完整手册](../../../brand/manual/index.html)（中文，浏览器打开） · [资产与生成命令](../../../brand/README.md)

本规范包含品牌资产与应用提案。桌面应用、官网和文档站的采用需另行评审。

![手册封面](../../en/assets/brand/manual-cover.png)

## 小球

| 用法 | 颜色 | 眼睛 |
| --- | --- | --- |
| 品牌符号 | 墨色；深色背景用纸色 | 固定看向右上 |
| 会话中的 Wuu | 墨色；暗色主题用纸色 | 随活动状态变化 |
| 其他 agent、模型与服务商 | 七种 agent 色之一 | 墨色，随活动状态变化 |

- 按显示直径选资产：大于 40 px 用展示版，21–40 px 用小尺寸版，20 px 及以下用微型版
- 保留眼睛的位置、倾斜与比例，不改为居中竖直
- 每个版面最多一只品牌小球；agent 小球群除外。下载、保存使用产品进度控件
- 不加嘴、腮红、四肢、渐变、高光、投影或描边；品牌符号不戴产品配饰

![小球构造](../../en/assets/brand/manual-ball-construction.png)

## 字标与组合

字标使用提供的文件，不用字体重新排。正文写 **Wuu**，命令写 `wuu`。

以字标 x 高度 X、小球直径 D 计：

| 项目 | 规格 |
| --- | --- |
| 横式组合（默认） | 球 1.4 X，间距 0.34 X |
| 竖式组合 | 用于接近正方形的版面 |
| 安全空间 | 组合四周 1 X；小球四周 0.25 D |
| 组合最小尺寸 | 屏幕 x 高度 7 px；印刷宽度 14 mm |
| 小球最小尺寸 | 屏幕 12 px；印刷 4 mm |

![组合方式](../../en/assets/brand/manual-lockups.png)

## 色彩

| 色组 | 用途 | 限制 |
| --- | --- | --- |
| 墨 `#141411`、纸 `#F7F7F4` | 标识、Wuu 头像、主按钮、正文 | 不换色相或加渐变 |
| 石色中性色 | 底色、表面、分隔线、次要文字、选中态 | 必要文字保持足够对比度 |
| agent 色 | 其他 agent 球身、插画、按 agent 区分的图表 | 不用于文字、控件、状态或 Wuu |
| 状态色 | 成功、警告、错误、信息 | 配文字或图标，不作装饰 |
| 交互色 | 墨色主按钮、2 px 蓝色焦点环 | 不为每个选中项加彩色 |

中性色对比度：正文 17.2 : 1，次要文字 8 : 1，三级文字 4.8 : 1，占位文字 3 : 1。agent 色的 OKLCH 明度为 0.84，须配名字区分。失败时球身不变色，用眼睛姿态、红色文字和图标提示。

![色彩用途](../../en/assets/brand/manual-colour-roles.png)

![agent 色](../../en/assets/brand/manual-agent-colours.png)

## 字体与排版

| 字体 | 用途 | 授权 |
| --- | --- | --- |
| Hanken Grotesk | 品牌拉丁字母标题与正文 | SIL OFL 1.1，随仓库分发 |
| 思源黑体 / Noto Sans CJK SC | 中文 | SIL OFL 1.1，从官方发布获取 |
| Fragment Mono | 命令、路径、变量名；关闭连字 | SIL OFL 1.1，随仓库分发 |
| 系统字体 | 产品界面，跟随用户设置 | 操作系统自带 |

中英文、数字与单位之间加空格。中文用全角标点，命令和路径用等宽字体，强调用字重。

![字体](../../en/assets/brand/manual-type-families.png)

## 动效

| 动作 | 时长 | 使用场景 |
| --- | --- | --- |
| 视线转移 | 180 ms，ease-out | 状态改变 |
| 眨眼 | 60 + 40 + 90 ms，间隔 3.8–8.2 s | 空闲、聆听、等你确认 |
| 回落 | 280 ms，压扁不超过 6 % | 每轮完成时一次 |
| 工作中微动 | 周期 1600 ms | 思考、执行期间 |

15 种活动对应八个姿态。开启减少动态效果时，直接切换最终姿态，保留状态文字，停止眨眼、回落和循环。[参考实现](../../../brand/assets/motion/wuu-ball.js)

![动效状态](../../en/assets/brand/manual-motion-states.png)

## 应用

- 应用图标沿用 `assets/app-icon-source.*` 原稿。其渐变、28° 眼睛和动势线不用于标识、头像或插画；图标不代替标识
- 封面、首屏和社交图可裁切球身：最多两条边，双眼完整，文字不压在球上

![应用图标](../../en/assets/brand/manual-app-icon.png)

![产品界面提案](../../en/assets/brand/manual-product-light.png)

![官网提案](../../en/assets/brand/manual-website.png)

## 维护与授权

数值在 `brand/tokens/tokens.json` 中修改。运行 `npm --prefix brand run build`、`npm --prefix brand run render`，检查生成结果；不要手改导出文件。环境要求见 [brand/README.md](../../../brand/README.md)。产品变量仍以[设计系统](design-system.md)为准。

品牌资产随仓库以 MIT 分发；MIT 不授予商标权，品牌使用条款待维护者确认。

待验证：商标、用户研究、32 px 以下应用图标、印刷色、Windows / macOS 渲染、agent 色觉模拟。
