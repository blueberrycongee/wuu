/**
 * Draw the DMG window background around the icon positions in `build.dmg`
 * and render it into build/dmg-background.png and @2x: a slingshot beside the
 * app has just fired Wuu on an arc into Applications. Run
 * `npm run dmg-background:generate` from desktop on macOS, so the text uses the
 * system fonts Finder users see, then commit both PNGs; electron-builder
 * combines the pair into one HiDPI TIFF while packaging.
 */
const { app, BrowserWindow } = require("electron");
const { writeFileSync } = require("node:fs");
const path = require("node:path");
const { build } = require("../package.json");
const icon = require("../../assets/app-icon-source.json");

const BUILD_DIR = path.join(__dirname, "..", "build");
const WIDTH = 720;
const HEIGHT = 420;
// Selecting a 128 pt icon draws a box this far from its centre, and the label
// follows below it. Artwork stays clear of the box. Finder labels are always
// black over a picture, so the light ground starts between box and label.
// Finder also hides the bottom 32 pt of the picture under its title-bar offset.
const ZONE = { side: 72, top: 73, box: 69, gap: 6 };

const SKY = "#ff3d00";
const GROUND = "#f0eee6";
const WHITE = "#ffffff";
const INK = "#191a18";
const SLING = { offset: 44, height: 92, spread: 24, trunk: 16, arm: 14 };
// How high the shot climbs above the straight line into Applications.
const LIFT = 204;
const FLYER_RADIUS = 22;
const TRAIL_SPACING = 13;

const n = (value) => Number(value.toFixed(2));

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

// The icon's three pop marks, fanned around a point as if around a body.
function popMarks(x, y, radius, towardDegrees, spreadDegrees) {
  return icon.marks.map((mark, i) => {
    const degrees = towardDegrees + (i - 1) * spreadDegrees + mark.angle;
    const angle = degrees * Math.PI / 180;
    const distance = radius + (icon.fanGap + mark.gap + mark.length / 2) * radius / icon.radius;
    return pill(x + distance * Math.cos(angle), y + distance * Math.sin(angle),
      mark.length * radius / icon.radius, mark.width * radius / icon.radius, degrees, icon.markColor);
  }).join("");
}

function slingshot(x, ground) {
  const forkY = ground - SLING.height * 0.47;
  const arm = SLING.height * 0.5;
  const tips = [-1, 1].map((side) => {
    const angle = side * SLING.spread * Math.PI / 180;
    return { side, x: x + Math.sin(angle) * arm, y: forkY - Math.cos(angle) * arm };
  });
  const [left, right] = tips;
  // Just released: the band still bows toward the shot.
  const band = `<path d="M${n(left.x)} ${n(left.y)}Q${n(x)} ${n(left.y - 16)} ${n(right.x)} ${n(right.y)}" fill="none" stroke="${INK}" stroke-width="3.2" stroke-linecap="round"/>`;
  // The trunk runs under the ground so the slingshot reads as planted.
  const trunk = pill(x, (forkY + ground) / 2 + 8, SLING.trunk, ground - forkY + 24, 0, icon.bodyShadow);
  const arms = tips.map((tip) => pill((x + tip.x) / 2, (forkY + tip.y) / 2, SLING.arm, arm + SLING.arm, tip.side * SLING.spread, icon.bodyShadow));
  const wraps = tips.map((tip) => pill(tip.x, tip.y + 8, 16, 5, tip.side * SLING.spread, WHITE));
  return { svg: band + trunk + arms.join("") + wraps.join(""), pouch: { x, y: left.y - 8 } };
}

function drawBackground() {
  const [appIcon, applications] = build.dmg.contents;
  const ground = applications.y + ZONE.box + 2;
  const sling = slingshot(appIcon.x + ZONE.side + SLING.offset, ground);
  const start = sling.pouch;
  const end = { x: applications.x - 4, y: applications.y + 10 };
  const at = (t) => ({
    x: start.x + (end.x - start.x) * t,
    y: start.y + (end.y - start.y) * t - 4 * LIFT * t * (1 - t),
  });

  // Wuu is caught where the descent first reaches the Applications selection box.
  const reach = applications.y - ZONE.top - ZONE.gap - FLYER_RADIUS * 1.15;
  let flight = 0.5;
  while (at(flight).y < reach) flight += 0.001;
  const flyer = at(flight);
  const next = at(flight + 0.001);
  const heading = Math.atan2(next.y - flyer.y, next.x - flyer.x) * 180 / Math.PI;

  // Angry-Birds-style trail: alternating puffs behind the shot, older ones smaller.
  const trail = [];
  let travelled = 0;
  let previous = start;
  for (let t = 0; t < flight; t += 0.0005) {
    const point = at(t);
    travelled += Math.hypot(point.x - previous.x, point.y - previous.y);
    previous = point;
    const toFlyer = Math.hypot(flyer.x - point.x, flyer.y - point.y);
    if (travelled >= TRAIL_SPACING * (trail.length + 5) && toFlyer > FLYER_RADIUS * 1.9) {
      const radius = (trail.length % 2 ? 2 : 3.6) * (0.5 + 0.5 * t / flight);
      trail.push(`<circle cx="${n(point.x)}" cy="${n(point.y)}" r="${n(radius)}" fill="${WHITE}"/>`);
    }
  }

  return `<svg xmlns="http://www.w3.org/2000/svg" width="${WIDTH}" height="${HEIGHT}" viewBox="0 0 ${WIDTH} ${HEIGHT}">`
    + `<defs><radialGradient id="body" cx="0.375" cy="0.275" r="0.75">`
    + `<stop offset="0" stop-color="${icon.bodyHighlight}"/><stop offset="0.52" stop-color="${icon.bodyColor}"/><stop offset="1" stop-color="${icon.bodyShadow}"/>`
    + `</radialGradient></defs>`
    + `<rect width="${WIDTH}" height="${HEIGHT}" fill="${SKY}"/>`
    + sling.svg
    + `<rect y="${n(ground)}" width="${WIDTH}" height="${n(HEIGHT - ground)}" fill="${GROUND}"/>`
    + `<g fill="${WHITE}">`
    + `<text x="40" y="60" font-family="system-ui, BlinkMacSystemFont, sans-serif" font-size="24" font-weight="800" letter-spacing="-0.4">Drag wuu into Applications</text>`
    + `<text x="41" y="86" font-family="'PingFang SC', system-ui, sans-serif" font-size="14" font-weight="600">把 wuu 拖进 Applications 即可安装</text>`
    + `</g>`
    + trail.join("")
    + popMarks(start.x, start.y + 6, 40, -90, 32)
    // Squash and stretch along the flight, eyes on where it is going.
    + `<g transform="translate(${n(flyer.x)} ${n(flyer.y)}) rotate(${n(heading)}) scale(1.12 0.9) rotate(${n(-heading)})">${wuu(FLYER_RADIUS, heading)}</g>`
    + `</svg>`;
}

// Rasterize separately at each scale to preserve sharp text and edges.
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

if (process.platform !== "darwin") {
  console.error("Generate DMG backgrounds on macOS to use the Finder system fonts.");
  app.exit(1);
} else {
  app.dock?.hide();
  app.whenReady().then(async () => {
    const svg = drawBackground();
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
}
