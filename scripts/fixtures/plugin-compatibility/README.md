# Historical plugin process fixture

`2026-10-22/` is an immutable compatibility baseline from public Wuu commit
`08000266921da772f2ca999b8687f34fb4dbc911`, under the retained MIT license.
`todo.mjs` is the exact checked-in distributed bundle. `sdk/` contains the
historical package's `npm pack` allowlist, compiled once using the TypeScript
5.9.3 recorded in its original lockfile. This is repository-source provenance,
not evidence of an npm registry publication.

`provenance.json` records source and retained artifact SHA-256 digests. Normal
builds and tests never rebuild this SDK. To reproduce into a **new** directory:

```sh
node scripts/capture-plugin-compatibility.mjs /tmp/new-wuu-compatibility-capture
```

This requires that historical Git object and TypeScript 5.9.3 in the normal SDK
tooling path. Review changes in a new baseline; do not replace the old one to make
a failing test pass. Capture preserves UTF-8 files instead of committing an npm
tarball, so normal text review/publication can retain all runtime bytes.

`probe.mjs` uses that retained package for the original settings get/list call
shapes and lifecycle. `negotiation.mjs` is a separate independent wire fixture for
version acceptance/rejection; it deliberately imports no SDK. Run the current-host
gate with `node scripts/plugin-compatibility-e2e.mjs` from the repository root.
See the maintained [compatibility contract](../../../docs/en/customize/plugin-compatibility.md).
