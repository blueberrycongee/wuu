# Action Notes

A desktop-only example of `view.title` and `conversation.message.actions` command placements. Refresh is scoped to this plugin's notes view. Save captures only the clicked message's displayed text into a deduplicated, plugin-owned temporary list.

Notes are memory-only: successful reload or disable clears them. No runtime process, credentials, external services, or automatic view placement are involved. Nothing is enabled by adding this example to the checkout.

## Build and test

From this directory in a matching Wuu checkout:

```bash
npm install --prefix ../../../packages/plugin-sdk
npm run build --prefix ../../../packages/plugin-sdk
npm install
npm test
wuu plugin validate .
wuu plugin test .
wuu plugin dev .
```

The JavaScript entry imports SDK types through JSDoc only and uses the host's React instance. It needs no bundler. Type-checking uses the matching SDK, not private desktop imports.

After explicitly loading it, open **Action notes** from Workspace Tools. On a synthetic conversation message, choose **Save to action notes**. Use **Refresh action notes** on the notes view. Empty or streaming messages cannot be captured. Capturing the same message twice keeps one entry.

The automated test exercises placement filters, missing command context, duplicate capture, refresh, and cleanup. It does not render Desktop. Verify keyboard focus, narrow layout, themes, and reload/disable in Desktop separately. Generation cleanup removes buttons and subscriptions; the example also refuses any retained callback after cleanup.

See the [English command-actions guide](../../../docs/en/customize/plugin-command-actions.md) or [中文指南](../../../docs/zh-cn/customize/plugin-command-actions.md) for the public contract. For persistent notes, use plugin-owned storage rather than treating this in-memory list as durable data.
