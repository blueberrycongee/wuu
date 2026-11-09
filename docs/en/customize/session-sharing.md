# Restricted session sharing

`session.sharing@1` provides a recipient-specific **projected view**, not remote
control or raw session history. It is an opt-in reference capability, not a
sharing product enabled by the default desktop or headless composition. Select
`wuu.session-sharing-access` and `wuu.session-sharing.default` explicitly. An external
provider can replace its storage, format and business policy through the same
public service. No built-in fallback runs when the selected provider fails.

For example, extend an existing version-4 composition with this ordinary patch:

```json
{"modules":{"wuu.session-sharing-access":true,"wuu.session-sharing.default":true}}
```

Put the patch in the composition's `patches` array. Omitting or disabling the
provider leaves sharing unavailable; it does not affect unrelated capabilities.
The endpoint and CLI below exercise the public contract. They do not provision
a hosted sharing service, an identity system, or a default-product sharing UI.

## Publish a reviewed view

Use the supported `wuu session-share` command from the session's workspace. It
uses the ordinary authenticated app-server boundary; it does not open a second
session database. The local command is an owner operation, not a way to
impersonate recipients. Obtain message IDs from the owner's conversation history.

```sh
wuu session-share preview '{"thread_id":"THREAD","selection":{"message_ids":["MESSAGE"]}}'
```

Review the returned `document` and save `review_digest`. Text is not guaranteed
to be secret-free. The allowlist removes reasoning, raw input, tool arguments,
paths, engine state and arbitrary metadata; it cannot determine whether an
ordinary sentence contains confidential information. Review attachment contents
separately before selecting them.

```sh
wuu session-share create '{"operation_id":"YOUR_STABLE_OPERATION_ID","thread_id":"THREAD","selection":{"message_ids":["MESSAGE"]},"mode":"snapshot","recipients":[{"kind":"user","issuer":"YOUR_VERIFIED_ISSUER","tenant":"YOUR_TENANT","subject":"RECIPIENT_SUBJECT"}],"expires_at":"FUTURE_RFC3339_TIMESTAMP","reviewed":true,"review_digest":"PREVIEW_DIGEST"}'
```

Replace the placeholders, including a future expiry. Recipient identities must
match identities established by the transport's selected identity provider, not
an email address inferred by the client. Creation fails if selected content has
changed since preview. Use the same `operation_id` and request intent to retry an uncertain create
acknowledgement, including after restart. The durable receipt returns the same
grant; changed intent conflicts. A retry never renews an expired or revoked grant.
If the original session was removed, add `recover_only:true` to the same create
request to recover an existing receipt without loading source. This cannot create
a new empty grant.

## Scope and lifecycle

- `message_ids`, `tool_result_ids` and `attachment_ids` are separate explicit
  allowlists. Empty or missing lists reveal nothing. Selecting a message never
  selects its tool results or attachments.
- `after` is inclusive and `before` exclusive. They intersect the ID selectors.
  Records without a known timestamp do not pass a time-constrained selection.
- Attachment IDs are `ITEM/image/INDEX`, `ITEM/file/INDEX`, or
  `ITEM/result/INDEX`. Only inline stored bytes are supported in this version;
  external artifact references and filesystem paths are not followed.
- `snapshot` freezes the selected content at creation. `live` rereads the selected
  IDs and requires `live_updates:true`, explicitly consenting to subsequent edits
  of those items. Newly appended message IDs are **not** automatically included.
- Online reads, including each attachment fetch, recheck recipient, session,
  expiry and revocation. A live read resolves the grant before obtaining source
  data and checks again before returning the projection. Poll the read endpoint
  for changes; this version does not subscribe recipients to raw thread events.
- A downloaded snapshot or attachment is an independent copy. Revoking online
  access cannot recall it or undo bytes already delivered before revocation.

```sh
wuu session-share read '{"thread_id":"THREAD","share_id":"SHARE"}' > reviewed-copy.json
wuu session-share attachment '{"thread_id":"THREAD","share_id":"SHARE","attachment_id":"MESSAGE/image/0"}'
wuu session-share revoke '{"thread_id":"THREAD","share_id":"SHARE"}'
```

The view schema is `version:1`, with inert text and attachment metadata. Never
render embedded URLs as automatically fetched resources. Use the authenticated
`session/share/attachment/read` method for bytes; there is no public bearer URL.
A view is bounded to 2 MiB and 2,000 items, an attachment to 8 MiB. Narrow an
oversized selection instead of bypassing the projection.

## Authentication and action separation

All `session/share/*` requests go through `resource.authorization@1`. The default
resource policy delegates recipient reads to the selected sharing provider's
`authorize` method. The request uses a transport-verified principal; a JSON
`actor`, `principal` or `owner` claim cannot replace it. The grant ID is a resource
identifier, not authentication. Read grants do not authorize raw `thread/resume`,
`thread/content/read`, `artifact/read`, workspace access, comments, session input,
or execution. A different selected authorization policy must explicitly grant
any separate send or execute action; no comment-to-execute implication exists.
The default sharing implementation does not implement commenting or execution.

## Replace the provider

[`examples/plugins/restricted-session-sharing.ts`](../../../examples/plugins/restricted-session-sharing.ts)
is an independent TypeScript implementation using only the public SDK and
`state.documents@1`. It owns a separate durable namespace, limits expiry to
24 hours and transforms text before review. Its pattern redaction is an example
business rule, not a security guarantee. Run it as a single local TypeScript file
with `wuu plugin dev`, select its `session.sharing@1` service and remove
`wuu.session-sharing.default`. Existing grants are not silently migrated or
reinterpreted by a replacement; choose compatible storage semantics or migrate
explicitly with writers stopped.

The SDK exports `SESSION_SHARING_DESCRIPTOR`, `requireSessionSharing` and
`sessionSharing(host)`. The service methods are `preview`, `create`, `resolve`,
`read`, `attachment`, `revoke` and `authorize`. Provider requests contain bounded
canonical source records and verified actor facts from the host. App-server
clients use `session/share/preview`, `/create`, `/read`, `/attachment/read` and
`/revoke`, with `thread_id`; they never supply source data or authority facts.
