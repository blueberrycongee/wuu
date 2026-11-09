# 受限会话分享

`session.sharing@1` 提供针对接收者的**投影视图**，不授予远程控制或原始历史访问权。
它是按需组合的参考能力，不是默认桌面或无界面产品自动启用的分享产品。
显式选择 `wuu.session-sharing-access` 和 `wuu.session-sharing.default` 才会启用。
外部提供者可通过同一公共服务替换存储、格式和业务策略。选定提供者失效时，不会回退到内置实现。

例如，在已有版本 4 组合的 `patches` 数组中加入普通补丁：

```json
{"modules":{"wuu.session-sharing-access":true,"wuu.session-sharing.default":true}}
```

未选择或禁用提供者时，分享不可用，不影响无关能力。下述端点和 CLI 用来使用、验证公共契约，
不会创建托管分享服务、企业身份系统或默认产品的分享界面。

## 审阅后发布

在会话的工作目录使用 `wuu session-share`。该命令经过普通 app-server 身份验证和授权边界，
不会另行打开会话数据库。本地命令代表所有者，不能用于冒充接收者。消息 ID 从所有者的会话历史取得。

```sh
wuu session-share preview '{"thread_id":"THREAD","selection":{"message_ids":["MESSAGE"]}}'
```

审阅返回的 `document` 并保存 `review_digest`。可见文字不保证不含秘密。
允许列表排除了推理、原始输入、工具参数、路径、引擎状态和任意元数据，
但无法判断普通句子是否含机密信息。选择附件前，还应单独审阅附件内容。

```sh
wuu session-share create '{"operation_id":"YOUR_STABLE_OPERATION_ID","thread_id":"THREAD","selection":{"message_ids":["MESSAGE"]},"mode":"snapshot","recipients":[{"kind":"user","issuer":"YOUR_VERIFIED_ISSUER","tenant":"YOUR_TENANT","subject":"RECIPIENT_SUBJECT"}],"expires_at":"FUTURE_RFC3339_TIMESTAMP","reviewed":true,"review_digest":"PREVIEW_DIGEST"}'
```

替换所有占位符，并设置未来的到期时间。接收者标识必须与传输层选定身份提供者验证的标识一致，
不能由客户端猜测电子邮件地址。预览后所选内容发生变化，创建会失败。
创建响应丢失时，使用相同 `operation_id` 和请求意图重试，重启后也适用。
持久化回执会返回同一个授权；修改意图会冲突。重试不会续期或恢复已撤销的授权。
若原会话已移除，在同一创建请求中添加 `recover_only:true`，可不加载源内容而恢复现有回执，
不会创建新的空授权。

## 范围和生命周期

- `message_ids`、`tool_result_ids`、`attachment_ids` 是相互独立的明确允许列表。
  缺省或空列表不披露内容。选择消息不会连带选择其工具结果和附件。
- `after` 含边界，`before` 不含边界，与 ID 选择相交。有时间限制时，时间未知的记录不会通过。
- 附件 ID 为 `ITEM/image/INDEX`、`ITEM/file/INDEX` 或 `ITEM/result/INDEX`。
  此版本仅支持内联保存的字节，不追踪外部制品引用或文件路径。
- `snapshot` 冻结创建时的所选内容。`live` 重新读取所选 ID，并要求 `live_updates:true`，
  表示明确同意这些条目后续的修改。新追加的消息 ID **不会**自动纳入。
- 在线读取和每次附件下载都会重新检查接收者、会话、到期和撤销状态。
  实时读取先检查授权再取得源数据，返回投影前再次检查。可轮询读取接口；
  此版本不会让接收者订阅原始会话事件。
- 已下载快照和附件是独立副本。撤销在线访问无法收回副本，也无法撤回撤销之前已传出的字节。

```sh
wuu session-share read '{"thread_id":"THREAD","share_id":"SHARE"}' > reviewed-copy.json
wuu session-share attachment '{"thread_id":"THREAD","share_id":"SHARE","attachment_id":"MESSAGE/image/0"}'
wuu session-share revoke '{"thread_id":"THREAD","share_id":"SHARE"}'
```

视图使用 `version:1`，只包含惰性文字和附件元数据。不要自动请求文字中嵌入的 URL。
附件字节通过经身份验证的 `session/share/attachment/read` 获取，不提供匿名持有者链接。
视图上限为 2 MiB、2,000 个条目，单附件上限为 8 MiB。超限时缩小选择范围，不要绕过投影。

## 身份验证和操作分离

所有 `session/share/*` 请求均经过 `resource.authorization@1`。默认资源策略将接收者读取决定
委托给选定分享提供者的 `authorize`。请求使用传输层已验证身份；JSON 中的 `actor`、`principal`
或 `owner` 声明不能替换该身份。分享 ID 是资源标识，不是身份凭据。
读取授权不包含原始 `thread/resume`、`thread/content/read`、`artifact/read`、工作区访问、评论、
会话输入或执行权限。其他选定授权策略必须明确授予相应发送或执行操作，评论不会隐含执行权。
默认分享实现不提供评论或执行功能。

## 替换提供者

[`examples/plugins/restricted-session-sharing.ts`](../../../examples/plugins/restricted-session-sharing.ts)
是仅依赖公共 SDK 和 `state.documents@1` 的独立 TypeScript 实现。它使用独立持久化命名空间，
将有效期限制为 24 小时，并在审阅前转换文字。模式脱敏只是业务规则示例，不是安全保证。
用 `wuu plugin dev` 直接运行这个单文件，选择其 `session.sharing@1`，并移除
`wuu.session-sharing.default`。替换不会静默迁移或重新解释现有授权；应选用兼容存储语义，
或停止写入后显式迁移。

SDK 导出 `SESSION_SHARING_DESCRIPTOR`、`requireSessionSharing`、`sessionSharing(host)`。
服务方法为 `preview`、`create`、`resolve`、`read`、`attachment`、`revoke`、`authorize`。
提供者接收宿主提供的有界规范化源记录和已验证的操作者事实。
app-server 客户端使用 `session/share/preview`、`/create`、`/read`、`/attachment/read`、`/revoke`，
并传入 `thread_id`，不传入源数据或权限事实。
