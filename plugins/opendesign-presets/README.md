# OpenDesign Presets

An optional Wuu plugin with one `opendesign` skill and a searchable, pinned copy
of OpenDesign's reusable assets. User-facing behavior and installation are
covered in the [English guide](../../docs/en/customize/plugins.md#opendesign-presets)
and [中文指南](../../docs/zh-cn/customize/plugins.md#opendesign-预设).

## Build and package

From the Wuu repository root, with Node.js 22.7+, Git, tar, and the Wuu CLI:

```bash
npm ci --prefix plugins/opendesign-presets
npm --prefix plugins/opendesign-presets run build
wuu plugin validate plugins/opendesign-presets/dist
wuu skills lint plugins/opendesign-presets/dist/skills
wuu plugin pack --output plugins/opendesign-presets/opendesign-presets-0.1.0.zip plugins/opendesign-presets/dist
```

The default build fetches the public repository at the commit in `upstream.json`.
An existing checkout can supply the same committed objects without downloading:

```bash
node plugins/opendesign-presets/build.mjs --source /path/to/open-design
```

Both paths use the pinned commit; local edits and untracked files are not
imported. Only the generated `dist/` is replaced. The distributable contains no
symlinks or references to the build machine's checkout, and needs no OpenDesign
installation. Node is needed only for the optional catalog search script; the
skill can also read the JSON catalog with ordinary file tools.

`package/` contains the Wuu-authored adapter. The generated package combines it
with design systems, complete template and workflow directories, craft references,
and media prompt recipes. It preserves upstream source contents, associated assets,
licenses, and attribution. Upstream repository metadata excluded by Wuu's packer
is explicitly listed in `upstream.json`.

`UPSTREAM.json` records the source revision and each imported file's SHA-256.
The generated `references/catalog.json` indexes 152 design systems, 115 templates,
169 supporting workflows, 11 craft references, and 106 media prompts at the current pin. Assets stay outside
Git and outside Wuu's embedded defaults. Template scripts and third-party services
are reference material; installing this plugin does not execute or configure them.

To refresh, inspect a new upstream commit, update `upstream.json`, rebuild,
review the counts and license changes, run validation and representative catalog
queries, and bump the plugin's own version before distributing an update. Keep
`package/plugin.json` and this directory's `package.json` versions aligned.
