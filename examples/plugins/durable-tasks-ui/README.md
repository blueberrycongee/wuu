# Remote durable task view

An explicitly mounted remote-only web reference, separate from the cloud driver
and from Wuu's default desktop composition. No runtime plugin is installed or
started on the display machine. It uses the public SDK's
`ConnectDesktopSessionV1` / `DesktopSessionConnectionV1` contract.

Import `mountDurableTasks` from `view.mjs`, load `style.css`, and call:

```js
const dispose = await mountDurableTasks(element, {
  connectDesktopSession: trustedConnector.connectDesktopSession,
  route: explicitlySelectedRemoteRoute,
  sessionId: authorizedCloudSessionId,
});
// When the view closes:
await dispose();
```

The trusted connector must authenticate the exact remote host and implement the
SDK's attach, detach and RPC-generation semantics. This reference does not ship
a connector, credential form, transport tunnel or desktop navigation placement.
Do not pass a local connection disguised as a remote route. Install/select the
cloud provider on the execution host separately. Default desktop providers and
composition are unchanged.

Approval and denial controls intentionally remain disabled: this metadata-only
view cannot show the intended action. Complete decisions through a separate
authorized review surface. The transport model supports authenticated decision
RPCs and uncertain-outcome handling, but that is not a complete approval UX.

The remote host must support `durable/tasks/list`, `durable/waits/list`, and
`durable/waits/decide`. The host, not a supplied session ID, authorizes reads and
human decisions. Decisions do not authorize execution outside ordinary policy.
The connector owns reconnect/cursors. After attachment the view reads fresh
replace-all snapshots, displays the host task revision, and refreshes every five
seconds while mounted. Disconnect disables decisions and preserves a marked
stale snapshot. Unmount detaches the view without shutting down remote work.
An uncertain decision is never automatically replayed.

Only explicit task metadata and wait status/expiry are rendered. Questions,
prompts, arguments, outputs and free-form errors are omitted. This is a metadata
monitor, not a substitute for reviewing the actual action in an authorized
approval surface before deciding. Input/event waits never show decision controls. Host expiry is authoritative; the display
never manufactures a decision from its wall clock. Missing usage is not estimated;
recovery gaps are labeled unavailable or incomplete rather than shown as a full total.

## Controlled rendered acceptance

Serve this directory with an ordinary static HTTP server and open
`acceptance.html?test=1` in Chromium. `body[data-acceptance="passed"]` and
`#result` contain the behavioral result. This runs the actual view against a
synthetic implementation of the public connector; it does not prove remote
transport authentication or production host integration.

For screenshots open `acceptance.html?theme=light&font=15`, varying `theme`
(light/dark), `font` (15/22) and viewport width (1080/360). Optional
`state=empty|error|disconnected` exercises page states. The fixture includes long
identifiers, parent/child tasks, failed tasks, pending/approved/expired waits,
and private sentinel fields that must never appear. See `acceptance-cases.md`.
