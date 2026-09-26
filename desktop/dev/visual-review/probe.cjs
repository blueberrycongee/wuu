// Baseline audit probe: start the desktop development server, then run
//   ./node_modules/.bin/electron dev/visual-review/probe.cjs
// from desktop/. It reports rendered geometry and typography for the fixtures,
// so spacing/tracking decisions come from measurements rather than guesses.
const { app, BrowserWindow } = require("electron");
const fs = require("node:fs");
const path = require("node:path");

const output = path.resolve(__dirname, "../../../artifacts/visual-review");
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", fs.mkdtempSync(path.join(output, "probe-profile-")));
const origin = process.env.WUU_FIXTURE_ORIGIN || "http://127.0.0.1:5173";

const surfaces = [
  { name: "design-controls", url: "/dev/design-system/?surface=controls", width: 1280, height: 900 },
  { name: "three-pane", url: "/dev/three-pane/?theme=light&size=14.5", width: 1440, height: 900 },
  { name: "message-flow-conversation", url: "/dev/message-flow-reading/?surface=conversation&theme=light&size=14.5", width: 1280, height: 900 },
  { name: "message-flow-prose", url: "/dev/message-flow-reading/?theme=light&size=14.5", width: 1280, height: 900 },
];

const report = () => {
  const rect = node => {
    const r = node.getBoundingClientRect();
    return {
      top: Math.round(r.top * 10) / 10, bottom: Math.round(r.bottom * 10) / 10,
      left: Math.round(r.left * 10) / 10, right: Math.round(r.right * 10) / 10,
      width: Math.round(r.width * 10) / 10, height: Math.round(r.height * 10) / 10,
    };
  };
  const style = node => {
    const s = getComputedStyle(node);
    return {
      fontSize: s.fontSize, lineHeight: s.lineHeight, letterSpacing: s.letterSpacing,
      fontWeight: s.fontWeight, gap: s.gap, padding: s.padding, margin: s.margin,
      fontFamily: s.fontFamily.split(",")[0],
    };
  };
  const describe = (label, selector, depth = 0) => {
    const nodes = [...document.querySelectorAll(selector)].slice(0, 4);
    return nodes.map((node, index) => {
      const entry = { label: `${label}${nodes.length > 1 ? `[${index}]` : ""}`, rect: rect(node), style: style(node) };
      if (depth > 0) {
        entry.children = [...node.children].slice(0, depth * 4).map(child => ({
          tag: child.className || child.tagName,
          rect: rect(child),
          style: style(child),
        }));
      }
      return entry;
    });
  };
  const gradients = [];
  const verticals = [...document.querySelectorAll(".turn, .user-message-block, .agent-block-with-action-slot, .message, .settings-row, .settings-section")]
    .slice(0, 40).map(node => ({ tag: node.className.split(" ")[0], rect: rect(node) }));
  for (let i = 1; i < verticals.length; i++) {
    gradients.push({
      from: verticals[i - 1].tag, to: verticals[i].tag,
      gap: Math.round((verticals[i].rect.top - verticals[i - 1].rect.bottom) * 10) / 10,
    });
  }
  return {
    viewport: { width: window.innerWidth, height: window.innerHeight },
    documentFontSize: getComputedStyle(document.documentElement).fontSize,
    body: style(document.body),
    samples: [
      ...describe("sidebar-row", ".sidebar .thread-row, .sidebar .nav-item", 0),
      ...describe("user-block", ".user-message-block", 1),
      ...describe("user-bubble", ".user-message", 0),
      ...describe("user-actions", ".user-message-actions", 1),
      ...describe("agent-actions", ".agent-message-actions", 1),
      ...describe("paragraph", ".rich-content .rich-paragraph", 0),
      ...describe("settings-row", ".settings-row", 1),
      ...describe("settings-section", ".settings-section", 0),
      ...describe("turn", ".turn", 0),
    ],
    verticals,
    gradients,
  };
};

app.whenReady().then(async () => {
  const win = new BrowserWindow({
    show: false, width: 1280, height: 900,
    webPreferences: { contextIsolation: true, nodeIntegration: false, backgroundThrottling: false },
  });
  const out = {};
  const only = process.env.WUU_PROBE_ONLY;
  for (const surface of surfaces) {
    if (only && !surface.name.includes(only)) continue;
    win.setContentSize(surface.width, surface.height);
    await win.loadURL(`${origin}${surface.url}`);
    await win.webContents.executeJavaScript("new Promise(r => setTimeout(r, 900))");
    out[surface.name] = await win.webContents.executeJavaScript(`(${report.toString()})()`, true);
    console.log(`probed ${surface.name}`);
  }
  fs.writeFileSync(path.join(output, "probe.json"), JSON.stringify(out, null, 2));
  app.exit(0);
}).catch(error => { console.error(error); app.exit(1); });
