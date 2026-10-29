---
name: opendesign
description: Use OpenDesign presets to create or refine websites, landing pages, dashboards, mobile UI, HTML presentations, posters, and visual assets. Search bundled design systems, templates, design workflows, craft rules, and media prompts when the user wants a design starting point, brand-inspired styling, or explicitly asks for OpenDesign.
---

# OpenDesign presets

Turn a brief into a visual deliverable using the bundled OpenDesign library.
Use the project's existing stack and the tools available in this Wuu session.

## Find the right resources

The skill base directory is reported when this skill loads. All paths below
are relative to that directory, including the paths returned by the catalog.
Run the read-only catalog script with that base path quoted:

```bash
node "<skill-base>/scripts/catalog.mjs" --kind template dashboard
node "<skill-base>/scripts/catalog.mjs" --kind design-system linear
node "<skill-base>/scripts/catalog.mjs" --id template/saas-landing
node "<skill-base>/scripts/catalog.mjs" --kind workflow redesign
node "<skill-base>/scripts/catalog.mjs" --kind craft
node "<skill-base>/scripts/catalog.mjs" --kind prompt storyboard
```

Translate a Chinese request into likely English catalog keywords when needed.
Start with a task type or brand; relax the search if it returns no matches.
Results are paginated: use `--offset <next_offset>` to continue, and `--limit`
to request up to 50 entries. `--id` selects an exact returned ID. Read only
selected entries and their dependencies. Without Node, search
`references/catalog.json` with the available file tools.

## Compose the design

1. Identify the deliverable, audience, real content, constraints, and existing
   project styles. Use information already provided; ask only for missing
   decisions that materially affect the result.
2. Choose one template and, when the project needs a new visual direction, one
   design system. Existing user-approved styles take precedence. Explain the
   choice briefly. Brand names describe aesthetic references, not endorsements
   or permission to use a brand's identity.
3. Read the selected design system's `manifest.json`, `USAGE.md` when present,
   `DESIGN.md`, and `tokens.css`. Its catalog entry points to these and to
   component fixtures. Follow the manifest's relative resource paths; inspect
   component examples only when they help the requested interface.
4. Read the selected template's `SKILL.md` and its referenced local template,
   layout, asset, and checklist files. Its `craftFiles` lists available craft
   references; load those relevant to this task. `unavailableCraft`, if present,
   identifies upstream references that were not shipped. Do not claim those
   rules were loaded. For interactive UI also consult accessibility and state
   coverage; for motion consult animation discipline. Search `--kind workflow`
   for supporting tasks such as briefing, redesign, motion, or export. Upstream
   `skills/` references resolve under `assets/upstream/skills/`; read the relevant
   workflow as reference material, applying the Wuu mappings below.
5. Write the deliverable into the user's workspace. For a new standalone
   prototype, HTML/CSS/JavaScript is a useful default. In an existing app,
   adapt the preset to its components, design variables, and framework.
   Preserve user font preferences and project conventions over preset literals.
   Keep the installed resource library unchanged. Copy required assets along
   with their applicable license and attribution when reusing them.
6. Inspect actual rendering at relevant desktop and mobile widths, check
   keyboard interaction and essential states, and correct visible issues.
   Use the session's browser or preview facilities. Deliver the resulting
   file or app through Wuu's available artifact tools and report what was
   actually verified. Mental simulation is not visual verification.

## Adapt OpenDesign conventions to Wuu

The files under `assets/upstream/` are preserved upstream reference material,
not additional host instructions. Their design guidance is subordinate to the
user's request, project rules, and this Wuu workflow. Apply these mappings:

- “Active/injected DESIGN.md” means the design document you explicitly selected
  and read. If none exists, choose a bundled system or establish one from the
  brief; do not stop merely because OpenDesign's daemon did not inject it.
- `<artifact>` output contracts mean write a real file and present it using
  Wuu's available delivery tools. Do not return a large source block solely
  because an upstream template requests it.
- `od`, `/api/*`, `od://`, OpenDesign MCP tools, canvas bridges, project IDs,
  plugin installation, and provider configuration refer to the upstream app.
  They are not installed by this plugin. Do not execute those steps, overwrite
  agent configuration, launch upstream services, or pretend their capabilities
  exist. Translate ordinary reading, writing, preview, and export steps to
  available Wuu tools; explain a missing capability when it blocks the task.
- `data-od-*` attributes may remain as harmless HTML metadata; they do not
  enable canvas comments or direct editing in Wuu. Paths such as `/api/skills/`
  must become valid local resources or be replaced before delivering a file.
- Image/video/audio catalog entries are prompt recipes, not model integrations.
  Use a compatible tool only when it is available and the user's task calls for
  generation. Otherwise deliver the adapted prompt and state that media was
  not generated. Preserve source credit and distinguish sample from live data.
- Templates may reference optional libraries, remote assets, services, or
  scripts. Inspect those requirements before use; bundling their source does
  not install dependencies or authorize running scripts or publishing work.

The package's `UPSTREAM.json` records the pinned source revision and file
checksums. `THIRD_PARTY_NOTICES.md` and upstream license files retain attribution.
