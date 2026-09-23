// Maintained documentation artwork, rendered from the desktop's foundation CSS.
// Run with Node; Electron uses a disposable profile and never starts the product.
const fs = require("node:fs");
const path = require("node:path");

const desktop = path.resolve(__dirname, "..");
const output = path.resolve(desktop, "../docs/en/assets/design-system");

if (!process.versions.electron) {
  const { spawnSync } = require("node:child_process");
  const temporaryRoot = path.join(desktop, ".tmp");
  fs.mkdirSync(temporaryRoot, { recursive: true });
  const temporary = fs.mkdtempSync(path.join(temporaryRoot, "design-system-"));
  try {
    const env = { ...process.env };
    delete env.ELECTRON_RUN_AS_NODE;
    const result = spawnSync(require("electron"), [__filename, temporary], {
      env, stdio: "inherit", timeout: 120_000,
    });
    if (result.error) throw result.error;
    process.exitCode = result.status ?? 1;
  } finally {
    fs.rmSync(temporary, { recursive: true, force: true });
  }
} else {
  const { app, BrowserWindow } = require("electron");
  const { buildSync } = require("esbuild");
  const temporary = process.argv[2];
  app.setPath("userData", path.join(temporary, "profile"));
  app.setPath("crashDumps", temporary);
  app.commandLine.appendSwitch("force-device-scale-factor", "2");
  app.commandLine.appendSwitch("force-color-profile", "srgb");
  app.on("window-all-closed", () => {});

  app.whenReady().then(async () => {
    const css = ["base", "spacing", "theme", "appearance"].map(name =>
      fs.readFileSync(path.join(desktop, `src/renderer/styles/${name}.css`), "utf8"),
    ).join("\n");
    const defaults = buildSync({
      stdin: {
        contents: `
          import { MESSAGE_FLOW_FONT_SIZE_RANGE } from './src/shared/protocol';
          import { appearanceDefaults, applyAppearance } from './src/renderer/AppearancePreferences';
          document.documentElement.style.setProperty('--conversation-message-font-size', MESSAGE_FLOW_FONT_SIZE_RANGE.default + 'px');
          applyAppearance(appearanceDefaults);
        `,
        resolveDir: desktop,
      },
      bundle: true, write: false, format: "iife", platform: "browser",
    }).outputFiles[0].text;
    const entry = path.join(temporary, "boards.html");
    fs.writeFileSync(entry, `<!doctype html><html lang="en"><meta charset="utf-8">
      <title>Wuu design system</title><style>${css}\n${boardCSS}</style><body></body></html>`);
    const window = new BrowserWindow({
      show: false, width: 1600, height: 2400, useContentSize: true,
      webPreferences: { sandbox: true, contextIsolation: true, nodeIntegration: false, backgroundThrottling: false, offscreen: true },
    });
    try {
      await window.loadFile(entry);
      await window.webContents.executeJavaScript(defaults);
      fs.mkdirSync(output, { recursive: true });
      let marker = 200;
      for (const theme of ["light", "dark"]) {
        for (const kind of ["colour", "geometry", "type"]) {
          marker++;
          // Offscreen paint events can still contain the previous frame. A
          // grayscale marker outside the exported crop identifies this board.
          const painted = new Promise(resolve => {
            const onPaint = (_event, _dirty, image) => {
              const scale = image.getSize().width / 1600;
              const pixel = image.crop({ x: Math.floor(1598 * scale), y: Math.floor(2398 * scale), width: 1, height: 1 }).toBitmap();
              if (pixel[0] !== marker || pixel[1] !== marker || pixel[2] !== marker) return;
              window.webContents.removeListener("paint", onPaint);
              resolve(image);
            };
            window.webContents.on("paint", onPaint);
          });
          const size = await window.webContents.executeJavaScript(`(${renderBoard})(${JSON.stringify(kind)}, ${JSON.stringify(theme)}, ${marker})`);
          if (size.height > 2390 || size.overflow) throw new Error(`Board overflow: ${kind}/${theme} ${JSON.stringify(size)}`);
          window.webContents.invalidate();
          const frame = await painted;
          const scale = frame.getSize().width / 1600;
          const image = frame.crop({ x: 0, y: 0, width: frame.getSize().width, height: Math.round(size.height * scale) });
          const filename = `${kind}-${theme}.png`;
          const artwork = image.resize({ width: 3200, height: size.height * 2 });
          fs.writeFileSync(path.join(output, filename), artwork.toPNG());
          console.log(`${filename}: ${artwork.getSize().width} × ${artwork.getSize().height}`);
        }
      }
    } finally {
      window.destroy();
    }
    app.quit();
  }).catch(error => { console.error(error); app.exit(1); });
}

// These dimensions belong to the documentation canvas, not product controls.
const boardCSS = `
html, body { height: auto; min-height: 0; overflow: visible; }
body { margin: 0; width: 1600px; background: var(--surface-2); color: var(--ink-strong); }
* { box-sizing: border-box; }
.board { margin: 48px; padding: 56px; border-radius: var(--radius-md); background: var(--paper); }
.masthead { display: flex; justify-content: space-between; align-items: center; margin-bottom: 40px; color: var(--ink-soft); font-size: 18px; }
.brand { display: flex; align-items: center; gap: 12px; color: var(--ink-strong); font-size: 24px; font-weight: 600; }
.brand::before { content: ''; width: 12px; height: 12px; border-radius: var(--radius-circle); background: var(--wuu-accent); }
h1 { margin: 0; font-size: 52px; line-height: 1.15; font-weight: 500; }
.intro { font-size: 22px; line-height: 1.6; color: var(--ink-soft); margin: 16px 0 40px; }
h2 { margin: 0 0 20px; font-size: 20px; font-weight: 500; color: var(--ink-soft); }
section + section { margin-top: 36px; }
.grid { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 24px; }
.swatch { height: 94px; border-radius: var(--radius-sm); margin-bottom: 12px; border: 1px solid var(--hairline); }
.name { font-size: 20px; line-height: 1.4; }
.token, .value { font: 17px/1.6 var(--font-mono); color: var(--ink-soft); overflow-wrap: anywhere; }
.footer { border-top: 1px solid var(--hairline); margin-top: 40px; padding-top: 24px; display: flex; justify-content: space-between; gap: 32px; font-size: 17px; line-height: 1.6; color: var(--ink-soft); }
.scale { display: flex; align-items: end; gap: 44px; padding: 8px 0 24px; }
.space-column { min-width: 116px; }
.space-block { background: var(--surface-4); border-radius: 4px; margin-bottom: 16px; }
.role-grid { display: grid; grid-template-columns: repeat(3, 1fr); gap: 16px 32px; margin-top: 20px; }
.role { border-top: 1px solid var(--hairline); padding-top: 12px; }
.radii { display: grid; grid-template-columns: repeat(6, 1fr); gap: 28px; }
.radius-shape { width: 112px; height: 112px; margin-bottom: 16px; background: var(--surface-2); border: 1px solid var(--hairline-strong); }
.radius-shape.pill { width: 160px; height: 80px; margin-block: 16px 32px; }
.elevations { display: grid; grid-template-columns: repeat(4, 1fr); gap: 32px; padding: 24px 0; }
.elevation { display: grid; place-content: center; height: 120px; border-radius: var(--radius-sm); background: var(--surface-1); border: 1px solid var(--hairline); margin-bottom: 20px; }
.note { font-size: 20px; line-height: 1.6; color: var(--ink-soft); margin: 16px 0 0; }
.type-row { display: grid; grid-template-columns: 290px minmax(0, 1fr) 160px; align-items: center; gap: 24px; min-height: 118px; padding: 24px 0; border-bottom: 1px solid var(--hairline); }
.type-sample { line-height: 1.4; overflow-wrap: anywhere; }
.type-value { font: 19px/1.6 var(--font-mono); text-align: right; color: var(--ink-soft); }
.weights { display: flex; justify-content: space-between; font-size: 28px; padding: 24px 0 8px; }
`;

async function renderBoard(kind, theme, marker) {
  document.documentElement.dataset.theme = theme;
  document.body.replaceChildren();
  const root = getComputedStyle(document.documentElement);
  const probe = document.createElement("div");
  probe.style.position = "absolute";
  probe.style.visibility = "hidden";
  document.body.append(probe);
  function value(token, property) {
    if (!root.getPropertyValue(token).trim()) throw new Error(`Missing token ${token}`);
    probe.style.setProperty(property, `var(${token})`);
    return getComputedStyle(probe).getPropertyValue(property);
  }
  function color(token) {
    const css = value(token, "background-color");
    const context = document.createElement("canvas").getContext("2d");
    context.fillStyle = css;
    context.fillRect(0, 0, 1, 1);
    const a = context.getImageData(0, 0, 1, 1).data[3];
    // Read opaque channels separately: premultiplied 8-bit translucent pixels
    // otherwise quantize the source RGB (especially at 6–7% opacity).
    context.fillStyle = `rgb(from ${css} r g b / 1)`;
    context.fillRect(0, 0, 1, 1);
    const [r, g, b] = context.getImageData(0, 0, 1, 1).data;
    return `#${[r, g, b].map(n => n.toString(16).padStart(2, "0")).join("").toUpperCase()}${a === 255 ? "" : ` / ${Math.round(a / 255 * 100)}%`}`;
  }
  const heading = { colour: "Colour / 色彩", geometry: "Space, radius & elevation / 空间与层级", type: "Typography / 排版" }[kind];
  const descriptions = {
    colour: "Semantic roles, not a decorative palette. / 按用途选择颜色，而不是按色值。",
    geometry: "Compact controls. Comfortable reading. / 紧凑的操作区，舒展的阅读区。",
    type: "One UI baseline. Independent code size. / 统一界面基线，代码字号独立。",
  };
  let content = "";
  if (kind === "colour") {
    const groups = [
      ["Surfaces / 表面", [["Canvas / 画布", "--paper"], ["Surface / 基础表面", "--surface-1"], ["Muted / 次级表面", "--surface-2"], ["Raised / 强调表面", "--surface-3"]]],
      ["Text / 文字", [["Strong / 强调", "--ink-strong"], ["Body / 正文", "--ink"], ["Secondary / 次要", "--ink-soft"], ["Muted / 弱化", "--ink-muted"]]],
      ["Lines / 边界", [["Divider / 内部分隔", "--hairline-soft"], ["Frame / 容器边界", "--hairline"], ["Strong / 强边界", "--hairline-strong"], ["Control / 功能边界", "--control-boundary"]]],
      ["Accent & interaction / 品牌与交互", [["Brand / 品牌朱红", "--wuu-accent"], ["Pressed / 品牌按下", "--wuu-accent-press"], ["Slider / 滑块强调", "--interaction-accent"], ["Focus / 键盘焦点", "--focus-ring"]]],
      ["Status / 状态", [["Success / 成功", "--success"], ["Warning / 注意", "--warning"], ["Danger / 危险", "--danger"], ["Info / 信息", "--info"]]],
      ["Status surfaces / 状态底色", [["Success tint / 成功底色", "--success-soft"], ["Warning tint / 注意底色", "--warning-soft"], ["Danger tint / 危险底色", "--danger-soft"], ["Selection / 选中底色", "--selection-surface"]]],
    ];
    content = groups.map(([label, tokens]) => `<section><h2>${label}</h2><div class="grid">${tokens.map(([name, token]) => `<div><div class="swatch" style="background:var(${token})"></div><div class="name">${name}</div><div class="token">${token}</div><div class="value">${color(token)}</div></div>`).join("")}</div></section>`).join("");
    content += '<p class="note">Muted text and status fills are not universal text colours. Check each foreground/background pair.<br>弱化文字与状态填充色不能任意用作正文；对比度必须按实际前景和背景检查。</p>';
  } else if (kind === "geometry") {
    const spaces = [1, 2, 3, 4, 5, 6, 8].map(n => `--space-${n}`);
    content = `<section><h2>Space / 间距 · diagrams at 2× / 图示放大两倍</h2><div class="scale">${spaces.map(token => {
      const pixels = parseFloat(value(token, "width"));
      return `<div class="space-column"><div class="space-block" style="width:${pixels * 2}px;height:${pixels * 2}px"></div><div class="name">${pixels}px</div><div class="token">${token}</div></div>`;
    }).join("")}</div><div class="role-grid">${[
      ["Page / 页面", "--page-padding"], ["Section / 分组", "--section-gap"], ["Heading / 标题到内容", "--section-heading-gap"],
      ["Card / 卡片内缩", "--card-padding"], ["Panel / 面板内缩", "--panel-padding"], ["Menu / 菜单内缩", "--menu-inset"],
    ].map(([name, token]) => `<div class="role"><div class="name">${name} · ${value(token, "width")}</div><div class="token">${token}</div></div>`).join("")}</div></section>`;
    const radii = [["Inner / 内层", "--radius-xs"], ["Control / 控件", "--radius-sm"], ["Panel / 面板", "--radius-md"], ["Menu / 紧凑菜单", "--menu-shell-radius"], ["Pill / 胶囊", "--radius-pill"], ["Circle / 圆形", "--radius-circle"]];
    content += `<section><h2>Radius / 圆角 · actual CSS radii / 圆角按实际 CSS 值绘制</h2><div class="radii">${radii.map(([name, token]) => `<div><div class="radius-shape${token === "--radius-pill" ? " pill" : ""}" style="border-radius:var(${token})"></div><div class="name">${name}</div><div class="token">${token}</div><div class="value">${value(token, "border-top-left-radius")}</div></div>`).join("")}</div><p class="note">Menu shell = inner radius + inset. / 菜单外层圆角 = 内层圆角 + 内缩。<br>Panel/dialog overlays use --menu-radius; --radius-lg aliases --radius-md. / 面板式浮层另用对应角色。</p></section>`;
    content += `<section><h2>Elevation / 层级 · production shadow recipes / 产品实际阴影配方</h2><div class="elevations">${[["Control / 控件", "--shadow-soft"], ["Card / 卡片", "--shadow-card"], ["Popover / 浮层", "--shadow-pop"], ["Modal / 对话框", "--shadow-modal"]].map(([name, token]) => `<div><div class="elevation" style="box-shadow:var(${token})"><span class="name">${name}</span></div><div class="token">${token}</div></div>`).join("")}</div><p class="note">${theme === "light" ? "Quiet ambient shadows separate overlapping surfaces. / 用克制的环境阴影区分叠放关系。" : "Inset highlights define dark surfaces; overlays retain black ambient shadows. / 深色表面以内描边建立层级，浮层保留黑色环境阴影。"}<br>Do not raise elevation on hover. / 不因悬停而提升层级。</p></section>`;
    content += `<section><h2>Control floors / 控件下限</h2><div class="role-grid"><div class="role"><div class="name">Field / 表单 · ${value("--control-field-height", "min-height")}</div><div class="token">--control-field-height</div></div><div class="role"><div class="name">Menu row / 菜单行 · ${value("--control-row-height", "min-height")}</div><div class="token">--control-row-height</div></div><div class="role"><div class="name">Coarse pointer / 触控 · ≥44px</div><div class="token">spacing.css · pointer: coarse</div></div></div><p class="note">Density changes whitespace, not minimum targets. Text can grow. / 密度调整留白，不缩小点击下限；文字增长时允许控件增高。</p></section>`;
  } else {
    const rows = [
      ["Hero / 大标题", "--font-hero", "Start with a clear intent.", "--weight-medium"],
      ["Display / 展示标题", "--font-display", "把想法变成可用的工具", "--weight-medium"],
      ["Heading / 分区标题", "--font-heading", "Workspace / 工作空间", "--weight-medium"],
      ["Title / 小节标题", "--font-title", "Review the changes / 检查变更", "--weight-medium"],
      ["UI & body / 界面与正文", "--font-ui", "清楚地阅读，专注地完成工作。", "--weight-regular"],
      ["Menu / 菜单与紧凑控件", "--font-menu", "Open workspace / 打开工作区", "--weight-medium"],
      ["Metadata / 辅助信息", "--font-xs", "Updated just now / 刚刚更新", "--weight-regular"],
      ["Code / 代码", "--appearance-code-size", 'const task = await run("review");', "--weight-regular"],
    ];
    content = `<section><h2>Role → token → reference size / 角色 → 变量 → 参考字号 · samples at 2× / 示例放大两倍</h2>${rows.map(([name, token, sample, weight]) => {
      const pixels = parseFloat(value(token, "font-size"));
      return `<div class="type-row"><div><div class="name">${name}</div><div class="token">${token}</div></div><div class="type-sample" style="font-size:${pixels * 2}px;font-weight:var(${weight});${token === "--appearance-code-size" ? 'font-family:var(--font-mono)' : ''}">${sample}</div><div class="type-value">${Number(pixels.toFixed(2))}px / ${root.getPropertyValue(weight).trim()}</div></div>`;
    }).join("")}</section>`;
    content += `<section><h2>Weight / 字重</h2><div class="weights">${[["Regular", "--weight-regular"], ["Medium", "--weight-medium"], ["Semibold", "--weight-semibold"], ["Bold", "--weight-bold"]].map(([name, token]) => `<span style="font-weight:var(${token})">${root.getPropertyValue(token).trim()} ${name}</span>`).join("")}</div><p class="note">Regular by default. Emphasis is selective. / 默认常规字重，仅对必要内容强调。</p></section>`;
    content += `<section><h2>Reading rhythm / 阅读节奏</h2><div class="role-grid">${[["UI / 界面", "--line-ui"], ["Body / 正文", "--line-body"], ["Metadata / 辅助", "--line-meta"]].map(([name, token]) => `<div class="role"><div class="name">${name} · ${root.getPropertyValue(token).trim()}</div><div class="token">${token}</div></div>`).join("")}</div><p class="note">System UI sans-serif + CJK fallback; monospace for code. User font choices remain authoritative.<br>系统界面字体与中文回退；代码使用等宽字体。尊重用户字体偏好，不压缩字距。</p></section>`;
  }
  const ui = value("--font-ui", "font-size");
  const code = value("--appearance-code-size", "font-size");
  probe.remove();
  document.body.insertAdjacentHTML("beforeend", `<main class="board"><header class="masthead"><span class="brand">wuu / design system</span><span>Desktop foundations · ${theme === "light" ? "Light / 浅色" : "Dark / 深色"}</span></header><h1>${heading}</h1><p class="intro">${descriptions[kind]}</p>${content}<footer class="footer"><span>Built-in theme · UI ${ui} / code ${code} · density 1<br>内置主题参考值；主题、字体与密度偏好可覆盖。</span><span>Source / 来源<br>base.css · spacing.css · theme.css · appearance.css</span></footer></main>`);
  await document.fonts.ready;
  await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
  document.body.insertAdjacentHTML("beforeend", `<div style="position:absolute;left:1596px;top:2396px;width:4px;height:4px;background:rgb(${marker},${marker},${marker})"></div>`);
  const board = document.querySelector(".board");
  return { height: Math.ceil(board.getBoundingClientRect().bottom + 48), overflow: document.documentElement.scrollWidth > 1600 };
}
