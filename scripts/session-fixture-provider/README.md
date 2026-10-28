# Synthetic session fault provider

This test-only process provides `state.documents@1` through Wuu's public JSONL
plugin protocol and delegates storage to `persistence.NewService(..., "sqlite")`.
It does not replace SQLite with an in-memory or JSON-file implementation.

## Consumer contract

1. Use a disposable fixture home. Create `.wuu-synthetic-fixture` in that home
   containing exactly `wuu-session-fixture-v1\n` before installation.
2. Build once: `go build -o <fixture-binary> ./scripts/session-fixture-provider`.
3. Write the fixture's normal `config.json`, then run
   `<fixture-binary> install --home <fixture-home>` before starting Wuu.
   Installation grants the fixture package and selects `fixture.session-documents`
   for `state.documents@1` in that home's configuration. Other configuration
   fields are preserved. Normal homes without the marker are rejected.
4. Set `WUU_HOME` to this home for every app-server and read/seed helper. All
   consumers use the selected service; do not open the backing database directly.
5. Atomically replace `<home>/session-fixture-provider/faults.json` with:

   ```json
   {"version":1,"faults":[{"kind":"edit-binding","session_id":"target-thread"}]}
   ```

   Fault kinds and selectors:

   | Kind | Required selector | Rejected mutation |
   | --- | --- | --- |
   | `edit-binding` | `session_id` | Existing session's nonempty `engine_ref` changes |
   | `fork-create` | none | A new session document is inserted |
   | `native-boundary` | `session_id` | History row in that thread receives nonempty `engine_turn_id` |
   | `ack-history` | `client_id` | New history row with that client ID |
   | `claim` | `id` | That held input changes from pending to dispatching |
   | `release` | `id` | That held input changes from dispatching to pending |

   Remove this control file to clear all faults. The error code is
   `invalid_request`, with a `synthetic commit rejected before apply:` message.
   This is a definite pre-apply rejection, not an unknown I/O outcome.
   Revision conflicts and existing operation receipts take precedence over faults.
   A repeated committed operation retains its receipt even while a fault is active.

The selected provider's SQLite authority is under
`<home>/session-fixture-provider/store`. A separate cross-process fixture gate
covers fault inspection plus backend commit, so two app-servers share real CAS and
receipts. Marker, control, manifest and storage paths reject symlink components.

Each matching rejection appends its transaction ID, revision, rule and document
key to `<home>/session-fixture-provider/rejections.jsonl`. Rules remain armed
across repeated attempts and provider process replacements until removed.
