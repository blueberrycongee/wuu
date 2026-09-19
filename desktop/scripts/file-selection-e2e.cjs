// Build and run from desktop: npm run test:e2e:file-selection
// To rerun an existing build: ./node_modules/.bin/electron scripts/file-selection-e2e.cjs
// WUU_E2E_VISIBLE=true shows the fixture window. WUU_E2E_OUTPUT selects artifacts.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { app, BrowserWindow } = require("electron");

const desktopRoot = path.resolve(__dirname, "..");
const renderer = process.env.WUU_E2E_RENDERER || path.join(desktopRoot, "out/renderer/index.html");
const output = process.env.WUU_E2E_OUTPUT || fs.mkdtempSync(path.join(os.tmpdir(), "wuu-file-selection-e2e-"));
fs.mkdirSync(output, { recursive: true });
app.setPath("userData", path.join(output, "profile"));
process.env.WUU_FILE_SELECTION_E2E_CWD = path.resolve(desktopRoot, "..");
const report = { renderer, output, cases: [], screenshots: [], errors: [], layoutObservations: [] };
let win;
let phase = "startup";
app.on("window-all-closed", () => {});
app.whenReady().then(run).then(() => app.quit()).catch(async error => {
  report.errors.push({ phase, message: error.stack || String(error) });
  if (win && !win.isDestroyed()) {
    try {
      await screenshot("failure");
      report.failureDOM = await evaluate(() => ({ text: document.body.innerText, active: document.activeElement?.outerHTML,
        inputs: Array.from(document.querySelectorAll("textarea")).map(input => ({ value: input.value, visible: Boolean(input.getBoundingClientRect().width) })) }));
      report.failureFixture = await snapshot();
    } catch { /* A crashed renderer cannot provide diagnostics. */ }
  }
  writeReport();
  console.error(`File selection E2E failed at ${phase}:`, error);
  console.error(`Artifacts: ${output}`);
  app.exit(1);
});

async function run() {
  console.log(`File selection E2E artifacts: ${output}`);
  assert.ok(fs.existsSync(renderer), "Renderer missing. Run npm run test:e2e:file-selection from desktop.");
  // Pairwise visual coverage, with every action repeated on all three formats.
  const variants = [
    { name: "light-default-wide", theme: "light", size: 14, width: 1380, height: 900 },
    { name: "dark-large-wide", theme: "dark", size: 20, width: 1380, height: 900 },
    { name: "light-large-narrow", theme: "light", size: 20, width: 760, height: 900 },
    { name: "dark-default-narrow", theme: "dark", size: 14, width: 760, height: 900 },
  ];
  for (const variant of variants) {
    phase = `${variant.name}: startup`;
    win = new BrowserWindow({
      width: variant.width, height: variant.height, show: process.env.WUU_E2E_VISIBLE === "true",
      webPreferences: { preload: path.join(__dirname, "file-selection-e2e-preload.cjs"),
        contextIsolation: true, nodeIntegration: false, sandbox: false, backgroundThrottling: false,
        partition: `selection-${variant.name}-${Date.now()}` },
    });
    win.setContentSize(variant.width, variant.height);
    win.webContents.on("render-process-gone", (_event, details) => {
      report.errors.push({ phase, message: `Renderer exited: ${details.reason}` });
    });
    // Fail closed on network access. The fixture must never reach a provider.
    win.webContents.session.webRequest.onBeforeRequest({ urls: ["http://*/*", "https://*/*", "ws://*/*", "wss://*/*"] }, (details, callback) => {
      report.errors.push({ phase, message: `Unexpected network request: ${details.url}` });
      callback({ cancel: true });
    });
    await win.loadFile(renderer);
    await waitFor(() => Boolean(document.querySelector(".conversation-pane .turn")), "active fixture session");
    await evaluate(({ theme, size }) => {
      document.documentElement.dataset.theme = theme;
      document.documentElement.style.setProperty("--conversation-message-font-size", `${size}px`);
      window.dispatchEvent(new Event("wuu-content-size-change"));
    }, variant);
    await openFiles();
    for (const file of ["selection-guide.md", "selection-code.ts", "selection-notes.txt"]) {
      const label = `${variant.name}-${path.extname(file).slice(1)}`;
      console.log(`RUN ${label}`);
      phase = `${label}: open file`;
      await openFile(file);
      const initial = await snapshot();
      const text = initial.files[file];
      const quote = file.endsWith(".md") ? "Select this Markdown passage for review." : text;
      const draft = `Keep this independent draft (${label}).`;
      await fill("[data-main-conversation-composer] textarea", draft);

      phase = `${label}: quote`;
      await selectFile(file, quote);
      await screenshot(`${label}-toolbar`);
      await clickButton(".file-selection-popup", "Add to conversation");
      await waitFor(() => Boolean(document.querySelector("[data-main-conversation-composer] .file-selection-quote-chip")), "selected text tag");
      assert.equal(await evaluate(composerValue), draft, "Selected text must remain folded instead of expanding into the draft");
      assert.equal(await evaluate(() => Boolean(document.querySelector(".file-selection-comments"))), false, "A quote must not create a document comment");
      const tag = await visibleGeometry("[data-main-conversation-composer] .file-selection-quote-chip .file-selection-tag");
      win.webContents.sendInputEvent({ type: "mouseMove", x: Math.round(tag.x + tag.width / 2), y: Math.round(tag.y + tag.height / 2) });
      await waitFor(expected => document.querySelector(".file-selection-quote-text")?.textContent === expected, "hovered original text", quote);
      await visibleGeometry(".file-selection-quote-panel");
      await screenshot(`${label}-quote-tag`);
      await press("Escape");
      await click("[data-main-conversation-composer] .file-selection-quote-remove");
      await waitFor(() => !document.querySelector("[data-main-conversation-composer] .file-selection-quote-chip"), "quote removed");
      assert.equal(await evaluate(composerValue), draft, "Removing a quote must preserve typed text");
      await selectFile(file, quote);
      await clickButton(".file-selection-popup", "Add to conversation");
      assert.equal((await snapshot()).submissions.length, initial.submissions.length, "Adding a quote must not submit a turn");
      await click("[data-main-conversation-composer] .composer-send-button");
      await waitFor(count => window.selectionE2E.snapshot().submissions.length === count, "quoted text submission", initial.submissions.length + 1);
      const quoted = (await snapshot()).submissions.at(-1);
      assert.equal(quoted.contentParts?.[0]?.intent, "quote", "Selected text must be a distinct quote attachment");
      assert.equal(quoted.contentParts[0].source.quote, quote, "The hidden quote must preserve the exact original text");
      assert.equal(quoted.contentParts[1].text, draft, "The typed draft must be preserved alongside the quote");
      assert.equal(quoted.prompt, quoted.contentParts.map(part => part.text).join("").trim(), "The canonical submission must include the folded quote and draft");
      await evaluate(() => window.selectionE2E.complete());
      await waitFor(() => composerValue() === "", "quoted draft sent");
      await fill("[data-main-conversation-composer] textarea", draft);

      phase = `${label}: comment`;
      await selectFile(file, quote);
      await clickButton(".file-selection-popup", "Comment");
      const comment = `Explain this selection (${label}).`;
      await fill(".file-selection-popup textarea", comment);
      await screenshot(`${label}-comment-form`);
      await clickButton(".file-selection-popup", "Save comment");
      await waitFor(() => Boolean(document.querySelector("[data-main-conversation-composer] .file-selection-tag")), "comment chip");
      assert.equal(await evaluate(composerValue), draft, "Comment attachment must not replace the visible draft");
      phase = `${label}: document comment visibility`;
      const commentLayout = await evaluate(() => {
        const aside = document.querySelector(".workspace-file-resource.active .file-selection-comments");
        if (!aside) return null;
        return Array.from(aside.querySelectorAll("button, .file-selection-comment > p, blockquote")).map(element => {
          const rect = element.getBoundingClientRect();
          const hit = document.elementFromPoint(rect.x + rect.width / 2, rect.y + rect.height / 2);
          return { label: element.textContent, bounds: rect.toJSON(), reachable: element.contains(hit),
            inViewport: rect.width > 0 && rect.height > 0 && rect.x >= 0 && rect.y >= 0 && rect.right <= innerWidth && rect.bottom <= innerHeight,
            coveringElement: hit?.className };
        });
      });
      report.layoutObservations.push({ label, comments: commentLayout });
      assert.ok(commentLayout?.length >= 5, "Saved document comment must expose quote, text, and Locate/Edit/Remove controls");
      for (const target of commentLayout) assert.ok(target.inViewport && target.reachable,
        `Document comment content must be visible and hit-testable: ${JSON.stringify(target)}`);
      await screenshot(`${label}-document-comment`);
      await click("[data-main-conversation-composer] .file-selection-tag");
      await waitFor(expected => document.querySelector(".file-selection-panel")?.textContent.includes(expected), "comment chip details", comment);
      await click(".file-selection-panel details summary");
      assert.equal(await evaluate(() => document.querySelector(".file-selection-panel pre")?.textContent), quote);
      await screenshot(`${label}-comment-chip`);
      await press("Escape");

      phase = `${label}: inline edit preserving draft and comment`;
      await selectFile(file, quote);
      await clickButton(".file-selection-popup", "Edit");
      const instruction = `Replace the selected content (${label}).`;
      await fill(".file-selection-popup textarea", instruction);
      await screenshot(`${label}-edit-form`);
      await clickButton(".file-selection-popup", "Send edit request");
      await waitFor(count => window.selectionE2E.snapshot().submissions.length === count, "edit submission", initial.submissions.length + 2);
      const edit = (await snapshot()).submissions.at(-1);
      assert.equal(edit.contentParts?.length, 1, "Inline edit must submit only its own attachment");
      verifySource(edit.contentParts[0], file, text, quote, "edit", instruction);
      assert.equal(edit.activeDocument?.path, file, "Inline edit should target the selected document");
      assert.ok(!edit.prompt.includes(draft), "Inline edit must not consume the unrelated draft");
      assert.equal(await evaluate(composerValue), draft);
      assert.ok(await evaluate(() => Boolean(document.querySelector("[data-main-conversation-composer] .file-selection-tag"))), "Pending comment must survive inline edit");

      phase = `${label}: completion refresh`;
      const updated = text.replace(file.endsWith(".md") ? quote : text.split("\n")[0], "Updated selection after completion.");
      const beforeReads = (await snapshot()).reads.length;
      await evaluate(changes => window.selectionE2E.complete(changes), { [file]: updated });
      await waitFor(({ count, file, updated }) => {
        const state = window.selectionE2E.snapshot();
        return state.reads.slice(count).some(read => read.path === file && read.text === updated);
      }, "file re-read after completion", { count: beforeReads, file, updated });
      await waitFor(() => document.querySelector(".workspace-file-resource.active .file-selection-content")?.textContent.replace(/\u00a0/g, " ").includes("Updated selection after completion."), "updated file rendered");
      assert.equal(await evaluate(composerValue), draft, "Completion refresh must preserve the draft");
      await screenshot(`${label}-refreshed`);

      phase = `${label}: send pending comment`;
      await waitFor(() => {
        const button = document.querySelector("[data-main-conversation-composer] .composer-send-button");
        return button && !button.disabled;
      }, "composer ready after completion");
      await click("[data-main-conversation-composer] .composer-send-button");
      await waitFor(count => window.selectionE2E.snapshot().submissions.length === count, "comment submission", initial.submissions.length + 3);
      const sent = (await snapshot()).submissions.at(-1);
      const parts = sent.contentParts?.filter(part => part.type === "file_selection") ?? [];
      assert.equal(parts.length, 1, "Comment must be sent as one structured attachment");
      verifySource(parts[0], file, text, quote, "comment", comment);
      assert.ok(sent.prompt.includes(draft), "Normal send must include the main draft");
      await evaluate(() => window.selectionE2E.complete());
      await waitFor(() => composerValue() === "" && !document.querySelector("[data-main-conversation-composer] .file-selection-tag"), "sent composer cleared");
      report.cases.push({ variant, file, quote, quoted, edit, sent, checks: ["toolbar", "quote-payload", "document-comment-hit-tests", "comment-chip", "inline-edit-preserves-draft-and-comment", "completion-refresh", "comment-payload"] });
      writeReport();
      console.log(`PASS ${label}`);
    }
    win.destroy();
    win = undefined;
  }
  assert.deepEqual(report.errors, [], "Unexpected runtime or network errors");
  writeReport();
  console.log(`PASS ${report.cases.length} file selection cases. Report and screenshots: ${output}`);
}

function verifySource(part, file, text, quote, intent, comment) {
  assert.equal(part.type, "file_selection");
  assert.equal(part.intent, intent);
  assert.equal(part.comment, comment);
  assert.equal(part.source.workspace, process.env.WUU_FILE_SELECTION_E2E_CWD);
  assert.equal(part.source.path, file);
  assert.equal(part.source.quote, quote);
  assert.ok(part.source.revision, "Selection must capture a file revision");
  const offset = (line, column) => text.split("\n").slice(0, line - 1).reduce((sum, item) => sum + item.length + 1, 0) + column - 1;
  assert.equal(text.slice(offset(part.source.start_line, part.source.start_column), offset(part.source.end_line, part.source.end_column)), quote,
    "Submitted range must resolve to the exact quoted source");
}

async function openFiles() {
  await evaluate(() => {
    const panel = document.querySelector(".workspace-right-panel");
    if (panel?.getAttribute("aria-hidden") !== "false") {
      const toggle = Array.from(document.querySelectorAll(".side-panel-toggle-button")).find(button => /right sidebar|右侧栏/i.test(button.getAttribute("aria-label") || ""));
      if (!toggle) throw new Error("Right panel toggle missing");
      toggle.click();
    }
  });
  await click(".workspace-panel-add");
  await clickButton(".workspace-tool-menu", "Files", ".workspace-tool-menu-item");
  await waitFor(() => Boolean(document.querySelector(".workspace-file-tree-frame file-tree-container")?.shadowRoot?.querySelector("[data-item-path]")), "file tree");
}

async function openFile(file) {
  await evaluate(file => {
    const root = document.querySelector(".workspace-file-tree-frame file-tree-container")?.shadowRoot;
    const row = Array.from(root?.querySelectorAll("[data-item-path]") ?? []).find(row => row.getAttribute("data-item-path") === file);
    if (!row) throw new Error(`File row missing: ${file}`);
    row.click();
  }, file);
  await waitFor(file => window.selectionE2E.snapshot().reads.some(read => read.path === file) && Boolean(document.querySelector(".workspace-file-resource.active .file-selection-surface")), "selection surface", file);
  await evaluate(() => {
    const expand = Array.from(document.querySelectorAll("button")).find(button => /expand to full panel|展开为全面板/i.test(button.getAttribute("aria-label") || ""));
    expand?.click();
  });
  await waitFor(() => Boolean(document.querySelector(".workspace-document-composer [data-main-conversation-composer]")), "document composer");
  await frame();
}

async function selectFile(file, quote) {
  if (file.endsWith(".md")) {
    await evaluate(quote => {
      const root = document.querySelector(".workspace-file-resource.active .file-selection-content");
      if (!root) throw new Error("Selection content missing");
      const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
      let node;
      while ((node = walker.nextNode())) {
        const start = node.textContent.indexOf(quote);
        if (start < 0) continue;
        node.parentElement.scrollIntoView({ block: "center" });
        const range = document.createRange();
        range.setStart(node, start);
        range.setEnd(node, start + quote.length);
        window.getSelection().removeAllRanges();
        window.getSelection().addRange(range);
        document.dispatchEvent(new Event("selectionchange"));
        return;
      }
      throw new Error("Markdown selection text missing");
    }, quote);
  } else {
    await waitFor(() => Boolean(document.querySelector(".workspace-file-resource.active .monaco-editor .view-line")), "Monaco content");
    await click(".workspace-file-resource.active .monaco-editor .view-line");
    await press("A", [process.platform === "darwin" ? "meta" : "control"]);
  }
  await waitFor(() => Boolean(document.querySelector(".file-selection-popup [role=toolbar]")), "selection toolbar");
  await visibleGeometry(".file-selection-popup");
}

async function clickButton(scope, text, selector = "button") {
  const target = await waitFor(({ scope, text, selector }) => {
    const root = document.querySelector(scope) || document;
    const element = Array.from(root.querySelectorAll(selector)).find(button => button.textContent.trim() === text || button.getAttribute("aria-label") === text);
    if (!element || element.disabled) return null;
    const rect = element.getBoundingClientRect();
    if (!rect.width || !rect.height) return null;
    const x = rect.x + rect.width / 2, y = rect.y + rect.height / 2;
    const hit = document.elementFromPoint(x, y);
    if (!element.contains(hit)) throw new Error(`Button ${text} is obscured by ${hit?.outerHTML.slice(0,400)}`);
    return { x, y };
  }, `button ${text}`, { scope, text, selector });
  await mouseClick(target);
}

async function click(selector) {
  const rect = await visibleGeometry(selector);
  await mouseClick({ x: rect.x + rect.width / 2, y: rect.y + rect.height / 2 });
}

async function mouseClick(point) {
  const position = { x: Math.round(point.x), y: Math.round(point.y) };
  win.webContents.sendInputEvent({ type: "mouseMove", ...position });
  win.webContents.sendInputEvent({ type: "mouseDown", button: "left", clickCount: 1, ...position });
  win.webContents.sendInputEvent({ type: "mouseUp", button: "left", clickCount: 1, ...position });
  await frame();
}

async function press(keyCode, modifiers = []) {
  win.webContents.sendInputEvent({ type: "keyDown", keyCode, modifiers });
  win.webContents.sendInputEvent({ type: "keyUp", keyCode, modifiers });
  await frame();
}

async function fill(selector, value) {
  await click(selector);
  await press("A", [process.platform === "darwin" ? "meta" : "control"]);
  await win.webContents.insertText(value);
  await waitFor(({ selector, value }) => document.querySelector(selector)?.value === value, "typed input", { selector, value });
}

async function visibleGeometry(selector) {
  const rect = await waitFor(selector => {
    const element = document.querySelector(selector);
    if (!element) return null;
    const r = element.getBoundingClientRect();
    return r.width && r.height && r.x >= -1 && r.y >= -1 && r.right <= innerWidth + 1 && r.bottom <= innerHeight + 1
      ? { x: r.x, y: r.y, width: r.width, height: r.height, right: r.right, bottom: r.bottom, viewportWidth: innerWidth, viewportHeight: innerHeight } : null;
  }, `visible ${selector}`, selector);
  assert.ok(rect.x >= -1 && rect.y >= -1 && rect.right <= rect.viewportWidth + 1 && rect.bottom <= rect.viewportHeight + 1,
    `${selector} must fit the viewport: ${JSON.stringify(rect)}`);
  return rect;
}

async function screenshot(name) {
  await frame();
  const target = path.join(output, `${name}.png`);
  fs.writeFileSync(target, (await win.webContents.capturePage()).toPNG());
  report.screenshots.push(target);
  return target;
}

function composerValue() {
  return document.querySelector("[data-main-conversation-composer] textarea")?.value;
}
function snapshot() { return evaluate(() => window.selectionE2E.snapshot()); }
function frame() { return evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))); }
function evaluate(fn, argument) {
  return win.webContents.executeJavaScript(`(async () => { const composerValue = ${composerValue.toString()};
    try { return { ok: true, value: await (${fn.toString()})(${JSON.stringify(argument) ?? "undefined"}) }; }
    catch (error) { return { ok: false, error: error.stack || String(error) }; }
  })()`, true).then(result => {
    if (!result.ok) throw new Error(result.error);
    return result.value;
  });
}
async function waitFor(fn, description, argument, timeout = 10000) {
  const start = Date.now();
  while (Date.now() - start < timeout) {
    const result = await evaluate(fn, argument);
    if (result) return result;
    await new Promise(resolve => setTimeout(resolve, 40));
  }
  throw new Error(`Timed out waiting for ${description}`);
}
function writeReport() { fs.writeFileSync(path.join(output, "report.json"), JSON.stringify(report, null, 2)); }
