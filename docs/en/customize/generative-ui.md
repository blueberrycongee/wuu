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
- Copy or reset values locally. A version 2 button or form can explicitly send a follow-up
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

## Version 2 protocol

`render_ui` accepts `{ "spec": { "version": 2, "title": "…", "fallback": "…",
"state": [], "body": [] } }`. State entries contain an `id` and JSON `value`;
body nodes have unique IDs and explicit, type-specific properties. The canonical
schema is [`plugins/generative-ui/schema.json`](../../../plugins/generative-ui/schema.json).

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

The native tool and desktop both validate programs. Limits include 128 KiB per
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

## Version 1 protocol

`render_ui` accepts an object containing `spec`. A spec has `version: 1`, a
nonblank `title`, a useful nonblank `fallback`, and 1–16 `blocks` with unique IDs.
The canonical tool schema is
[`plugins/generative-ui/schema.json`](../../../plugins/generative-ui/schema.json).
The native tool validates it before publishing a resource with MIME type
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

## Developer verification

Build the core/helpers with `npm --prefix desktop run build:core`, then run
`npm --prefix desktop run test:e2e:generative-ui` in a graphical session. On
headless Linux, invoke the same Electron script with `--no-sandbox
--ozone-platform=headless --ozone-override-screen-size=1440,1400`.
`WUU_GENERATIVE_UI_PLUGIN_HELPER` selects an already-built helper and
`WUU_GENUI_OUTPUT` selects an output directory.

The E2E uses a real native tool process and production artifact/plugin renderer
components with synthetic data. It checks native/desktop validation agreement,
interactive controls, state isolation and restoration, disabled/error fallback,
and storage failure; it records tool output, JSON results, and light/dark,
wide/narrow, default/large-font screenshots. Its storage adapter is synthetic;
it does not claim a live provider call or packaged-app verification.

For the complete application path, run
`npm --prefix desktop run test:e2e:generative-ui-app`. This builds and opens the
real App/main/preload/core with a loopback synthetic HTTP provider in a temporary
home. It checks default-off tool discovery, catalog enablement, a real native
tool result in the conversation, production plugin storage writes and reload,
disable/re-enable fallback, interleaved tool-argument previews, cancellation, and
explicit follow-up submission while preserving composer drafts. It records actual IPC, provider requests,
native storage, screenshots and build hashes under
`desktop/out/e2e/generative-ui-app` (`WUU_GENUI_APP_OUTPUT` overrides this).
No external model or user credential is used. This is source-build application
acceptance, not a packaged-distribution check.

Run `go run scripts/generative-ui-lifecycle-e2e.go` from the repository root to
check native tool discovery, execution, disable/re-enable and safe mode with an
isolated temporary home; `WUU_GENERATIVE_UI_PLUGIN_HELPER` selects the helper and
`WUU_GENUI_LIFECYCLE_OUTPUT` selects the JSON evidence file. It also compiles the
advertised schema after Wuu's provider normalization, without calling a provider;
this does not establish schema support for every provider or model.
