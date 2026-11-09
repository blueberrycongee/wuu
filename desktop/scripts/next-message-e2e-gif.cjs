// Convert only the real Chromium captures produced by next-message-e2e.cjs.
// Usage: node scripts/next-message-e2e-gif.cjs /absolute/evidence/directory
const fs = require("node:fs");
const path = require("node:path");
const crypto = require("node:crypto");
const { spawnSync } = require("node:child_process");
const output = path.resolve(process.argv[2] || path.join(__dirname, "../out/e2e/next-message"));
const metadataPath = path.join(output, "recording.json");
const recording = JSON.parse(fs.readFileSync(metadataPath, "utf8"));
if (!recording.frameCount || recording.frames.length !== recording.frameCount) throw new Error("Complete source recording required");
const filename = path.join(output, "next-message-demo.gif");
const args = ["-y", "-framerate", "10", "-i", path.join(output, "recording-frames/%05d.png"),
  "-frames:v", String(recording.frameCount), "-vf", "split[s0][s1];[s0]palettegen=stats_mode=diff[p];[s1][p]paletteuse=dither=sierra2_4a", "-loop", "0", filename];
const result = spawnSync("ffmpeg", args, { encoding: "utf8" });
if (result.status !== 0) throw new Error(result.stderr || "ffmpeg failed");
recording.gif = { filename: path.basename(filename), sha256: crypto.createHash("sha256").update(fs.readFileSync(filename)).digest("hex"),
  frameRate: 10, durationSeconds: recording.frameCount / 10, command: ["ffmpeg", ...args],
  transformation: "Lossless source screenshots converted to an indexed-color looping GIF at a fixed 10 fps. No UI overlays, generated pixels, or text were added." };
fs.writeFileSync(metadataPath, JSON.stringify(recording, null, 2));
console.log(JSON.stringify(recording.gif, null, 2));
