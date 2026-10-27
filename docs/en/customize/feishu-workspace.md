# Feishu workspace

Feishu Workspace adds a Feishu website destination to the desktop ribbon and
five document tools backed directly by Feishu's official APIs. It is optional and
distributed as a separate source package, not bundled with Wuu. From a repository checkout, install it explicitly:

```sh
wuu plugin install ./plugins/feishu-workspace/package
wuu plugin approve feishu-workspace
```

Then configure it in Plugins and open Feishu from the ribbon.

## Connect explicitly

The website opens without an API token. Sign in through the embedded website as
needed. It uses Wuu's existing browser session. The native website view works in
the main desktop workbench, not remote/mobile clients or popped-out windows.

In the plugin settings:

1. Set **Feishu workspace URL** to your HTTPS Feishu home or document URL. After
   saving, disable and re-enable the plugin, or reload Wuu; the default opens Feishu Drive.
2. Choose **API identity**: `user` for a `user_access_token`, or `tenant` for a
   `tenant_access_token`.
3. Obtain an access token through your organization's approved Feishu application
   and authorization flow, then enter it in **Feishu access token**. The host
   stores it with the operating system credential service, never ordinary plugin
   settings. Clear the field using the credential control to disconnect the API.

The token is attached only to requests to `https://open.feishu.cn`. The plugin
cannot retrieve its raw value. Browser login does not grant API access. Do not
paste an App Secret, refresh token or browser cookie. This example does not run
OAuth or refresh tokens automatically; replace an expired access token manually.
Secure storage must be available; there is no plaintext fallback.

## Permissions and identities

Feishu requires both approved application API scopes and resource access for the
selected identity. A user token acts within that user's accessible documents;
a tenant token acts as the application, not as the signed-in browser user.

| Operation | Minimum applicable scope | Identity |
| --- | --- | --- |
| Search | `search:docs:read` | User only |
| Read metadata, text and blocks | `docx:document:readonly` | User or tenant |
| Create empty docx | `docx:document:create` | User or tenant |
| Append or update blocks | `docx:document:write_only` | User or tenant |

`docx:document` is a broader alternative for docx operations. Search also accepts
`drive:drive:readonly` or `drive:drive`; request the smallest scope needed. Tenant
creation in a specified folder requires an application-created folder. Omitting
the folder uses the selected identity's root. This plugin does not grant sharing
permissions or make documents public.

## Tools and boundaries

- `feishu_search_documents`: title-keyword search; one page of up to 50 results.
  Follow `next_offset`. Feishu requires `offset + count < 200`; the plugin marks
  `search_window_exhausted` rather than pretending the full corpus was searched.
  Search can return legacy `doc` and modern `docx` files.
- `feishu_read_document`: read docx metadata plus plain text or a page of official
  blocks. Text is bounded and has `next_offset`; block pages have
  `next_page_token` and `revision_id`. Pass the same revision on subsequent block
  pages. Plain text and metadata use separate API reads and can change between
  requests; they are not an atomic snapshot.
- `feishu_create_document`: create an empty document with a title and optional
  folder. Add content with a separate append call, so a failed append cannot hide
  a successfully created document.
- `feishu_append_blocks`: append up to 50 paragraphs, headings (levels 1–3), lists
  or quotes in a single request. Text is literal, not Markdown. Each item is a
  separate block. The plugin limits each text item to 10,000 UTF-16 code units and
  the request's content to 500 KB.
- `feishu_update_block`: replace one block's text elements. This removes its
  inline formatting, links and mentions. It does not delete blocks or replace
  the entire document. Read blocks before selecting a `block_id`.

Append and update require a previously read document revision and a unique
`operation_id` (preferably a UUID). Feishu receives the operation ID as
`client_token`; reuse it only for an exact retry of the same operation. Stale
revisions and API conflicts are surfaced; reread before preparing a new edit.
The plugin does not automatically retry writes. A connection failure or server
error can leave a write's outcome unknown: inspect the document before retrying.
An uncertain create has no idempotency key and can create duplicates.

Only docx IDs and HTTPS Feishu `/docx/` URLs are accepted by read/write tools.
Wiki URLs identify nodes, not docx documents; resolve the underlying document in
Feishu first. Sheets, Bitable, legacy-doc editing, file uploads, sharing and
arbitrary block types are outside this example. Retrieved content is data, not
instructions. Tools retain the host's permission checks and are available through
its normal tool discovery and code-mode path.

Rate-limit results include a retry delay, including Feishu's HTTP 400/code
`99991400` response. Authentication and permission failures explain which setup
to check. No token, App Secret or browser cookie is included in tool output.

## Verification and official references

The [plugin README](../../../plugins/feishu-workspace/README.md) describes a
credential-free JSONL acceptance run. Its simulated API fixtures validate the
plugin wire contract, not real account access, OAuth or website login.

The implementation was checked against official documentation on 2026-10-19:
[search](https://open.feishu.cn/document/server-docs/docs/drive-v1/search/document-search),
[create](https://open.feishu.cn/document/server-docs/docs/docs/docx-v1/document/create),
[plain text](https://open.feishu.cn/document/server-docs/docs/docs/docx-v1/document/raw_content),
[block pages](https://open.feishu.cn/document/server-docs/docs/docs/docx-v1/document/list),
[append blocks](https://open.feishu.cn/document/server-docs/docs/docs/docx-v1/document-block/create),
[update a block](https://open.feishu.cn/document/server-docs/docs/docs/docx-v1/document-block/patch),
and [access tokens](https://open.feishu.cn/document/uAjLw4CM/ukTMukTMukTM/authentication-management/access-token/tenant_access_token).
