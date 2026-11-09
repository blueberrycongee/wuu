/**
 * Draw the DMG window background around the icon positions in `build.dmg`
 * and render it into build/dmg-background.png and @2x. The picture is the
 * drag instruction: a slingshot beside the app has fired Wuu on an arc that
 * splits into a squad of its colourful agents, all diving into a toy-block
 * fort built around Applications. It is wordless and on white, so it needs no
 * translation and a resized Finder window has no visible picture edge. Run
 * `npm run dmg-background:generate` from desktop, then commit both PNGs;
 * electron-builder combines the pair into one HiDPI TIFF while packaging.
 */
const { app, BrowserWindow } = require("electron");
const { buildSync } = require("esbuild");
const { writeFileSync } = require("node:fs");
const path = require("node:path");
const { build } = require("../package.json");
const icon = require("../../assets/app-icon-source.json");

const DESKTOP_DIR = path.join(__dirname, "..");
const BUILD_DIR = path.join(DESKTOP_DIR, "build");
const WIDTH = 720;
const HEIGHT = 420;
// Selecting a 128 pt icon draws a box this far from its centre, and the label
// follows below it. Artwork stays clear of the box.
const ZONE = { side: 72, top: 73, gap: 6 };
// The icons' artwork ends about this far below their centre; props stand on
// the same line so the scene shares one floor with the icons.
const FLOOR = 48;

const PAPER = "#ffffff";
const INK = "#111315";
const SLING = { offset: 44, height: 92, spread: 24, trunk: 14, arm: 12 };
// How high the shot climbs above the straight line into Applications, and
// where along that arc Wuu splits into its squad.
const LIFT = 122;
const SPLIT = 0.4;
const TRAIL_SPACING = 12.5;
const WUU_RADIUS = 23;
// Wuu leads the squad; each agent is an in-app avatar identity. They fly in a
// V that points down into Applications. `dx` places a member over the folder,
// `rise` sets how far above the box it is caught, and `turn` fans its path
// away from the shot's heading at the split.
const SQUAD = [
  { hue: 96, shape: "triangle", size: 40, dx: -52, rise: 30, turn: 40 },
  { wuu: true, dx: 4, rise: 0, turn: 16 },
  { hue: 202, shape: "round", size: 36, dx: 58, rise: 36, turn: -14 },
];
// The lookout on the left tower of the fort.
const LOOKOUT = { hue: 14, shape: "round", size: 36 };
// Agents queued behind the app for the next shot, nearest first; `hop` lifts
// one off the floor with excitement.
const QUEUE = [
  { hue: 288, shape: "capsule", size: 36, hop: 0 },
  { hue: 150, shape: "rounded-square", size: 32, hop: 16 },
];

const n = (value) => Number(value.toFixed(2));

// Agents are drawn by the product's own Blobatar renderer and mascot traits,
// so the squad is the same cast users meet inside Wuu.
function loadAvatarKit() {
  const source = buildSync({
    stdin: {
      contents: `export { blobatar, palette, _layout } from "blobatar";
        export { SHAPES } from "blobatar/blob";
        export { WUU_MASCOT_TRAITS } from "./src/renderer/wuu-mascot-spec";`,
      resolveDir: DESKTOP_DIR,
      loader: "ts",
    },
    bundle: true, write: false, format: "iife", globalName: "kit", platform: "neutral",
  }).outputFiles[0].text;
  return new Function(`${source};return kit;`)();
}

function pill(x, y, width, height, degrees, fill) {
  const radius = Math.min(width, height) / 2;
  return `<rect x="${n(-width / 2)}" y="${n(-height / 2)}" width="${n(width)}" height="${n(height)}" rx="${n(radius)}" fill="${fill}" transform="translate(${n(x)} ${n(y)}) rotate(${n(degrees)})"/>`;
}

// Face geometry from the app icon: eye size and spacing relative to the body,
// and the face's offset toward the gaze. The icon's tilt at its own gaze sets
// how far the eye pair turns as the gaze moves around.
function wuu(radius, gazeDegrees) {
  const gaze = gazeDegrees * Math.PI / 180;
  const iconGaze = Math.atan2(icon.faceY, icon.faceX);
  const tilt = icon.tilt / Math.sin(2 * iconGaze) * Math.sin(2 * gaze);
  const axis = tilt * Math.PI / 180;
  const offset = Math.hypot(icon.faceX, icon.faceY) * radius;
  const spread = icon.eyeGap / 2 / icon.radius * radius;
  const eyes = [-1, 1].map((side) => pill(
    offset * Math.cos(gaze) + side * spread * Math.cos(axis),
    offset * Math.sin(gaze) + side * spread * Math.sin(axis),
    icon.eyeWidth / icon.radius * radius, icon.eyeHeight / icon.radius * radius, tilt, icon.eyeColor));
  return `<circle r="${n(radius)}" fill="url(#body)"/>${eyes.join("")}`;
}

// An in-app agent avatar centred on the origin, eyes turned toward `gaze`.
// `half` is the painted body's half height, for standing it on a surface.
function agent(kit, member, gaze) {
  const { trait } = kit.SHAPES.find((shape) => shape.id === member.shape);
  const name = `dmg-${member.shape}-${member.hue}`;
  const options = {
    palette: kit.palette(member.hue),
    traits: { ...kit.WUU_MASCOT_TRAITS, shape: trait },
    perspective: { ...gaze, strength: 1 },
  };
  const body = kit.blobatar(name, options).replace(/^<svg[^>]*>/, "").replace(/<\/svg>$/, "");
  const scale = member.size / 100;
  return {
    svg: `<g transform="scale(${n(scale)}) translate(-50 -50)">${body}</g>`,
    half: kit._layout(name, options).body.ry * scale,
  };
}

// The icon's three pop marks, fanned around a point as if around a body of
// `radius`, at `scale` times their size in the icon.
function popMarks(x, y, radius, towardDegrees, spreadDegrees, scale) {
  return icon.marks.map((mark, i) => {
    const degrees = towardDegrees + (i - 1) * spreadDegrees + mark.angle;
    const angle = degrees * Math.PI / 180;
    const distance = radius + (icon.fanGap + mark.gap + mark.length / 2) * scale;
    return pill(x + distance * Math.cos(angle), y + distance * Math.sin(angle),
      mark.length * scale, mark.width * scale, degrees, icon.markColor);
  }).join("");
}

// A soft contact shadow where a prop meets the floor.
function footing(x, floor, width) {
  return `<ellipse cx="${n(x)}" cy="${n(floor + 1)}" rx="${n(width / 2)}" ry="3.2" fill="${INK}" fill-opacity="0.07"/>`;
}

function slingshot(kit, x, floor) {
  const forkY = floor - SLING.height * 0.47;
  const arm = SLING.height * 0.5;
  const tips = [-1, 1].map((side) => {
    const angle = side * SLING.spread * Math.PI / 180;
    return { side, x: x + Math.sin(angle) * arm, y: forkY - Math.cos(angle) * arm };
  });
  const [left, right] = tips;
  // Just released: the band still bows toward the shot.
  const band = `<path d="M${n(left.x)} ${n(left.y)}Q${n(x)} ${n(left.y - 16)} ${n(right.x)} ${n(right.y)}" fill="none" stroke="${INK}" stroke-width="3.2" stroke-linecap="round"/>`;
  // The trunk continues below the floor, clipped there, so it reads as planted.
  const trunk = pill(x, (forkY + floor) / 2 + 8, SLING.trunk, floor - forkY + 24, 0, icon.bodyColor);
  const arms = tips.map((tip) => pill((x + tip.x) / 2, (forkY + tip.y) / 2, SLING.arm, arm + SLING.arm, tip.side * SLING.spread, icon.bodyColor));
  const wraps = tips.map((tip) => pill(tip.x, tip.y + 8, 16, 5, tip.side * SLING.spread, kit.palette(52).head));
  return {
    svg: footing(x, floor, 44) + band + `<g clip-path="url(#floor)">${trunk}</g>` + arms.join("") + wraps.join(""),
    pouch: { x, y: left.y - 8 },
  };
}

// Toy blocks in the avatar palette: peach wood, blue ice and a neutral stone.
// Each block carries a darker footing so stacks read as separate pieces.
function block(x, y, width, height, fill, glint = false) {
  const shade = `<rect x="${n(x)}" y="${n(y + height - 3)}" width="${n(width)}" height="3" rx="1.5" fill="${INK}" fill-opacity="0.08"/>`;
  const shine = glint
    ? `<path d="M${n(x + width * 0.28)} ${n(y + 5)}L${n(x + width * 0.28)} ${n(y + height - 8)}" stroke="${PAPER}" stroke-opacity="0.85" stroke-width="2.4" stroke-linecap="round"/>`
    : "";
  return `<rect x="${n(x)}" y="${n(y)}" width="${n(width)}" height="${n(height)}" rx="3" fill="${fill}"/>${shade}${shine}`;
}

// Two towers flank the Applications folder, leaving its top open for the squad.
function fort(kit, applications, floor) {
  const wood = kit.palette(52).head;
  const ice = kit.palette(250).head;
  const stone = "#dedfe0";
  const roof = kit.palette(14).head;

  // Left tower: stone footing, ice pillar, wooden deck for the lookout.
  const lx = applications.x - ZONE.side - 6;
  const left = footing(lx - 16, floor, 48)
    + block(lx - 32, floor - 20, 32, 20, stone)
    + block(lx - 24, floor - 64, 16, 44, ice, true)
    + block(lx - 38, floor - 73, 38, 9, wood);
  const deck = { x: lx - 19, y: floor - 73 };

  // Right tower: taller, capped with a roof and a pennant over Applications.
  const rx = applications.x + ZONE.side + 6;
  const roofBase = floor - 108;
  const right = footing(rx + 17, floor, 50)
    + block(rx, floor - 20, 34, 20, stone)
    + block(rx + 9, floor - 80, 16, 60, wood)
    + block(rx + 3, roofBase, 28, 28, ice, true)
    + `<path d="M${n(rx - 1)} ${n(roofBase)}L${n(rx + 17)} ${n(roofBase - 22)}L${n(rx + 35)} ${n(roofBase)}Z" fill="${roof}" stroke="${roof}" stroke-width="4" stroke-linejoin="round"/>`;
  const poleTop = roofBase - 50;
  const flag = `<path d="M${n(rx + 17)} ${n(roofBase - 22)}V${n(poleTop)}" stroke="${icon.bodyShadow}" stroke-width="2.4" stroke-linecap="round"/>`
    + `<path d="M${n(rx + 18)} ${n(poleTop + 1)}Q${n(rx + 30)} ${n(poleTop + 3)} ${n(rx + 42)} ${n(poleTop + 8)}Q${n(rx + 30)} ${n(poleTop + 13)} ${n(rx + 18)} ${n(poleTop + 16)}Z" fill="${kit.palette(96).head}"/>`;
  return { svg: left + right + flag, deck };
}

// A quadratic from the split point to where a squad member is caught.
function curve(from, control, to) {
  return (t) => ({
    x: (1 - t) ** 2 * from.x + 2 * (1 - t) * t * control.x + t ** 2 * to.x,
    y: (1 - t) ** 2 * from.y + 2 * (1 - t) * t * control.y + t ** 2 * to.y,
  });
}

// Alternating puffs along a path up to `to`, smaller the older they are when
// `scale` grows. `phase` carries the spacing and size rhythm across the split
// so the trail stays continuous, and puffs stop `clear` short of `target`.
function puffs(path, fill, { to, clear, target, phase, scale }) {
  const dots = [];
  let travelled = 0;
  let previous = path(0);
  for (let t = 0; t <= to; t += 0.0005) {
    const point = path(t);
    travelled += Math.hypot(point.x - previous.x, point.y - previous.y);
    previous = point;
    if (travelled < TRAIL_SPACING * (dots.length + phase.skip)) continue;
    if (Math.hypot(target.x - point.x, target.y - point.y) < clear) break;
    const index = phase.count + dots.length;
    const radius = (index % 2 ? 1.9 : 3.3) * scale(t);
    dots.push(`<circle cx="${n(point.x)}" cy="${n(point.y)}" r="${n(radius)}" fill="${fill}"/>`);
  }
  return dots;
}

function drawBackground(kit) {
  const [appIcon, applications] = build.dmg.contents;
  const floor = applications.y + FLOOR;
  const boxTop = applications.y - ZONE.top - ZONE.gap;
  const sling = slingshot(kit, appIcon.x + ZONE.side + SLING.offset, floor);
  const start = sling.pouch;
  const end = { x: applications.x, y: boxTop };
  const at = (t) => ({
    x: start.x + (end.x - start.x) * t,
    y: start.y + (end.y - start.y) * t - 4 * LIFT * t * (1 - t),
  });
  const split = at(SPLIT);
  const ahead = at(SPLIT + 0.001);
  const heading = Math.atan2(ahead.y - split.y, ahead.x - split.x);

  // The shot up to the split, in Wuu's own graphite.
  const trail = puffs(at, icon.bodyColor, {
    to: SPLIT, clear: 0, target: split, phase: { skip: 3, count: 0 }, scale: (t) => 0.55 + 0.45 * t / SPLIT,
  });

  const members = SQUAD.map((member) => {
    const half = member.wuu ? WUU_RADIUS * 1.1 : agent(kit, member, {}).half;
    const to = { x: applications.x + member.dx, y: boxTop - half - member.rise };
    // Leave the split fanned off the shot's heading, then curve into the V.
    const turn = heading + member.turn * Math.PI / 180;
    const reach = Math.hypot(to.x - split.x, to.y - split.y) * 0.45;
    const control = { x: split.x + Math.cos(turn) * reach, y: split.y + Math.sin(turn) * reach };
    const path = curve(split, control, to);
    const near = path(0.999);
    const direction = Math.atan2(to.y - near.y, to.x - near.x) * 180 / Math.PI;
    const color = member.wuu ? icon.bodyColor : kit.palette(member.hue, true, 0.5).head;
    const dots = puffs(path, color, {
      to: 1, clear: half * 1.9, target: to, phase: { skip: 2, count: trail.length }, scale: () => 1,
    });
    // Squash and stretch along the flight, eyes on where it is going. Agents
    // lean into the dive instead, so their silhouettes keep their shape.
    const stretch = member.wuu
      ? `rotate(${n(direction)}) scale(1.08 0.93) rotate(${n(-direction)})`
      : `rotate(${n((direction - 90) * 0.3)})`;
    const figure = member.wuu
      ? wuu(WUU_RADIUS, direction)
      : agent(kit, member, { yaw: 24 * Math.cos(direction * Math.PI / 180), pitch: -30 * Math.sin(direction * Math.PI / 180) }).svg;
    return {
      dots: dots.join(""),
      figure: `<g transform="translate(${n(to.x)} ${n(to.y)}) ${stretch}">${figure}</g>`,
    };
  });

  const castle = fort(kit, applications, floor);
  const watcher = agent(kit, LOOKOUT, { yaw: 16, pitch: 24 });
  const lookout = `<g transform="translate(${n(castle.deck.x)} ${n(castle.deck.y - watcher.half)})">${watcher.svg}</g>`;
  let queueEdge = appIcon.x - ZONE.side - 4;
  const queue = QUEUE.map((member) => {
    const x = queueEdge - member.size / 2;
    queueEdge -= member.size + 4;
    const figure = agent(kit, member, { yaw: 18, pitch: 26 });
    return footing(x, floor, member.size * (0.8 - member.hop / 60))
      + `<g transform="translate(${n(x)} ${n(floor - figure.half - member.hop)})">${figure.svg}</g>`;
  }).join("");

  return `<svg xmlns="http://www.w3.org/2000/svg" width="${WIDTH}" height="${HEIGHT}" viewBox="0 0 ${WIDTH} ${HEIGHT}">`
    + `<defs><radialGradient id="body" cx="0.375" cy="0.275" r="0.75">`
    + `<stop offset="0" stop-color="${icon.bodyHighlight}"/><stop offset="0.52" stop-color="${icon.bodyColor}"/><stop offset="1" stop-color="${icon.bodyShadow}"/>`
    + `</radialGradient><clipPath id="floor"><rect width="${WIDTH}" height="${n(floor)}"/></clipPath></defs>`
    + `<rect width="${WIDTH}" height="${HEIGHT}" fill="${PAPER}"/>`
    + sling.svg + queue
    + castle.svg + lookout
    + trail.join("")
    + members.map((member) => member.dots).join("")
    + popMarks(split.x, split.y, 12, -90, 42, 0.085)
    + members.map((member) => member.figure).join("")
    + `</svg>`;
}

// Rasterize separately at each scale to preserve sharp edges.
function renderInPage(svg, scale) {
  return new Promise((resolve, reject) => {
    const image = new Image();
    image.onload = () => {
      const canvas = document.createElement("canvas");
      canvas.width = image.naturalWidth * scale;
      canvas.height = image.naturalHeight * scale;
      canvas.getContext("2d").drawImage(image, 0, 0, canvas.width, canvas.height);
      resolve(canvas.toDataURL("image/png"));
    };
    image.onerror = () => reject(new Error("Cannot load the DMG background SVG"));
    image.src = `data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`;
  });
}

app.dock?.hide();
app.whenReady().then(async () => {
  const svg = drawBackground(loadAvatarKit());
  const window = new BrowserWindow({ show: false });
  await window.loadURL("about:blank");
  for (const [scale, name] of [[1, "dmg-background.png"], [2, "dmg-background@2x.png"]]) {
    const url = await window.webContents.executeJavaScript(`(${renderInPage})(${JSON.stringify(svg)}, ${scale})`);
    writeFileSync(path.join(BUILD_DIR, name), Buffer.from(url.split(",")[1], "base64"));
  }
  console.log("DMG backgrounds updated.");
  app.quit();
}).catch((error) => {
  console.error(error);
  app.exit(1);
});
