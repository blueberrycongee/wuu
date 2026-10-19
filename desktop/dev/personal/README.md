# twin desktop interaction fixture

Build with `node desktop/scripts/build-personal-fixture.cjs`. The generated
`desktop/out/personal-fixture/index.html` runs the actual Wuu App shell, twin
contribution, native message layout, file cards, history search and composer.
The visible banner identifies all fixture data as synthetic. No real account,
model service or external write is used.

`node desktop/scripts/smoke-personal-fixture.cjs` checks direct main entry,
navigation order, continuous right-panel sections, in-panel task/output details,
main identity and draft retention across routes, and digest-verified attachments.
It has no layout or pixel renderer and is not visual evidence.

Rendered acceptance additionally covers light/dark appearance, 15/19px text,
wide/narrow windows, real native input, history search, and scroll continuity.
A separate production Electron/main/preload/Go-host harness is required to prove
real runtime lifecycle, streaming, cancellation and persistent recovery. A fixture
pass alone does not prove those contracts or natural-language model quality.
