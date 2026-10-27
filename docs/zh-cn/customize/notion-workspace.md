# Notion 工作区

可选的 **Notion** 插件提供 Notion 网站 Ribbon 入口，以及直连 Notion 官方 API 的文档工具。它作为独立源码包提供，不再随 Wuu 分发。在仓库根目录按需安装：

```sh
wuu plugin install ./plugins/notion-workspace/package
wuu plugin approve notion-workspace
```

随后可在 **技能与插件** 中配置。无需安装 Notion CLI。

## 打开网站

在 Ribbon 中选择 **Notion**。页面使用 Wuu 桌面浏览器会话，无需 API token 即可打开；必要时在页面内登录 Notion。网站登录不会自动授权文档 API。

插件的 **Notion website** 设置接受 `notion.so` 或 `notion.com`（含子域名）的 HTTPS 地址。修改后请禁用并重新启用插件，或重新加载 Wuu。网站视图仅支持桌面主工作台，不支持远程 Web、移动端或弹出的会话窗口。禁用插件会关闭网页并移除工具，但不会退出共享浏览器会话中的登录。

## 配置文档访问

1. 在 [Notion 开发者门户](https://app.notion.com/developers/connections)创建内部集成，仅授予所需内容权限：读取与搜索需要 read，创建与追加需要 insert，修改需要 update。
2. 在 Notion 页面的 **••• → Add connections** 菜单中，将集成连接到允许访问的页面。
3. 在插件设置的 **Notion internal integration token** 字段中保存内部集成 token。这是宿主凭据库支持的密钥字段，不属于普通插件设置。不要将 token 放进聊天、项目文件或网站地址。

宿主仅在发送到 `api.notion.com` 的 HTTPS 请求中注入 token，插件运行时拿不到凭据。API 不读取或提取网站 Cookie。此示例没有实现公开集成的 OAuth、token 刷新或网站登录转 API 授权。删除已保存的 token 会停止 API 访问，但不会在 Notion 撤销它；需要撤销时请到 Notion 操作。

## 文档工具

- `search_pages`：返回一页已共享页面的标题匹配结果和不透明续页游标。不是全文搜索，也不是完整工作区清单。
- `read_page`：读取页面元数据和一页直属子块，不自动读取嵌套内容、后续分页或完整的分页属性值。
- `read_children`、`read_block`：读取指定块，并显式遍历子块。将 `next_cursor` 原样传入 `start_cursor`；每页 1–100 项，默认 50 项。
- `create_page`：在明确指定的父页面 UUID 下创建带标题的子页面，可包含最多 100 个简单块。不创建数据库行或应用模板。
- `append_blocks`：在末尾追加 1–100 个块，保留原内容。
- `update_block_text`：用纯文本替换一个受支持块的全部富文本，会移除该文本的格式、提及和链接；其他字段与嵌套子块保留。
- `rename_page`：只修改现有标题属性。

写入支持段落、三级标题、无序与有序列表项、复选框、引用和折叠块。每段文字或标题最多 2,000 字符，每次编码后的请求最多 450 KB。不提供嵌套块创建、任意属性修改、删除、移动块或整页替换。使用 Notion 返回的 UUID；网站地址是展示链接，不会被解析为 API ID。

修改文字或标题必须提供近期读取结果中的完整 `last_edited_time`。插件在写入前再读一次，发现记录已变更便拒绝修改。这只是尽力而为的检查，不是原子锁；其他编辑者仍可能在两次请求之间修改内容。

写入成功时会说明 API 响应验证了什么。创建页面会校验返回的标题与父页面；子块内容仅获 API 确认，不会回读。追加与修改会比较返回的块文本或标题。这些响应检查不等于独立回读验证。写入响应丢失时返回 `outcome: unknown`，重试前应检查目标，避免产生重复页面或内容。成功响应未通过校验时返回 `applied_unverified`，同样不能盲目重复写入。

请求固定使用 `Notion-Version: 2026-03-11`，每次工具操作最多 45 秒，并通过串行运行时队列执行。遇到 429/529 时遵守 `Retry-After`，在短重试预算内最多重试两次，否则返回剩余等待时间。写入遇到网络故障或结果不明的服务端错误时不会自动重放。401 提示更换 token；403 可能涉及内容权限、页面访问或工作区限制；404 可能表示记录不存在、已移入废纸篓或未共享。

## 验证与开发

可重复的模拟 API 验收通过构建后的 JSONL 运行时和宿主凭据请求边界执行，不访问 Notion，不使用真实账号或 token：

```sh
npm --prefix plugins/notion-workspace run build
npm --prefix plugins/notion-workspace run test:e2e
```

JSON 证据报告写入 `.tmp/notion-workspace-e2e/`。它只验证本地契约，不代表真实账号授权、全部 Notion 块类型的内容保真或网站登录已验证。桌面渲染和真实账号验收需单独进行。

浅色与深色 SVG 标志来自 [Simple Icons](https://github.com/simple-icons/simple-icons/blob/develop/icons/notion.svg)，使用 CC0 许可；打包资源包含许可和来源说明。Notion 商标权仍属于其权利人。本插件未获 Notion 官方背书。

官方参考：[授权](https://developers.notion.com/docs/authorization)、[版本](https://developers.notion.com/reference/versioning)、[搜索](https://developers.notion.com/reference/post-search)、[创建页面](https://developers.notion.com/reference/post-page)、[读取子块](https://developers.notion.com/reference/get-block-children)、[追加子块](https://developers.notion.com/reference/patch-block-children)、[修改块](https://developers.notion.com/reference/update-a-block)、[修改页面](https://developers.notion.com/reference/patch-page)及[请求限制](https://developers.notion.com/reference/request-limits)。
