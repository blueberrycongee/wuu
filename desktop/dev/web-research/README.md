# Compact web sources preview

This fixture renders the production `AssistantTurnShell`, `ProcessSurface` and
`TurnSourcesRow` with synthetic search results in Electron. Only the surrounding
window chrome, user bubble and composer are scenery. It does not call an LLM,
start the app-server, or load user data.

## Interaction

Sources belong to their tool-call group in the message flow. The existing tool
summary owns the query and progress; sources add one compact row underneath.
Up to six 24 px round website avatars have separate 32 px hit targets, followed
by a source count. Titles and full URLs appear on hover or keyboard focus.
Only successful calls contribute sources, deduplicated by host within the group.

New circles reveal over `--motion-slow` (280 ms by default), staggered by 45 ms
up to 225 ms. A hover lifts the avatar by 2 px while its hit target stays fixed.
More sources expand in place with Wuu's shared disclosure; Escape collapses the
extra icons and returns focus to the count button. Closing makes them inert
immediately. Source buttons preserve modifier clicks and workspace-browser routing.

The whole process folds on answer handoff according to the existing turn policy.
Reopening it restores source links without replaying arrival motion. An inactive
conversation does not animate newly received sources. Both application and OS
reduced-motion preferences suppress arrivals and hover movement. Favicons use
letter fallbacks if their requests fail.

## Reference

The initial study borrowed staged disclosure from Motionbook's
[Flight pill](https://github.com/blueberrycongee/motionbook/tree/main/examples/flight-pill),
a study of R / [@wheresryan22's pill buttons](https://www.inspora.design/posts/pill-buttons).
This revision applies that sequence to small source circles inside existing tool
activity, with no standalone card surface. Timings above are authored for Wuu,
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
hover tooltips, overflow, keyboard activation, Escape/focus return, answer handoff,
reopening history, long source lists, no sources after failure/interruption/empty
results, inactive panes and both reduced-motion sources. Screenshots, measured
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
