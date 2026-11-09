# Explicit Personal clipper

Load this directory as an unpacked Chromium Manifest V3 extension. Click its
button on the page to share; it exports **only that URL, title and selected text**.
Open Personal → Connections → Share and import the `.wuu-share.json`, then press
Save to main chat. No account token, HTTP endpoint, history permission or broad
website access is involved. This deliberate file handoff works without exposing a
local unauthenticated receiver. For a bookmark, paste its URL in the same panel.
It is not a full-bookmark-library importer or an automatic page scraper.

The toolbar gesture grants `activeTab` only temporarily. `downloads` writes the
chosen local export; it is not used to inspect download history. Exported files
can contain private selected text: choose their destination deliberately.

Unpacked installation and a real Chromium toolbar click remain manual acceptance
steps; cloud tests cover the receiver contract, not an installed extension.
