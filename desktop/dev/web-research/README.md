# Compact web sources preview

This fixture renders the production `AssistantTurnShell`, `ProcessSurface` and
`TurnSourcesRow` with synthetic search results in Electron. Only the surrounding
window chrome, user bubble and composer are scenery. It does not call an LLM,
start the app-server, or load user data.

## Interaction

Sources share the aggregated tool-call header in one line. The tool summary
opens call details; separate source buttons open links and compact domain chips.
Up to six 24 px avatars have independent 32 px hit targets. Narrow headers
show fewer icons and move the remainder into +N without adding a second row. Titles and full URLs appear on hover or keyboard focus.
Only successful calls contribute sources. Exact duplicate URLs collapse within
the group; different pages on the same host remain individually reachable.
The tool summary counts calls, while +N counts remaining source links.

The first live results appear beside their owning search summary. Avatars
settle into that space over `--motion-slow` (280 ms by default), with a
50 ms lead-in and 35 ms spacing within the new batch, capped at three intervals.
The avatar moves; its hit target and focus outline stay still. A later batch
does not replay earlier sources or inherit a delay from its absolute position.

More sources open tightly wrapping favicon-and-domain chips near +N. The
initial panel shows up to eight additional links and an exact remaining-count
control reveals the rest. Full titles and URLs stay available on hover/focus.
Long collections scroll within the bounded panel. Opening it
does not add height to the message flow, and the first six targets stay still.
The panel follows the existing floating-menu positioning and keyboard behavior:
arrows navigate the chips, Escape returns focus to +N, Tab continues from the trigger, and
an outside press closes it. Source buttons preserve modifier clicks and
workspace-browser routing.

The whole process folds on answer handoff according to the existing turn policy.
Reopening it restores source links without replaying arrival motion. An inactive
conversation does not animate newly received sources, including sources not yet
shown in overflow. The first reveal of a cached conversation stays still. Both
application and OS reduced-motion preferences suppress arrivals and hover
movement; restoring motion does not replay old arrivals. Favicons crossfade from
letter fallbacks, which remain available if requests fail.

## Reference

The initial study borrowed staged disclosure from Motionbook's
[Flight pill](https://github.com/blueberrycongee/motionbook/tree/main/examples/flight-pill),
a study of R / [@wheresryan22's pill buttons](https://www.inspora.design/posts/pill-buttons).
Wuu keeps staged source arrivals and the compact source header as its visual
anchor. Additional destinations now use compact chips near the trigger.
It omits the reference's large card, rolling counters, blur and demonstration
timers: source receipts and user disclosure actions drive the actual state.
Timings above are authored for Wuu,
not measured from the reference. No reference code, assets, fonts or traces are
copied. The reference's previews are offline renders; see its `PROVENANCE.md`
and `VALIDATION.md` for separate evidence limits.

## Reproduce

From `desktop/`, in separate terminals:

```sh
npx vite --config dev/web-research/vite.config.ts
./node_modules/.bin/electron dev/web-research/capture.cjs
```

Open `http://127.0.0.1:5218/dev/web-research/` for manual review. Query parameters:
`theme=dark`, `size=20`, `motion=reduce`, and `long`.

The capture script covers light/dark, 14/20 px, 390/900 px windows, source arrival,
hover tooltips, same-line header geometry and non-overlapping targets, bounded chip geometry
without flow expansion, outside presses,
overflow reopening and rapid reversal, keyboard activation, Escape/focus return, answer handoff,
reopening history, long source lists, no sources after failure/interruption/empty
results, inactive panes and both reduced-motion sources. Arrival-start events
remain recorded across no-replay checks so a completed transient animation cannot
hide behind an endpoint assertion. Screenshots, measured
geometry, runtime versions and frame timestamps go to
`artifacts/web-research-compact/`. Real favicon requests are allowed; synthetic
`example.com` domains deliberately exercise the letter fallback.

From the repository root, encode the timestamped renderer capture:

```sh
ffmpeg -y -f concat -safe 0 -i artifacts/web-research-compact/frames.txt \
  -filter_complex '[0:v]fps=20,crop=900:590:0:0,split[a][b];[a]palettegen=stats_mode=diff[p];[b][p]paletteuse=dither=sierra2_4a' \
  -loop 0 artifacts/web-research-compact/preview.gif
```

The GIF preserves measured capture intervals, including capture overhead. It is
an actual React/CSS conversation rendering with fixture data, not a recording of
provider-backed search or the installed desktop app.
