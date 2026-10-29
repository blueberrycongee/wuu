# Personal Google Calendar broker

This internal desktop module implements OAuth and a restricted, read-only Google
Calendar transport. It is not registered with the running application. The
Personal host must supply authenticated session identity and connect its data
methods to `ConnectedCalendarReader` before it can be used in chat.

## Ownership and contracts

- `NewDesktop` uses `credentialstore.NewDesktopStore`, with no file fallback.
  `Config` is host-only. Never serialize it into a renderer, plugin setting,
  model tool argument, or log. A Desktop client secret, if needed, is not an
  authentication boundary; PKCE remains required.
- `Actor` comes from the host's verified session context. Client and model JSON
  must not supply its domain, user space, or session identity. Connection-changing
  methods (`Start`, `Cancel`, `Select`, `Disconnect`) are trusted user-client
  actions, never model tools. They are not exposed by this module's own HTTP server.
  Admission must come from the authenticated owner desktop/remote principal.
  `ownerClient=true`, `registrar=nil`, a `plugin.client.request` boolean, or the
  b56 UserHost `ServeForDevice` path is insufficient: background conversation RPC
  also uses that path. Resolve the authenticated principal before constructing
  Actor; never construct it from payload identity. The read-only model tool may
  separately use its restricted session/user-space identity. Mock Actor tests
  verify this module's scope checks, not production owner authentication.
- One active Google account is allowed per Personal user space. Secure storage
  namespace is `personal.google-calendar.v1`; its key is SHA-256 of the user
  space. The encrypted envelope binds client ID, connection ID, verified Google
  subject and user space. A reconnect cannot replace the account. Disconnect
  first to intentionally choose another account.
- The envelope is changed through the existing `credentialstore.Store.Update`.
  It contains no events. Refresh uses an atomic lease and revision check to
  reject another process's refresh or a late result after disconnect. An active
  lease returns `refresh_busy`; callers can retry later. Tokens are never sent to
  the plugin or renderer.
- Events, cached windows, baselines and cursors remain owned by
  `plugins/personal-calendar/src/index.ts` and `changes.ts`, using their existing
  `host.storage.compare_exchange` key. Do not copy that schema into Personal.
- OAuth `Cancel` and `Close` immediately fence the attempt, then wait for any
  credential commit and conditional cleanup. New OAuth consent creates a new
  connection ID, so cleanup deletes only the cancelled consent, never a newer
  consent. A terminal attempt cannot become connected again. Both methods return
  `credential_cleanup_failed` when local removal cannot be confirmed; callers
  must surface it and must not claim deletion. A non-cancellable OS command can
  delay completion. No code can guarantee deletion while the secure store fails.
- A secure-store envelope alone is never evidence that consent completed.
  A separate nonsecret completion file under the OS user's
  `~/.wuu/personal-calendar/activation-v1` binds client ID, user space and the
  consent's connection ID through its SHA-256 filename. It contains no token,
  Google subject, event data or copied Personal CAS state. The directory is 0700
  and each proof file is 0600. This location follows the OS user, independent of
  WUU_HOME, matching the existing desktop credential boundary.
- Completion atomically renames a prepared proof only after the secure-store
  commit succeeds and a final cancellation check passes. Missing, unreadable,
  corrupt or unsafe proof fails closed as `needs_reauth/authorization_incomplete`;
  no Google probe or token refresh is allowed, even when Google would accept the
  token. Failed cleanup remains visible but cannot reactivate on restart. R3
  records without proof require explicit new consent; they are not auto-migrated.
- Proof rename is the successful completion boundary. A concurrent Cancel that
  loses to this completed transition returns `authorization_already_completed`,
  requiring the explicit Disconnect action; it does not claim cancellation.
  Close fences pending work but preserves already completed connections. Orphan
  proof files have no authority without the matching secure-store connection;
  losing a proof can disable a connection but cannot activate one.
- Host RPC errors must preserve the sanitized `Failure.Code` and optional
  `PersistenceFailed` fields. Never serialize provider errors or classify their
  text. Known authorization failure fences the connection in memory before the
  secure-store write, including when that write fails. Cache readers call
  `Broker.Authorize`, which verifies current access with a bounded Calendar-list
  GET. This prevents a new process from treating an old persisted grant as live.
  Network failure keeps same-owner data explicitly stale; it never proves fresh.
- `Connection.SelectionRevision` changes on calendar selection, independently
  of token refresh. Connected snapshots carry connection and selection identity.
  The existing CAS owner checks every cache/status/feed delivery through the
  reader's `validateCache`; auth/scope changes clear all windows, and unavailable
  authorization validation marks retained windows stale. No extra storage key
  or duplicate cache schema was introduced.
- The UI fences each asynchronous result by operation generation and connection/
  account/user-space/selection identity. External `refresh()` supersedes a busy
  read; old results cannot restore cleared events or previous-account choices.
- `ConnectionViewHost.disconnect` must first fence the Calendar plugin and clear
  its cache, then call `Broker.Disconnect`. On reconnect or selection changes,
  clear the plugin's old cached windows before exposing the new connection.
  No cache, derived data or cached tool result may bypass current broker status.
- `Disconnect` clears secret fields and disables use, then deletes the secure
  entry atomically. A failed deletion is reported, never called successful.
  Optional provider revoke happens after local deletion. `unconfirmed` means
  Google revocation could not be verified; do not report it as revoked. This
  result must be shown in the client, with the Google account permissions link.
  No refresh token is retained for a later revoke retry.
- The OS-user trust boundary is unchanged. This is logical product separation,
  not protection against arbitrary code running as the same OS user.

## OAuth and read limits

`Start` binds `127.0.0.1:0` before returning the system-browser URL. The redirect
is `http://127.0.0.1:<ephemeral-port>/oauth/google-calendar/callback`. Attempts
expire after five minutes. State is single-use, PKCE is S256, and signed Google
ID tokens are checked for issuer, client audience, authorized party, subject,
expiry and nonce using Google's fixed HTTPS signing-key endpoint. No unsigned
identity or user-provided email controls the binding. Identity keys are fetched
for each connection attempt; they are not cached in this first module.

Scopes are exactly `openid`, `calendar.events.owned.readonly`, and
`calendar.calendarlist.readonly` (the Calendar scopes use Google's full URI).
Extra or missing granted scopes fail closed. The list endpoint filters ownership;
`Select` accepts at most eight owned calendars. Connection itself probes the
calendar list only, then requires explicit selection before reading events.

Event reads require RFC3339 boundaries and an IANA timezone, up to 31 × 24 hours.
Only GET list/events requests are available. Events expand recurrences, include
cancellations, and request a limited field projection. Pagination is capped at
10 pages, with repeated page-token rejection and 2,000 normalized event limit.
GET retries are capped at three attempts with jitter and bounded Retry-After;
OAuth POSTs are not retried after uncertain transport outcomes. There are no
writes, watch channels, scheduled syncs or proactive tasks.

## Synthetic verification

From the repository root, using existing Go and Node runtimes:

```sh
npm --prefix plugins ci --ignore-scripts
GOCACHE=/tmp/wuu-calendar-go-cache WUU_CALENDAR_E2E=1 go test -count=1 -race -v ./internal/personalcalendar
GOCACHE=/tmp/wuu-calendar-go-cache go test ./internal/credentialstore
GOCACHE=/tmp/wuu-calendar-go-cache go vet ./internal/personalcalendar
node --test plugins/personal-calendar/test/*.test.mjs
npm --prefix plugins run typecheck
node scripts/build-bundled-plugins.mjs --plugin=personal-calendar --check
make test-policy-check
node plugins/personal-calendar/preview/build.mjs
```

The OAuth tests inject an in-memory credential store and synthetic Google HTTP
transport. The loopback listener is real and requires permission to bind localhost.
The separate ID-token test uses a generated RSA key. None uses a Google account,
system credential lookup, or real consent. The preview includes the real component
and a memory-only fixture, with `connect-src 'none'` and no OAuth configuration.
Opening its HTML cannot connect to Google. These tests do not certify production
OAuth, Keychain prompts, integrated chat, or real Calendar responses.

## Official contracts

- [Desktop OAuth](https://developers.google.com/identity/protocols/oauth2/native-app)
- [Google OpenID Connect](https://developers.google.com/identity/openid-connect/openid-connect)
- [Calendar scopes](https://developers.google.com/workspace/calendar/api/auth)
- [Calendar list](https://developers.google.com/workspace/calendar/api/v3/reference/calendarList/list)
- [Events list](https://developers.google.com/workspace/calendar/api/v3/reference/events/list)
- [Calendar errors](https://developers.google.com/workspace/calendar/api/guides/errors)

The opt-in `TestSignedOAuthToCalendarToolE2E` launches the Node fixture in
`testdata/bridge.mjs`. It verifies signed OAuth and the real loopback callback,
then routes token-free data through the existing Calendar CAS and tool executor.
Set `WUU_CALENDAR_E2E=1`; without it the test explicitly skips because ordinary Go
checks do not install Node dependencies. `WUU_CALENDAR_E2E_OUTPUT` selects the
report/audit directory. All Google HTTP and credential storage remain synthetic.
