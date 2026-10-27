# Notion workspace

The optional **Notion** plugin adds a ribbon entry for the Notion website and document tools that call Notion's official API directly. It is distributed as a separate source package, not bundled with Wuu. From a repository checkout, install it explicitly:

```sh
wuu plugin install ./plugins/notion-workspace/package
wuu plugin approve notion-workspace
```

Then configure it in **Skills & Plugins**. No Notion CLI is installed.

## Open the website

Choose **Notion** in the ribbon. The page uses Wuu's desktop browser session and works without an API token. Sign in to Notion in that page when necessary. An existing website login does not authorize the document API.

The plugin's **Notion website** setting accepts an HTTPS `notion.so` or `notion.com` URL, including subdomains. After changing this address, disable and re-enable the plugin, or reload Wuu. The website view is available in the main desktop workbench, not remote web/mobile clients or popped-out conversation windows. Disabling the plugin closes its website surface and removes its tools; it does not sign out of the shared browser session.

## Configure document access

1. Create an internal integration in the [Notion developer portal](https://app.notion.com/developers/connections). Grant only the content capabilities you need: read for reading/search, insert for creating/appending, update for edits.
2. In Notion, use a page's **••• → Add connections** menu to connect the integration to the pages it should access.
3. In the plugin's settings, save its internal integration token in **Notion internal integration token**. This is a secret field backed by the host's credential store, not ordinary plugin settings. Never put the token in chat, a project file, or the website URL.

The host injects the token only into HTTPS requests to `api.notion.com`; the plugin runtime does not receive the credential. API requests do not use or extract website cookies. This example does not implement public-integration OAuth, token refresh, or browser-to-API login conversion. Removing the saved token stops API access but does not revoke it at Notion; revoke it in Notion when needed.

## Document tools

- `search_pages`: one page of shared-page title matches, with an opaque continuation cursor. It is not full-text search or a complete workspace inventory.
- `read_page`: page metadata and one page of direct children. Nested children, later pages, and full paginated property values are not implicitly fetched.
- `read_children` and `read_block`: inspect specific blocks and follow children explicitly. Pass `next_cursor` unchanged as `start_cursor`; page sizes are 1–100, default 50.
- `create_page`: a titled child page under an explicit parent page UUID, with up to 100 simple blocks. Database rows and templates are outside this example's scope.
- `append_blocks`: append 1–100 blocks at the end, preserving existing content.
- `update_block_text`: replace one supported block's rich text with plain text. This removes that text's formatting, mentions, and links; other block fields and nested children remain.
- `rename_page`: change only the existing title property.

Writes support paragraphs, three heading levels, bulleted and numbered items, checkboxes, quotes, and toggles. Each text/title is limited to 2,000 characters and each encoded write to 450 KB. Nested block creation, arbitrary property writes, deletion, moving blocks, and whole-page replacement are not exposed. Use UUIDs returned by Notion; website URLs are display links and are not parsed into API IDs.

Editing text or a title requires the exact `last_edited_time` from a recent read. The plugin reads again before writing and rejects an already-changed record. This is a best-effort preflight, not an atomic lock: another editor can still change the record between requests.

A successful write reports what the API response verified. Create verifies the returned title and parent; child content is acknowledged but not read back. Append and edits compare the returned block text or title with the request. These response checks are not independent read-back verification. If a write's response is lost, the result reports `outcome: unknown`; inspect its destination before retrying to avoid duplicate pages or content. A successful response that fails verification reports `applied_unverified` and must not be blindly repeated.

Requests use `Notion-Version: 2026-03-11`, a 45-second operation budget, and a serialized runtime lane. For 429/529 responses, the plugin honors `Retry-After`, retries at most twice when the wait fits its short retry budget, and returns the remaining delay otherwise. It does not replay network failures or ambiguous server errors on writes. A 401 asks for a replacement token; a 403 can reflect capabilities, access, or workspace limits; a 404 can mean a missing, trashed, or unshared record.

## Verification and development

The repeatable mocked-API acceptance run exercises the built JSONL runtime and its host credential-request boundary. It makes no request to Notion and uses no real account or token:

```sh
npm --prefix plugins/notion-workspace run build
npm --prefix plugins/notion-workspace run test:e2e
```

It writes a JSON evidence report under `.tmp/notion-workspace-e2e/`. This verifies local contracts, not real-account authorization, content fidelity across every Notion block type, or website login behavior. Desktop rendering and real-account acceptance remain separate checks.

The light/dark SVG mark is sourced from [Simple Icons](https://github.com/simple-icons/simple-icons/blob/develop/icons/notion.svg), distributed under CC0; the bundled assets include its license and source notice. Notion's trademark rights remain with its owner. The plugin is not endorsed by Notion.

Official references: [authorization](https://developers.notion.com/docs/authorization), [versioning](https://developers.notion.com/reference/versioning), [search](https://developers.notion.com/reference/post-search), [create page](https://developers.notion.com/reference/post-page), [read children](https://developers.notion.com/reference/get-block-children), [append children](https://developers.notion.com/reference/patch-block-children), [update block](https://developers.notion.com/reference/update-a-block), [update page](https://developers.notion.com/reference/patch-page), and [request limits](https://developers.notion.com/reference/request-limits).
