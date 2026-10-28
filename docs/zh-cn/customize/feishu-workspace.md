# 飞书工作区

飞书工作区插件在桌面侧栏中提供飞书网页入口，以及五个直接调用飞书官方 API 的
文档工具。它作为独立源码包提供，不再随 Wuu 分发。在仓库根目录按需安装：

```sh
wuu plugin install ./plugins/feishu-workspace/package
wuu plugin approve feishu-workspace
```

随后在插件列表中配置，再从侧栏打开飞书。

## 主动配置连接

网页无需 API 令牌即可打开，需要登录时在内嵌网页中登录。网页使用 Wuu 现有的
浏览器会话，仅支持桌面主工作台，不支持远程网页、移动端或弹出的独立窗口。

在插件设置中：

1. 将「飞书工作区网址」设为你的 HTTPS 飞书首页或文档网址。保存后请禁用并重新
   启用插件，或重新加载 Wuu；默认打开飞书云空间。
2. 选择「API 身份」：`user` 对应 `user_access_token`，`tenant` 对应
   `tenant_access_token`。
3. 通过组织批准的飞书应用与授权流程获取访问令牌，填入「飞书访问令牌」。宿主
   使用操作系统凭据服务保存，不写入普通插件设置。使用凭据控件清除令牌即可
   断开 API 连接。

令牌仅附加到发往 `https://open.feishu.cn` 的请求中；插件无法读取原始值。
网页登录不会自动授予 API 权限。不要填入 App Secret、刷新令牌或浏览器 Cookie。
示例不执行 OAuth 流程或自动刷新令牌，令牌过期后需手动替换。系统安全存储
不可用时会报错，不回退到明文文件。

## 权限与身份

飞书同时校验应用已获批的 API 权限，以及当前身份对目标资源的访问权限。
用户令牌代表该用户；应用令牌代表应用，不代表浏览器中登录的用户。

| 操作 | 最小适用权限 | 身份 |
| --- | --- | --- |
| 搜索 | `search:docs:read` | 仅用户 |
| 读取元数据、纯文本和文档块 | `docx:document:readonly` | 用户或应用 |
| 创建空白新版文档 | `docx:document:create` | 用户或应用 |
| 追加或修改文档块 | `docx:document:write_only` | 用户或应用 |

新版文档操作也接受范围更广的 `docx:document`。搜索还接受
`drive:drive:readonly` 或 `drive:drive`；应按需要申请最小权限。应用身份在指定
文件夹内创建文档时，仅支持应用创建的文件夹；省略文件夹参数则使用该身份的
根目录。插件不修改分享权限，也不会将文档设为公开。

## 工具与范围

- `feishu_search_documents`：按标题关键词搜索，每页最多 50 个结果。使用
  `next_offset` 继续读取。飞书要求 `offset + count < 200`；达到搜索窗口
  边界会返回 `search_window_exhausted`，不会声称搜索了全部文档。结果可能
  同时包含旧版 `doc` 和新版 `docx`。
- `feishu_read_document`：读取新版文档元数据与纯文本，或一页官方文档块。
  纯文本分页使用 `next_offset`；文档块分页使用 `next_page_token` 和
  `revision_id`。后续块分页需沿用同一版本号。纯文本与元数据分别请求，期间
  文档可能变化，不是原子快照。
- `feishu_create_document`：按标题与可选文件夹创建空文档。正文使用单独的
  追加调用写入，避免正文写入失败掩盖文档已创建的事实。
- `feishu_append_blocks`：一次请求追加最多 50 个段落、一级至三级标题、列表
  或引用。文本按字面写入，不解析 Markdown；每项创建一个独立块。每项最多
  10,000 个 UTF-16 编码单元，请求正文内容最多 500 KB。
- `feishu_update_block`：替换一个块的全部文本元素，会移除该块的行内格式、
  链接和提及。不会删除块或替换整篇文档。请先读取块并确认 `block_id`。

追加与修改均要求先前读取的文档版本，以及唯一 `operation_id`（建议 UUID）。
操作标识通过飞书 `client_token` 传递；仅在重试同一完整操作时复用。过期版本
与 API 冲突会直接返回，请重新读取后再准备新的修改。插件不会自动重试写入。
连接失败或服务端错误可能导致写入结果不确定，应先检查文档。创建操作没有
幂等标识，重复调用可能创建重复文档。

读写工具只接受新版文档 ID 或 HTTPS 飞书 `/docx/` 网址。知识库网址标识的是
节点，请先在飞书中找到底层文档。电子表格、多维表格、旧版文档编辑、文件上传、
分享及其他任意块类型不在此示例范围内。读取的内容作为数据处理，不视为指令。
工具保留宿主权限检查，通过正常工具发现及 code-mode 路径使用。

限流结果包含建议等待时间，也处理飞书 HTTP 400/错误码 `99991400` 的限流。
认证与权限错误会提示需要检查的设置。工具输出不包含访问令牌、App Secret 或
浏览器 Cookie。

## 验证与官方资料

[插件说明](../../../plugins/feishu-workspace/README_zh.md)介绍无凭据 JSONL 验收。
模拟 API 验证的是插件请求协议，不代表真实账号权限、OAuth 或网页登录验收。

实现于 2026-10-19 核对了以下官方资料：
[搜索](https://open.feishu.cn/document/server-docs/docs/drive-v1/search/document-search)、
[创建](https://open.feishu.cn/document/server-docs/docs/docs/docx-v1/document/create)、
[纯文本](https://open.feishu.cn/document/server-docs/docs/docs/docx-v1/document/raw_content)、
[块分页](https://open.feishu.cn/document/server-docs/docs/docs/docx-v1/document/list)、
[追加块](https://open.feishu.cn/document/server-docs/docs/docs/docx-v1/document-block/create)、
[修改块](https://open.feishu.cn/document/server-docs/docs/docs/docx-v1/document-block/patch)及
[访问令牌](https://open.feishu.cn/document/uAjLw4CM/ukTMukTMukTM/authentication-management/access-token/tenant_access_token)。
