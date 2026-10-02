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
const report = { boundary: "Built Electron renderer; in-memory preload, no Go/provider or real user data", renderer, output, cases: [], screenshots: [], errors: [], layoutObservations: [] };
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
    win.webContents.debugger.attach("1.3");
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
      document.documentElement.style.setProperty("--ui-font-size", `${size}px`);
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

      phase = `${label}: side-chat selection`;
      await selectFile(file, quote);
      await clickButton(".file-selection-action-menu", "Ask in side chat");
      await waitFor(() => Boolean(document.querySelector(".side-thread-panel .composer-file-selection-card .composer-document-card-main")), "side selection chip");
      await visibleGeometry(".side-thread-panel .composer-file-selection-card .composer-document-card-main");
      await screenshot(`${label}-side-chip`);
      const sideLayout = await evaluate(() => {
        const main = document.querySelector(".conversation-pane > .scroll-region");
        const side = document.querySelector(".side-thread-panel");
        return { main: main.getBoundingClientRect().toJSON(), side: side.getBoundingClientRect().toJSON(),
          mainInert: Boolean(main.closest("[inert]")), viewportWidth: innerWidth };
      });
      report.layoutObservations.push({ label, sideLayout });
      assert.equal(sideLayout.mainInert, false, "The side-chat destination must leave the main conversation interactive");
      assert.ok(sideLayout.main.right <= sideLayout.side.left + 1, "Main and side chat must not overlap");
      // AppLayoutState's existing 352px minimum protects the main reading area.
      assert.ok(sideLayout.viewportWidth < 760 || sideLayout.main.width >= 352 - 1,
        `Side chat must preserve the established main-column minimum: ${JSON.stringify(sideLayout)}`);
      await click(".side-thread-panel .composer-file-selection-card .composer-document-card-main");
      await waitFor(expected => document.querySelector(".composer-file-selection-card-popover .composer-response-selection-quote")?.textContent === expected, "side selection details", quote);
      await visibleGeometry(".composer-file-selection-card-popover");
      await checkAnnotationPanel(`${label}-side-selection-details`, ".side-thread-panel .composer-file-selection-card .composer-document-card-main");
      await screenshot(`${label}-side-selection-details`);
      if (label === "light-default-wide-md") {
        await press("Escape");
        await evaluate(() => {
          const input = document.querySelector(".side-thread-panel .composer textarea");
          input.focus();
          const clipboardData = new DataTransfer();
          clipboardData.setData("text/plain", Array.from({ length: 20 }, (_, index) => `Side paste line ${index + 1}`).join("\n"));
          input.dispatchEvent(new ClipboardEvent("paste", { bubbles: true, cancelable: true, clipboardData }));
        });
        await waitFor(() => Boolean(document.querySelector(".side-thread-panel .composer-collapsed-prompt-card")),
          "side long paste folds beside selection");
        const attachmentLayout = await evaluate(() => {
          const trays = [...document.querySelectorAll(".side-thread-panel .composer-attachment-tray")];
          return { trays: trays.length, cards: trays[0]?.querySelectorAll(".composer-attachment-card").length,
            followedByComposer: trays[0]?.nextElementSibling?.classList.contains("composer-frame") };
        });
        assert.deepEqual(attachmentLayout, { trays: 1, cards: 2, followedByComposer: true },
          "Side selection and long paste must share the baseline attachment tray above the composer");
        await screenshot(`${label}-side-selection-and-paste`);
        await click(".side-thread-panel .composer-collapsed-prompt-card .composer-attachment-card-remove");
        await waitFor(() => !document.querySelector(".side-thread-panel .composer-collapsed-prompt-card"), "remove only side paste");
        await click(".side-thread-panel .composer-file-selection-card .composer-document-card-main");
        await waitFor(expected => document.querySelector(".composer-file-selection-card-popover .composer-response-selection-quote")?.textContent === expected,
          "side selection survives paste removal", quote);
      }
      await click(".composer-file-selection-card-popover .composer-response-selection-remove");
      await waitFor(() => !document.querySelector(".side-thread-panel .composer-file-selection-card .composer-document-card-main"), "side selection removed");
      await click(".side-thread-panel__close");
      await showWorkspacePanel();
      await openFile(file);
      assert.equal(await evaluate(composerValue), draft, "Side selection must preserve the main draft");
      assert.equal((await snapshot()).submissions.length, initial.submissions.length, "Opening a side selection must not submit a main turn");

      phase = `${label}: quote`;
      await selectFile(file, quote);
      await screenshot(`${label}-toolbar`);
      await checkFilePlacement(`${label}-toolbar`);
      await clickButton(".file-selection-action-menu", "Add to conversation");
      await waitFor(() => Boolean(document.querySelector("[data-main-conversation-composer] .composer-file-selection-card .composer-document-card-main")), "selected text tag");
      assert.equal(await evaluate(composerValue), draft, "Selected text must remain folded instead of expanding into the draft");
      assert.equal(await evaluate(() => Boolean(document.querySelector(".file-selection-comments"))), false, "A quote must not create a document comment");
      await frame();
      await visibleGeometry("[data-main-conversation-composer] .composer-file-selection-card .composer-document-card-main");
      await click("[data-main-conversation-composer] .composer-file-selection-card .composer-document-card-main");
      await waitFor(expected => document.querySelector(".composer-response-selection-quote")?.textContent === expected, "opened original text", quote);
      await visibleGeometry(".composer-file-selection-card-popover");
      await screenshot(`${label}-quote-tag`);
      await press("Escape");
      await click("[data-main-conversation-composer] .composer-file-selection-card .composer-document-card-main");
      await click(".composer-response-selection-remove");
      await waitFor(() => !document.querySelector("[data-main-conversation-composer] .composer-file-selection-card .composer-document-card-main"), "quote removed");
      assert.equal(await evaluate(composerValue), draft, "Removing a quote must preserve typed text");
      await selectFile(file, quote);
      await clickButton(".file-selection-action-menu", "Add to conversation");
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
      await clickButton(".file-selection-action-menu", "Comment");
      let comment = `Explain this selection (${label}).`;
      await fill(".file-selection-action-menu textarea", comment);
      await screenshot(`${label}-comment-form`);
      await checkFilePlacement(`${label}-comment-form`);
      await clickButton(".file-selection-action-menu", "Comment");
      await waitFor(() => Boolean(document.querySelector("[data-main-conversation-composer] .composer-file-selection-card .composer-document-card-main")), "comment chip");
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
      phase = `${label}: saved comment edit dismissal`;
      await clickButton(".file-selection-comments", "Edit comment");
      await waitFor(expected => document.querySelector(".file-selection-action-menu textarea")?.value === expected, "saved comment editor", comment);
      await fill(".file-selection-action-menu textarea", `Discard this unsaved comment edit (${label}).`);
      await screenshot(`${label}-saved-comment-editor`);
      await press("Escape");
      await waitFor(() => !document.querySelector(".file-selection-action-menu textarea"), "saved comment editor dismissed");
      assert.equal(await evaluate(() => document.activeElement?.classList.contains("selection-action-comment-toggle")), true, "Escape returns focus to the comment toggle");
      assert.equal(await evaluate(() => document.querySelector(".file-selection-comments .file-selection-comment > p")?.textContent), comment, "Escape must preserve the saved comment");
      await screenshot(`${label}-saved-comment-escape-focus`);
      await press("Escape");
      await click("[data-main-conversation-composer] .composer-file-selection-card .composer-document-card-main");
      await waitFor(expected => document.querySelector(".composer-file-selection-card-popover .composer-response-selection-comment")?.value === expected, "comment chip details", comment);
      assert.equal(await evaluate(() => document.querySelector(".composer-file-selection-card-popover .composer-response-selection-quote")?.textContent), quote);
      await checkAnnotationPanel(`${label}-comment-chip`, "[data-main-conversation-composer] .composer-file-selection-card .composer-document-card-main");
      await screenshot(`${label}-comment-chip`);
      phase = `${label}: live comment chip editor`;
      comment = `Explain this selection (${label}). Keep the quoted source intact while reviewing a longer comment, including repeated words and punctuation. 第二行 😀\nAlso check how this wraps in a narrow window.\n${"中文注释需要完整保留。".repeat(12)}\n${"unbroken_annotation_".repeat(24)}`;
      await fill(".composer-response-selection-comment", comment);
      await checkAnnotationPanel(`${label}-comment-chip-editor`, "[data-main-conversation-composer] .composer-file-selection-card .composer-document-card-main");
      await screenshot(`${label}-comment-chip-editor`);
      await press("Escape");
      await waitFor(() => !document.querySelector(".composer-file-selection-card-popover"), "chip editor dismissed");
      assert.equal(await evaluate(() => document.activeElement?.matches(".composer-file-selection-card .composer-document-card-main")), true, "Escape restores chip focus");
      assert.equal(await evaluate(() => document.querySelector(".file-selection-comments .file-selection-comment > p")?.textContent), comment, "Live comment edits survive Escape");
      await screenshot(`${label}-comment-chip-escape-focus`);
      await click("[data-main-conversation-composer] .composer-file-selection-card .composer-document-card-main");
      await waitFor(expected => document.querySelector(".composer-response-selection-comment")?.value === expected,
        "reopened annotation retains complete long comment", comment);
      await checkAnnotationPanel(`${label}-comment-chip-reopened`, "[data-main-conversation-composer] .composer-file-selection-card .composer-document-card-main");
      await click("[data-main-conversation-composer] textarea");
      await waitFor(() => !document.querySelector(".composer-file-selection-card-popover"), "outside click dismisses annotation editor");
      assert.equal(await evaluate(composerValue), draft, "Dismissing an annotation must retain the independent composer draft");

      phase = `${label}: inline edit preserving draft and comment`;
      await selectFile(file, quote);
      await clickButton(".file-selection-action-menu", "Edit");
      const instruction = `Replace the selected content (${label}).`;
      await fill(".file-selection-popup textarea", instruction);
      await screenshot(`${label}-edit-form`);
      await checkFilePlacement(`${label}-edit-form`);
      await clickButton(".file-selection-popup", "Send edit request");
      await waitFor(count => window.selectionE2E.snapshot().submissions.length === count, "edit submission", initial.submissions.length + 2);
      const edit = (await snapshot()).submissions.at(-1);
      assert.equal(edit.contentParts?.length, 1, "Inline edit must submit only its own attachment");
      verifySource(edit.contentParts[0], file, text, quote, "edit", instruction);
      assert.equal(edit.activeDocument?.path, file, "Inline edit should target the selected document");
      assert.ok(!edit.prompt.includes(draft), "Inline edit must not consume the unrelated draft");
      assert.equal(await evaluate(composerValue), draft);
      assert.ok(await evaluate(() => Boolean(document.querySelector("[data-main-conversation-composer] .composer-file-selection-card .composer-document-card-main"))), "Pending comment must survive inline edit");

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
      await waitFor(() => composerValue() === "" && !document.querySelector("[data-main-conversation-composer] .composer-file-selection-card .composer-document-card-main"), "sent composer cleared");
      report.cases.push({ variant, file, quote, quoted, edit, sent, checks: ["side-selection-chip", "saved-comment-edit-dismissal", "live-comment-edit-and-focus", "toolbar", "quote-payload", "document-comment-hit-tests", "comment-chip", "inline-edit-preserves-draft-and-comment", "completion-refresh", "comment-payload"] });
      writeReport();
      console.log(`PASS ${label}`);
    }
    phase = `${variant.name}: wrapped middle-of-file selection`;
    await openFile("selection-wrapped.ts");
    await click(".workspace-file-resource.active .monaco-editor .view-line");
    await press("Home", [process.platform === "darwin" ? "meta" : "control"]);
    for (let line = 0; line < 18; line++) await press("Down");
    await press("Home");
    await press("Down", ["shift"]);
    await press("Down", ["shift"]);
    await press("End", ["shift"]);
    await waitFor(() => Boolean(document.querySelector(".file-selection-action-menu")), "wrapped selection toolbar");
    const wrapped = await checkFilePlacement(`${variant.name}-wrapped-toolbar`);
    assert.ok(new Set(wrapped.sourceRects.map(rect => Math.round(rect.top))).size >= 2, "Wrapped fixture must select multiple painted rows");
    await screenshot(`${variant.name}-wrapped-toolbar`);
    await clickButton(".file-selection-action-menu", "Comment");
    const wrappedComment = "Review this wrapped code without covering the source. 第二行 😀";
    await fill(".file-selection-action-menu textarea", wrappedComment);
    await checkFilePlacement(`${variant.name}-wrapped-comment`);
    await screenshot(`${variant.name}-wrapped-comment`);
    const editorRect = await visibleGeometry(".workspace-file-resource.active .monaco-editor");
    // Use the same native CDP wheel path as the sidebar scrolling fixture.
    const wheelPoint = { x: Math.round(editorRect.x + editorRect.width - 30), y: Math.round(editorRect.y + editorRect.height / 2) };
    assert.equal(await evaluate(point => Boolean(document.elementFromPoint(point.x, point.y)?.closest(".monaco-editor")), wheelPoint), true, "Native wheel target must hit the editor");
    win.focus();
    await win.webContents.debugger.sendCommand("Input.dispatchMouseEvent", { type: "mouseMoved", ...wheelPoint });
    await win.webContents.debugger.sendCommand("Input.dispatchMouseEvent", { type: "mouseWheel", ...wheelPoint, deltaY: 60, deltaX: 0 });
    await waitFor(top => {
      const rows = [...document.querySelectorAll(".workspace-file-resource.active .monaco-editor .selected-text")].map(node => node.getBoundingClientRect()).filter(rect => rect.width && rect.height);
      return rows.length > 0 && Math.abs(Math.min(...rows.map(rect => rect.top)) - top) > 1;
    }, "native wheel moves the wrapped source", wrapped.source.top);
    await frame();
    const scrolled = await checkFilePlacement(`${variant.name}-wrapped-scroll`);
    assert.ok(Math.abs(scrolled.source.top - wrapped.source.top) > 1, "Wrapped scroll scenario must move the source");
    assert.equal(await evaluate(() => document.querySelector(".file-selection-action-menu textarea")?.value), wrappedComment, "Scrolling must preserve the comment");
    await screenshot(`${variant.name}-wrapped-scroll`);
    report.cases.push({ variant, file: "selection-wrapped.ts", checks: ["mid-file-wrapped-source-placement", "popup-clearance", "scroll-reanchor"] });
    writeReport();
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

async function showWorkspacePanel() {
  await evaluate(() => {
    const panel = document.querySelector(".workspace-right-panel");
    if (panel?.getAttribute("aria-hidden") !== "false") {
      const toggle = Array.from(document.querySelectorAll(".side-panel-toggle-button")).find(button => /right sidebar|右侧栏/i.test(button.getAttribute("aria-label") || ""));
      if (!toggle) throw new Error("Right panel toggle missing");
      toggle.click();
    }
  });
}

async function openFiles() {
  await showWorkspacePanel();
  await click(".workspace-panel-add");
  await clickButton(".workspace-tool-menu", "Files", ".workspace-tool-menu-item");
  await waitFor(() => Boolean(document.querySelector(".workspace-file-tree-frame file-tree-container")?.shadowRoot?.querySelector("[data-item-path]")), "file tree");
}

async function openFile(file) {
  await evaluate(() => {
    const expand = Array.from(document.querySelectorAll("button")).find(button => /expand to full panel|展开为全面板/i.test(button.getAttribute("aria-label") || ""));
    expand?.click();
  });
  await waitFor(file => {
    const root = document.querySelector(".workspace-file-tree-frame file-tree-container")?.shadowRoot;
    return Array.from(root?.querySelectorAll("[data-item-path]") ?? []).some(row => row.getAttribute("data-item-path") === file);
  }, "file tree row ready after expanding", file);
  await evaluate(file => {
    const root = document.querySelector(".workspace-file-tree-frame file-tree-container")?.shadowRoot;
    const row = Array.from(root?.querySelectorAll("[data-item-path]") ?? []).find(row => row.getAttribute("data-item-path") === file);
    if (!row) throw new Error(`File row missing: ${file}`);
    row.click();
  }, file);
  await waitFor(file => window.selectionE2E.snapshot().reads.some(read => read.path === file) && Boolean(document.querySelector(".workspace-file-resource.active .file-selection-surface")), "selection surface", file);
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
  await waitFor(() => Boolean(document.querySelector(".file-selection-action-menu[role=toolbar]")), "selection toolbar");
  await visibleGeometry(".file-selection-action-menu");
}

async function clickButton(scope, text, selector = "button") {
  const target = await waitFor(({ scope, text, selector }) => {
    const root = document.querySelector(scope) || document;
    const element = Array.from(root.querySelectorAll(selector)).find(button => button.textContent.trim() === text || button.getAttribute("aria-label") === text);
    if (!element || element.disabled) return null;
    element.scrollIntoView({ block: "nearest", inline: "nearest", behavior: "instant" });
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

async function checkAnnotationPanel(name, anchorSelector) {
  await frame();
  const geometry = await evaluate(anchorSelector => {
    const anchor = document.querySelector(anchorSelector);
    const owner = anchor?.closest(".composer-frame, .composer-frame-shell, .composer-stack");
    const panel = document.querySelector(".composer-file-selection-card-popover");
    if (!owner || !panel) throw new Error("Missing annotation panel or owning composer");
    const input = panel.querySelector("textarea");
    return { owner: owner.getBoundingClientRect().toJSON(), panel: panel.getBoundingClientRect().toJSON(),
      viewport: { width: innerWidth, height: innerHeight },
      scrollWidth: panel.scrollWidth, clientWidth: panel.clientWidth,
      input: input ? { value: input.value, scrollWidth: input.scrollWidth, clientWidth: input.clientWidth } : null };
  }, anchorSelector);
  report.layoutObservations.push({ name, annotation: geometry });
  assert.ok(geometry.panel.left >= Math.max(0, geometry.owner.left) - 1 &&
    geometry.panel.right <= Math.min(geometry.viewport.width, geometry.owner.right) + 1,
    `${name}: annotation must remain in its owning composer column: ${JSON.stringify(geometry)}`);
  assert.ok(geometry.panel.top >= -1 && geometry.panel.bottom <= geometry.viewport.height + 1,
    `${name}: annotation must stay in the viewport`);
  assert.ok(geometry.scrollWidth <= geometry.clientWidth + 1, `${name}: annotation must not scroll horizontally`);
  if (geometry.input) assert.ok(geometry.input.scrollWidth <= geometry.input.clientWidth + 1,
    `${name}: long annotation text must wrap without horizontal overflow: ${JSON.stringify(geometry.input)}`);
}

async function checkFilePlacement(name) {
  await frame();
  const geometry = await evaluate(() => {
    const host = document.querySelector(".workspace-file-resource.active .file-selection-surface");
    const editor = host.querySelector(".monaco-editor");
    const popup = document.querySelector(".file-selection-popup, .file-selection-action-menu");
    const hostBox = host.getBoundingClientRect();
    const clip = (editor ?? host).getBoundingClientRect();
    let sourceRects;
    if (editor) {
      // Observe native painted selection rows independently of the owned anchor decoration.
      sourceRects = [...editor.querySelectorAll(".selected-text")].map(node => node.getBoundingClientRect());
    } else {
      const ranges = [...(CSS.highlights?.entries() ?? [])].filter(([key]) => key.startsWith("file-selection-"))
        .flatMap(([, highlight]) => [...highlight]).filter(range => host.contains(range.startContainer));
      sourceRects = ranges.flatMap(range => [...range.getClientRects()]);
    }
    sourceRects = sourceRects.map(rect => ({ left: Math.max(clip.left, rect.left), right: Math.min(clip.right, rect.right),
      top: Math.max(clip.top, rect.top), bottom: Math.min(clip.bottom, rect.bottom) })).filter(rect => rect.right > rect.left && rect.bottom > rect.top);
    if (!sourceRects.length) throw new Error("No painted source selection to measure");
    const source = { left: Math.min(...sourceRects.map(rect => rect.left)), right: Math.max(...sourceRects.map(rect => rect.right)),
      top: Math.min(...sourceRects.map(rect => rect.top)), bottom: Math.max(...sourceRects.map(rect => rect.bottom)) };
    const box = popup.getBoundingClientRect();
    const input = popup.querySelector("textarea"), inputBox = input?.getBoundingClientRect();
    return { source, sourceRects, popup: box.toJSON(),
      bounds: { left: Math.max(8, hostBox.left + 8), right: Math.min(innerWidth - 8, hostBox.right - 8), top: Math.max(8, hostBox.top + 8), bottom: Math.min(innerHeight - 8, hostBox.bottom - 8) },
      complete: { left: box.left, right: box.right, top: box.top, bottom: box.bottom },
      input: input ? { width: inputBox.width, clientWidth: input.clientWidth, scrollWidth: input.scrollWidth, value: input.value } : null };
  });
  report.layoutObservations.push({ name, placement: geometry });
  const { source, popup, complete, bounds } = geometry;
  const sourceLeft = Math.max(bounds.left, Math.min(source.left, bounds.right - popup.width));
  assert.ok(Math.abs(popup.left - sourceLeft) <= 4, `${name}: popup must align to the painted source start, clamped to the file surface: ${JSON.stringify(geometry)}`);
  const fullHeight = complete.bottom - complete.top;
  const aboveFits = source.top - bounds.top >= fullHeight + 8;
  const belowFits = bounds.bottom - source.bottom >= fullHeight + 8;
  if (aboveFits) assert.ok(complete.bottom <= source.top + 1, `${name}: prefer above when it fits`);
  else if (belowFits) assert.ok(complete.top >= source.bottom - 1, `${name}: below fallback must clear the source`);
  if (aboveFits || belowFits) assert.ok(!geometry.sourceRects.some(rect => complete.left < rect.right && complete.right > rect.left && complete.top < rect.bottom && complete.bottom > rect.top), `${name}: popup must not obscure selected text`);
  if (geometry.input) assert.ok(geometry.input.scrollWidth <= geometry.input.clientWidth + 1, `${name}: instruction text must wrap without horizontal clipping: ${JSON.stringify(geometry.input)}`);
  return geometry;
}

async function screenshot(name) {
  await evaluate(() => Promise.race([
    Promise.all(document.getAnimations().filter(animation => Number.isFinite(animation.effect?.getComputedTiming().endTime)).map(animation => animation.finished.catch(() => undefined))),
    new Promise(resolve => setTimeout(resolve, 1500)),
  ]));
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
