# Execution authorization acceptance fixture

Failure cases are enumerated in `cases.json` before fixture implementation. This
suite must exercise the real host gateway and shipped Personal plugin, not a
standalone JSONL mock. Independent providers compile outside the checkout using
only a packed `@wuu/plugin-sdk` and Node built-ins. Synthetic credentials and an
isolated local state directory are mandatory; no real account is used.

A login token is connection authority. The independent grant provider separately
owns durable account validity keyed by the host-attested issuer, tenant, subject
and identity kind. Revoking the login token alone is not account disablement;
account disablement and grant revocation deny subsequent unattended effects.

Run from the repository root after building the current bundled Personal runtime:

```sh
node scripts/execution-grant-acceptance.mjs
```

`WUU_GRANTS_BINARY` selects the CLI used only to install the independent packages.
`WUU_GRANTS_TRANSPORT` can reuse a freshly built `transport.go` binary; otherwise
this harness builds it with the repository version. `WUU_GRANTS_SDK_TARBALL` can
reuse an already packed SDK. `WUU_GRANTS_OUTPUT` selects the evidence directory.
The harness never commits, pushes, changes real-user state, or contacts accounts.

Each run saves its exact composition, SDK/binary hashes, redacted wire transcript,
provider-owned grant ledger, authority audit, consumer audit, command output and
`report.json`. A failed prerequisite stops dependent checks; an absent check is
**unrun**, not passed. The transport bootstrap uses `internal/sdk` only to attach
and detach independent pipes to the production UserHost. It exclusively invokes
`ServeProviderAuthenticated`; it never creates a principal or calls trusted-local
`Serve`. Providers compile outside the checkout and import no internal packages.

Native rejection covers a provider-authenticated external account. It does not
claim actual mobile pairing, logout-provider interoperability, production OIDC,
or UI/device acceptance. The suite's independent consumer probes gateway security
boundaries; the shipped Personal task case separately verifies product wiring.
