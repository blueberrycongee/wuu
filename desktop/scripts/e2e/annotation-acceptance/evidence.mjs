import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';

const root = path.resolve(process.argv[2] || 'desktop/out/e2e/annotation-acceptance');
const production = path.join(root, 'production');
const captures = path.join(production, 'annotation-acceptance');
const output = path.join(root, 'evidence');
fs.mkdirSync(output, { recursive: true });
const read = file => JSON.parse(fs.readFileSync(file, 'utf8'));
const command = (name, args, encoding) => {
  const result = spawnSync(name, args, { encoding, maxBuffer: 64 * 1024 * 1024 });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(String(result.stderr || `${name} exited ${result.status}`));
  return result.stdout;
};
const ffmpeg = (args, encoding) => command('ffmpeg', ['-hide_banner', '-loglevel', 'error', '-y', ...args], encoding);
const manifest = read(path.join(captures, 'manifest.json'));
const results = read(path.join(production, 'results.json'));
assert.equal(manifest.presentation, 'production');
assert.equal(results.passed, true, 'The full production document-context journey must pass.');
assert.equal(results.annotationAcceptance, true);
assert.equal(manifest.matrix.length, 8);
assert.ok(manifest.matrix.every(item => item.passed && item.commentEdit?.retained));
assert.equal(manifest.recordings.length, 2);

// Registry membership cannot prove that a shadow-root highlight was painted.
// Compare original paper pixels with the focused-comment capture in the live
// range rectangles; exclude glyph pixels and rectangle edges from the sample.
const highlightPixels = [];
for (const scenario of manifest.matrix) {
  const dimensions = scenario.states.commentFocus.viewport;
  const pixels = state => ffmpeg(['-i', path.join(captures, `${scenario.name}-${state}.png`),
    '-frames:v', '1', '-f', 'rawvideo', '-pix_fmt', 'rgb24', 'pipe:1']);
  const source = pixels('01-source');
  const focused = pixels('03-comment-focus');
  assert.equal(source.length, dimensions.width * dimensions.height * 3);
  assert.equal(focused.length, source.length);
  let sampled = 0, changed = 0;
  const visited = new Set();
  for (const rect of scenario.focusedSourceHighlight.flatMap(item => item.rects)) {
    for (let y = Math.max(0, Math.ceil(rect.top) + 1); y < Math.min(dimensions.height, Math.floor(rect.bottom) - 1); y++) {
      for (let x = Math.max(0, Math.ceil(rect.left) + 1); x < Math.min(dimensions.width, Math.floor(rect.right) - 1); x++) {
        const offset = (y * dimensions.width + x) * 3;
        if (visited.has(offset)) continue;
        visited.add(offset);
        const before = source.subarray(offset, offset + 3);
        if (Math.min(...before) < 245 || Math.max(...before) - Math.min(...before) > 4) continue;
        sampled++;
        const after = focused.subarray(offset, offset + 3);
        if (before.some((value, channel) => Math.abs(value - after[channel]) > 8)) changed++;
      }
    }
  }
  assert.ok(sampled > 100, `${scenario.name}: source paper provides a meaningful highlight sample`);
  assert.ok(changed / sampled > 0.8, `${scenario.name}: focused source range is visibly painted`);
  highlightPixels.push({ scenario: scenario.name, sampledPaperPixels: sampled, changedPaperPixels: changed,
    fractionChanged: changed / sampled, source: `${scenario.name}-01-source.png`, focused: `${scenario.name}-03-comment-focus.png` });
}

const artifacts = [];
for (const recording of manifest.recordings) {
  const directory = path.join(captures, recording.directory);
  const frames = read(path.join(directory, 'frames.json')).frames;
  assert.ok(frames.length > 1, `${recording.name} has actual captured frames.`);
  const lines = ['ffconcat version 1.0'];
  for (let index = 0; index < frames.length; index++) {
    assert.match(frames[index].file, /^\d{5}\.png$/);
    const bytes = fs.readFileSync(path.join(directory, frames[index].file));
    assert.equal(createHash('sha256').update(bytes).digest('hex'), frames[index].sha256);
    lines.push(`file '${frames[index].file}'`);
    const duration = index + 1 < frames.length ? (frames[index + 1].timestamp - frames[index].timestamp) / 1000 : 0.5;
    assert.ok(duration > 0, 'Captured frame timestamps must increase.');
    lines.push(`duration ${duration.toFixed(4)}`);
  }
  lines.push(`file '${frames.at(-1).file}'`);
  const concat = path.join(directory, 'frames.ffconcat');
  fs.writeFileSync(concat, `${lines.join('\n')}\n`);
  const gif = `production-${recording.name}.gif`;
  const target = path.join(output, gif);
  ffmpeg(['-f', 'concat', '-safe', '0', '-i', concat, '-filter_complex',
    '[0:v]split[a][b];[a]palettegen=stats_mode=diff[p];[b][p]paletteuse=dither=sierra2_4a',
    '-fps_mode', 'vfr', '-loop', '0', target]);
  const probe = JSON.parse(command('ffprobe', ['-v', 'error', '-count_frames', '-show_streams', '-show_format', '-of', 'json', target], 'utf8'));
  const decoded = ffmpeg(['-i', target, '-f', 'framemd5', 'pipe:1'], 'utf8').split('\n')
    .filter(line => line && !line.startsWith('#')).map(line => line.split(',').at(-1).trim());
  const frameCount = Number(probe.streams[0].nb_read_frames);
  const duration = Number(probe.format.duration);
  const distinctFrames = new Set(decoded).size;
  assert.ok(frameCount > 1 && distinctFrames > 1 && duration > 0, 'The delivered GIF must contain real changing frames.');
  artifacts.push({ file: gif, scenario: recording.name, kind: 'actual-captured-frames',
    capturedFrames: frames.length, decodedFrames: frameCount, distinctFrames, durationSeconds: duration });
}
for (const scenario of manifest.matrix) {
  for (const state of ['02-selection', '04-comment', '05-draft', '06-expanded', '07-source-return']) {
    const file = `${scenario.name}-${state}.png`;
    fs.copyFileSync(path.join(captures, file), path.join(output, file));
    artifacts.push({ file, scenario: scenario.name, state, kind: 'unaltered-real-screenshot' });
  }
}
const nonPdfPath = path.join(root, 'non-pdf', 'report.json');
const nonPdf = fs.existsSync(nonPdfPath) ? read(nonPdfPath) : null;
const summary = { passed: Boolean(nonPdf?.completed && nonPdf.errors.length === 0), pdfPassed: true,
  boundary: 'Production Electron main/preload/renderer and Go core, local synthetic provider, disposable profile. Screenshots and GIF frames are actual captures without generated UI or interpolated motion. Non-PDF regression uses the same production renderer with its existing in-memory preload fixture. This is not native macOS or operating-system IME verification.',
  sourceCommit: manifest.sourceCommit, sourceHeadCommit: manifest.sourceHeadCommit, buildDir: results.buildDir,
  buildConfig: 'desktop/electron.vite.config.ts', checks: results.checks,
  matrix: manifest.matrix.map(item => item.name), highlightPixels,
  nonPdf: nonPdf ? { completed: nonPdf.completed === true, boundary: nonPdf.boundary, cases: nonPdf.cases, errors: nonPdf.errors } : { completed: false, error: "Non-PDF report is missing." },
  artifacts };
fs.writeFileSync(path.join(output, 'acceptance.json'), JSON.stringify(summary, null, 2));
const media = artifacts.map(artifact => `<figure><figcaption>${artifact.file}</figcaption><img loading="lazy" src="${artifact.file}" alt="${artifact.file}"></figure>`).join('\n');
fs.writeFileSync(path.join(output, 'index.html'), `<!doctype html><meta charset="utf-8"><title>Wuu production annotation acceptance</title>
<style>body{font:15px system-ui;margin:24px;background:#f7f7f7;color:#222}img{max-width:100%;height:auto}figure{margin:28px 0}figcaption{margin:8px 0;font-weight:600}</style>
<h1>Wuu production annotation acceptance</h1><p>${summary.boundary}</p><p>Checkout: ${summary.sourceCommit}. PR head: ${summary.sourceHeadCommit || 'not supplied'}. Full PDF journey passed.</p>${media}`);
console.log(JSON.stringify({ passed: summary.passed, directory: output, artifacts: artifacts.length, highlightPixels }, null, 2));

if (!summary.passed) process.exitCode = 1;
