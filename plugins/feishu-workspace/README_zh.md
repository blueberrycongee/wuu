# 飞书工作区

[English](README.md) · [使用指南](../../docs/zh-cn/customize/feishu-workspace.md)

默认关闭的可选插件，通过公开 Extension API 提供飞书网页视图及文档工具。
运行时代码位于 `src/`；正式清单、网页视图和图标位于
`plugins/feishu-workspace/package/`。无需飞书 CLI，不使用浏览器 Cookie
进行 API 身份验证。

在仓库根目录使用 Node 22.7+：

```sh
node scripts/build-bundled-plugins.mjs --plugin=feishu-workspace
node plugins/feishu-workspace/scripts/api-acceptance.mjs
```

验收脚本启动实际构建后的 JSONL 子进程，并通过模拟宿主凭据传输验证用户与应用
身份边界、搜索与读取分页、网址校验、创建/追加/修改请求、版本号、幂等标识、权限
错误、限流、写入结果不确定及取消。结果与协议记录保存在
`.tmp/e2e/feishu-workspace/`。不发送外部请求、不使用真实凭据；不代表真实账号
或桌面视觉验收。

## 图标来源

单色 SVG 改编自[飞书开放平台官方图标](https://lf-package-cn.feishucdn.com/obj/feishu-static/lark/open/doc/frontend/favicon-logo.svg)，
获取于 2026-10-19。移除了白色底板，将路径缩放到 24 单位画布，并使用 Wuu 的
明暗主题单色填充。来源未公开开源图标许可证；飞书名称和标志的权利归其各自
权利人所有，不按 Wuu 的 MIT 许可证重新授权。图标仅用于识别连接的服务，
不表示官方关联或背书。
