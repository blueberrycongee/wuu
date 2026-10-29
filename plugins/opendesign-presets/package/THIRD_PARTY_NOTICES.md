# Third-party notices

This package contains material from [OpenDesign](https://github.com/nexu-io/open-design).
The exact revision and SHA-256 digest of every imported file are recorded in
`UPSTREAM.json`. The importer copies committed files without rewriting their
contents from `design-systems/`, `design-templates/`, `craft/`, `skills/`,
and `prompt-templates/`, plus the upstream README and license.
Repository-only configuration listed in `excludedFiles` is omitted from the package.

Imported files reside in `skills/opendesign/assets/upstream/`. OpenDesign's
root license is Apache-2.0; its full text is retained there as `LICENSE`.
Individual resources can carry their own licenses and attribution, which
remain with those resources. Those terms continue to apply; the Wuu adapter's
MIT license does not relicense upstream material.

Additional provenance is recorded in upstream design-system manifests,
`source/` evidence, template license files, prompt `source` fields, and the
original READMEs. In particular:

- `design-systems/README.md` credits VoltAgent/awesome-design-md,
  bergside/awesome-design-skills, tw93/kami, and
  Tom-Opencart/tom-modern-html-style-rule.
- `craft/README.md` credits Refero Design's MIT-licensed refero_skill.
- Template- and skill-local licenses and source comments identify other authors.

Retain the applicable notices and licenses when redistributing copied
resources. Brand-inspired design systems do not imply affiliation or
endorsement. Remote images, fonts, libraries, and service integrations
referenced by a resource are not downloaded or licensed by this adapter.

The Wuu-authored manifest, skill adapter, catalog script, and build tooling
are covered by the Wuu MIT license in the package root. This plugin packages
reusable presets; it does not include the OpenDesign application runtime.
