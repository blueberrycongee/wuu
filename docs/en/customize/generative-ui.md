# Generative UI (experimental)

Generative UI lets a model return interactive tables, charts, and small local
forms inside a conversation. The bundled plugin is **disabled by default**.
Open **Skills & Plugins**, select **Generative UI (Experimental)**, and enable
it to make its `render_ui` tool available. Disabling the plugin removes the tool
and renderer; existing results retain their text summary.

The plugin uses Wuu's existing tool-calling path. It does not require another
API key or a separate UI-generation service. Your selected provider and model
must support the tool schema. External engines must expose Wuu plugin tools to
use it; merely enabling the plugin does not add tools to an unrelated engine.

## Interactions

- Tables: filter locally and sort by a column, including numeric sorting.
- Bar and line charts: inspect values and select all, the last 10, or the last
  25 points. Expand **Chart data** for an accessible table of exact values.
- Forms: edit text, numbers, selections, and checkboxes; preview, copy, or reset
  the values. These are local drafts. No button sends a model message, submits
  a form, calls another tool, or opens a website.

Local view state is saved in namespaced plugin storage on this device, scoped
to the conversation, tool-result part, and exact spec. Reopening the same result
restores filters, sorting, chart range, and form drafts. The saved state budget
is 16 KiB per result. A storage failure or oversized draft shows a warning while
keeping the current view usable. State does not sync between devices. Treat
form drafts as locally stored conversation data; do not enter passwords or
other secrets into them.

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

Version 1 publishes complete specs after a tool call completes. It does not
stream partial specs or patches. It has no model-authored JavaScript, HTML,
CSS, URLs, network requests, or executable expressions. Wuu-owned React
components render a fixed vocabulary using the active theme. The plugin itself
is trusted extension code, like other Wuu plugins; these data restrictions do
not sandbox unrelated installed plugins.

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
and disable/re-enable fallback. It records actual IPC, provider requests,
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
