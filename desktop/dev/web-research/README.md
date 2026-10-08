# Compact web sources preview

This fixture renders the production `AssistantTurnShell`, `ProcessSurface` and
`TurnSourcesRow` with synthetic search results in Electron. Only the surrounding
window chrome, user bubble and composer are scenery. It does not call an LLM,
start the app-server, or load user data.

## Interaction

Search activity and source controls share one header. The tool summary and the
source-count control open the same process disclosure beneath the row, aligned
to its left edge. Direct favicon links remain separate hit targets.

The disclosure includes ALL sources, including the icons shown in the header,
and the existing tool timeline with the original query, status and call details.
Sources appear as tightly wrapping favicon/domain chips. Initially eight are
shown; the remaining-count control makes every additional link reachable. The
whole inspection area uses the existing bounded process scroll region.

Distinct URLs on the same site remain separate links; exact duplicate URLs are
collapsed within the group. The source count counts links, while the tool summary
counts calls. Titles and full URLs are available on hover/focus. Source clicks
preserve original URLs, modifiers and workspace-browser routing.

Narrow headers show fewer icons without adding another collapsed row. The first
live visible sources use the existing avatar entrance; revisiting a disclosure
or history does not replay receipts. Reduced motion is respected. Escape closes
the whole group and returns focus to the control that opened it. Failed or
interrupted calls do not contribute sources.

## Reference

The initial study borrowed staged disclosure from Motionbook's
[Flight pill](https://github.com/blueberrycongee/motionbook/tree/main/examples/flight-pill),
a study of R / [@wheresryan22's pill buttons](https://www.inspora.design/posts/pill-buttons).
Wuu keeps staged source arrivals and the compact source header as its visual
anchor. The complete source set now uses compact chips within the owning process disclosure.
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
hover tooltips, same-line header geometry and non-overlapping targets, left-aligned complete
source disclosure with bounded height, unified tool/source controls,
group reopening and rapid reversal, keyboard activation, Escape/focus return, answer handoff,
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
