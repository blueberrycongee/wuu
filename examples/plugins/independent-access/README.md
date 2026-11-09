# Independent identity and resource policy fixture

This provider imports only `@wuu/plugin-sdk`. It independently verifies synthetic
Ed25519 JWTs against a pinned public JWK and evaluates namespace-qualified grants
from its own state file on each call. It is not a production OIDC discovery,
login, token-rotation or SAML implementation.

Run the actual-executable acceptance from the repository root:

```sh
go build -o bin/wuu ./cmd/wuu
node packages/plugin-sdk/test/resource-access-package.mjs
```

The runner packs the public SDK, compiles this provider outside the checkout,
installs it normally and exercises authenticated JSONL calls. Evidence contains
no bearer tokens or signing keys. See the maintained [identity and resource
access contract](../../../docs/en/integrations/resource-access.md).
