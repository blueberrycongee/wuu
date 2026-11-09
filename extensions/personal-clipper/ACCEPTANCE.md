# Share/capture implementation status

- Browser: unpacked Manifest V3 extension exports the explicitly chosen current
  URL/title/selection, with `activeTab`, `scripting`, `downloads`; no history or
  bookmarks-list permission. Personal imports the capture file only after the
  owner presses Save. Pasted bookmark URL and text are supported too.
- Desktop receiver: strict bounded payload, forbidden URL schemes/credentials and
  local paths rejected. Imported JSON cannot claim raw bytes or arbitrary attachment
  references through the UI. Ordinary file intake stays on existing chat upload.
  Unknown delivery keeps the same capture ID for existing host deduplication.
- Android: `SEND` / `SEND_MULTIPLE` for text, images and PDF, temporary `content://`
  grants only, 4 attachments / 3 MB total, 64 KB text. Receives into an already
  connected Personal main chat's empty draft; does not auto-send. Cold-start or
  disconnected receiving reports reconnect-and-share-again, rather than queueing
  private material into an unknown future account. Existing pending drafts are
  never overwritten. Existing message upload and durable receipt retry path is
  reused when the user presses Send.
- iOS: Share Extension source, embedded Xcode target, App Group entitlements,
  account/host-bound local inbox and containing-app intake are implemented.
  The owner first enables a 10-minute intake lease in the Personal activity menu;
  the extension stores a bounded local envelope, never credentials or a network
  request. Back in the same account/host main chat, Receive stages it for review.
  Only Send delivers it. The share UUID is retained as the message request ID,
  including durable pending-message recovery; acknowledge/removal happens only
  after the host returns a receipt. Account change revokes the intake lease.
  Requires the developer's signed App Group capability on both targets. No Apple
  developer-account settings or provisioning profiles were changed here.

## Verification

`node --test plugins/personal/test/share-capture.test.mjs`: 3 tests passed.
Focused strict TypeScript check of receiver and UI: passed.
`PersonalShareCapture.test.tsx`: 2 jsdom lifecycle tests passed (exact uncertain-retry identity, successful clearing, no send on Clear, duplicate-click suppression, late completion after unmount).
`make test-policy-check`: passed.
`node --check extensions/personal-clipper/background.js`: passed.

`node desktop/scripts/personal-share-e2e.cjs` is a repeatable synthetic E2E
covering uncertain replay identity, successful clearing, clear-without-send,
unsafe import rejection, and light/dark/wide/narrow/large-font rendering. This
cloud's Chromium could not start (`socket() Operation not permitted`), so no
interaction pass or screenshot is claimed.

Android Gradle unit tests could not download the official wrapper due to DNS
failure; the requested sandbox escalation failed in infrastructure before the
command ran. No Android compilation, simulator, or real-device claim is made.
No real user account, browser history, clipboard or attachment was accessed.

Swift tests were added first for account isolation, lease expiry, exact acknowledgement, path rejection and payload bounds. No Swift/Xcode toolchain is installed here, so they were not run. Plist XML parses and Xcode object IDs are unique; these checks do not establish iOS compilation or signing.
