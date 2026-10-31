# Generative UI (experimental)

Generative UI lets a model return interactive layouts, charts, calculators, and forms inside a conversation. The bundled plugin is **disabled by default**.
Open **Skills & Plugins**, select **Generative UI (Experimental)**, and enable
it to make its `render_ui` tool available. Disabling the plugin removes the tool
and renderer; existing results retain their text summary.

The plugin uses Wuu's existing tool-calling path. It does not require another
API key or a separate UI-generation service. Your selected provider and model
must support the tool schema. External engines must expose Wuu plugin tools to
use it; merely enabling the plugin does not add tools to an unrelated engine.

## Interactions

- Compose rows, grids, cards, disclosures, and conditional or repeated content.
- Edit text, numbers, dates, selections, checkboxes, and sliders. Expressions can
  recalculate displayed values locally without making another model request.
- Filter and sort tables; inspect bar, line, area, scatter, pie, and donut charts.
  Series legends and accessible data tables expose the underlying values.
- Forms use native submission and field validation, including required and invalid
  inputs. Submission requires explicit interaction.
- Copy or reset values locally. A source or version 2 button or form can explicitly send a follow-up
  to the result's conversation, with its source and current values as context.
  Sending requires a real user interaction and uses the normal conversation path.
  It does not clear an existing composer draft or its attachments. Previews and
  unavailable or running conversations cannot submit a follow-up.

While tool arguments stream, the renderer previews complete, validated nodes.
Incomplete or invalid updates keep the last usable preview. Preview state is
transient; final results use namespaced plugin storage on this device, scoped to
conversation, tool-result part, and exact spec. Reopening the same result restores
its saved state. Version 2 separates declared input values from table filters,
sorting, chart legend visibility, and disclosure expansion. Their combined saved
snapshot must fit in 16 KiB. Component state stays out of follow-up message context.
A storage failure or oversized draft leaves the current view
usable and reports the persistence failure. State does not sync between devices.
Treat saved input as local conversation data; do not enter passwords or secrets.

## Source format (version 3)

New calls use `{ "spec": { "version": 3, "title": "…", "fallback": "…", "source": "…" } }`.
The model-facing [schema](../../../plugins/generative-ui/schema.json) describes
the supported language and components. Put `source` last so the title and text
fallback arrive before the interface.

Source combines an optional leading `{@body ...}` declaration block with JSX.
For example, the following is the value of `source`:

```jsx
{@body
  const [quantity, setQuantity] = DIL.useState(2);
  const total = quantity * 12;
}
<Stack>
  <Input label="Quantity" inputType="number"
    value={quantity} onChange={setQuantity} />
  <Metric label="Total" value={total} />
  <Button onClick={() => DIL.sendMessage(`Explain the total of ${total}`)}>
    Explain this result
  </Button>
</Stack>
```

State setters accept a value or updater function. Ordinary expressions, local
functions, array mapping/filtering/reduction, and conditional JSX calculate the
view. Controls receive the new value in `onChange`, rather than a DOM event.
Mapped components need unique `key` values to preserve their state when reordered.
Function components can return JSX. Prose belongs in `Text` or `Caption`;
`strong`, `em`, `code`, and `br` support inline formatting.

Layouts, charts, tables and controls use shared theme-aware components.
Custom diagrams use an explicit SVG vocabulary, including shapes, paths, text,
gradients and markers. SVG paint accepts semantic colors such as `accent`,
`muted`, `surface`, `success`, and `danger`. References stay local to the
view; external images, scripts, arbitrary styles, and foreign HTML are unavailable.

The plugin parses and interprets a bounded language; it does not run generated
JavaScript in the desktop realm. There are no imports, DOM APIs, network requests,
async functions, or unrestricted loops. Limits are 120 KiB of source within
128 KiB of arguments, 4,096 syntax nodes, 512 rendered components, 200 items per
collection, 30,000 calculation steps per render or interaction, and 16 KiB of state.
Expanded tables, charts, and controls share a 10,000-unit rendering budget;
rendered text and attribute values together are limited to 1 MiB. Reusing the
same data in multiple components counts each rendered occurrence.
The runtime and desktop validate the same source and initial view.

Text and complete children appear while their enclosing JSX is still streaming.
Incomplete expressions retain the previous usable preview. Final source must parse
completely. Preview and final rendering keep component identity, and newly added
content fades in briefly unless reduced motion is enabled. Previews remain read-only.
Failed interactions preserve the last valid values. Values and component preferences
restore on reopening the same result; only declared values accompany a follow-up.

## Historical version 2 protocol

`render_ui` accepts `{ "spec": { "version": 2, "title": "…", "fallback": "…",
"state": [], "body": [] } }`. State entries contain an `id` and JSON `value`;
body nodes have unique IDs and explicit, type-specific properties. Version 2 is retained for historical artifacts and direct runtime compatibility;
new model calls use version 3.

Version 2 is a bounded data program. Expressions read declared state and support
arithmetic, comparisons, formatting, conditions, array construction, mapping,
filtering, and aggregation. Repetition exposes the current item within its scope. An optional `each.key`
expression supplies a stable identity across reordering: a nonblank string of
at most 80 characters or a finite number within the normal bounds. Duplicate
sibling keys fail validation at runtime. Without an explicit key, full item
values determine identity, so editing an item may reset its local view state.
Actions can set declared state, reset, copy text, or explicitly send a message.
Unknown nodes, properties, state references, and out-of-scope expressions fail
validation. It does not execute generated JavaScript or accept arbitrary HTML,
CSS, URLs, network access, or React props.

The TypeScript tool runtime and desktop share the same program validator. Limits include 128 KiB per
spec, 32 state slots with a 16 KiB initial state budget, 256 declared nodes with
12 nesting levels, and 2,048 expression nodes with 16 nesting levels. Runtime
execution is also bounded; oversized computed views fail instead of continuing
unbounded work. Version 1 historical results remain supported.

A small calculator uses the same controls and expression format as a larger form:

```json
{
  "spec": {
    "version": 2,
    "title": "Estimate",
    "fallback": "Choose a quantity to calculate the total at 12 per item.",
    "state": [{ "id": "quantity", "value": 2 }],
    "body": [
      { "id": "quantityInput", "type": "input", "inputType": "number", "state": "quantity", "label": "Quantity" },
      { "id": "total", "type": "metric", "label": "Total", "value": { "op": "multiply", "args": [{ "op": "state", "key": "quantity" }, 12] } }
    ]
  }
}
```

## Historical version 1 protocol

`render_ui` accepts an object containing `spec`. A spec has `version: 1`, a
nonblank `title`, a useful nonblank `fallback`, and 1–16 `blocks` with unique IDs.
This format is retained for historical artifacts and direct runtime compatibility.
The tool runtime validates it before publishing a resource with MIME type
`application/vnd.wuu.ui+json` and `placement: inline`; the desktop validates
stored content again before rendering it.

Supported blocks:

| Type | Required content | Limits |
| --- | --- | --- |
| `text` | `text` | 4,000 characters |
| `table` | `columns` (`key`, `label`), `rows` (arrays of cells) | 1–8 columns, 0–200 rows; every row matches the column count |
| `chart` | `chartType` (`bar` or `line`), `points` (`label`, `value`) | 1–100 points; optional title and axis labels |
| `form` | `fields` (`id`, `label`, `kind`) | 1–12 fields; kinds `text`, `number`, `select`, `checkbox`; optional initial values |

IDs start with an ASCII letter and contain at most 48 letters, digits, hyphens,
or underscores. Titles are limited to 160 characters, labels to 80, table text
cells and text inputs to 500, and fallback text to 8,000. Numbers must be finite
and between −10¹² and 10¹². Select fields require 1–20 distinct options; other
field kinds reject options. Initial values must match the field kind. Unknown
properties and component types are rejected. The total input and rendered spec
must each fit in 128 KiB.

For example:

```json
{
  "spec": {
    "version": 1,
    "title": "Tasks by team",
    "fallback": "Design: 12 tasks. Engineering: 24 tasks.",
    "blocks": [{
      "id": "tasks",
      "type": "chart",
      "chartType": "bar",
      "xLabel": "Team",
      "yLabel": "Tasks",
      "points": [
        { "label": "Design", "value": 12 },
        { "label": "Engineering", "value": 24 }
      ]
    }]
  }
}
```

`render_ui` is a direct presentation tool, including when programmatic tool
calling is enabled. Call it at the top level; it cannot run inside `run_code`.

## Boundaries and recovery

Final specs are published as tool resources. Streaming previews are opt-in for
the active MIME renderer and remain read-only until the final result is accepted.
Wuu-owned React components render the validated vocabulary using the active theme.
The plugin itself is trusted extension code, like other Wuu plugins; data-program
restrictions do not sandbox unrelated installed plugins.

Invalid tool arguments return a readable error so the model can retry or answer
in text. Unsupported historical specs, disabled/missing plugins, and renderer
errors fall back to the ordinary text accompanying the resource. Other clients
can use that same text without implementing the interactive renderer.

## Live plugin updates

The tool runtime and desktop renderer use the ordinary public extension lifecycle.
After building once, `wuu plugin dev plugins/generative-ui` watches the development
package, including its schema, shared validator, desktop entry and SDK inputs.
It rebuilds and publishes changed generations without rebuilding Go or restarting
Wuu. Generated output does not trigger another build.

Renderer updates replace mounted views immediately. Tool definitions and schemas
are adopted at the next idle turn boundary; an in-progress turn keeps its previous
runtime snapshot. A failed replacement preserves the last usable generation.
Disabling the plugin removes interactive rendering and revokes its tools, including
retained runtime generations. Existing results keep their versioned data and text
fallback; re-enabling restores compatible saved input state. Updates must preserve
v1/v2/v3 result and storage compatibility or provide an explicit migration.

## Developer verification

Build the plugin runtime with `npm --prefix plugins run build -- --plugin=generative-ui`, then run
`npm --prefix desktop run test:e2e:generative-ui` in a graphical session. On
headless Linux, invoke the same Electron script with `--no-sandbox
--ozone-platform=headless --ozone-override-screen-size=1440,1400`.
`WUU_GENUI_OUTPUT` selects an output directory. The script runs the bundled
JavaScript tool with the desktop Node runtime; no separate helper is required.

The E2E uses a real JavaScript tool process and production artifact/plugin renderer
components with synthetic data. It checks runtime/desktop validation agreement,
interactive controls, state isolation and restoration, disabled/error fallback,
and storage failure; it records tool output, JSON results, and light/dark,
wide/narrow, default/large-font screenshots. Its storage adapter is synthetic;
it does not claim a live provider call or packaged-app verification.

For the complete application path, run
`npm --prefix desktop run test:e2e:generative-ui-app`. This builds and opens the
real App/main/preload/core with a loopback synthetic HTTP provider in a temporary
home. It checks default-off tool discovery, catalog enablement, a real plugin
tool result in the conversation, production plugin storage writes and reload,
disable/re-enable fallback, interleaved tool-argument previews, cancellation, and
explicit follow-up submission while preserving composer drafts. It records actual IPC, provider requests,
native storage, screenshots and build hashes under
`desktop/out/e2e/generative-ui-app` (`WUU_GENUI_APP_OUTPUT` overrides this).
No external model or user credential is used. This is source-build application
acceptance, not a packaged-distribution check.

Run `go run scripts/generative-ui-lifecycle-e2e.go` from the repository root to
check plugin tool discovery, execution, live replacement with a retained foreground
snapshot, failed replacement recovery, disable/re-enable and safe mode with an
isolated temporary home. Build the bundled runtime first and provide Node.js on
`PATH`; `WUU_GENUI_LIFECYCLE_OUTPUT` selects the JSON evidence file. It also compiles the
advertised schema after Wuu's provider normalization, without calling a provider;
this does not establish schema support for every provider or model.
