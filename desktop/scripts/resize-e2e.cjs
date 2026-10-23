const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { app, BrowserWindow } = require("electron");

const desktopRoot = path.resolve(__dirname, "..");
const repoRoot = path.resolve(desktopRoot, "..");
const rendererHtml = path.join(desktopRoot, "out", "renderer", "index.html");
const preload = path.join(__dirname, "resize-e2e-preload.cjs");
const resizeEvidence = path.join(desktopRoot, "out", "e2e", "resize");
fs.mkdirSync(resizeEvidence, { recursive: true });
const resizeUserData = path.join(desktopRoot, "out", "e2e", "resize-user-data");
const disableGpu = process.env.WUU_E2E_DISABLE_GPU === "true";
const visible = process.env.WUU_E2E_VISIBLE === "true";

process.env.WUU_RESIZE_E2E_CWD = repoRoot;
process.env.WUU_RESIZE_E2E_LOCALE = "zh-CN";
fs.rmSync(resizeUserData, { recursive: true, force: true });
fs.mkdirSync(resizeUserData, { recursive: true });
app.setPath("userData", resizeUserData);
if (disableGpu) {
  app.commandLine.appendSwitch("disable-gpu");
  app.commandLine.appendSwitch("disable-software-rasterizer");
}

app.whenReady().then(run).catch(fail);

async function run() {
  assert.ok(fs.existsSync(rendererHtml), "Renderer build is missing. Run npm run build first.");
  assert.ok(fs.existsSync(preload), "Resize E2E preload is missing.");

  const win = new BrowserWindow({
    width: 1380,
    height: 860,
    show: visible,
    webPreferences: {
      contextIsolation: true,
      nodeIntegration: false,
      preload,
      sandbox: false
    }
  });

  win.webContents.on("render-process-gone", (_event, details) => {
    fail(new Error(`Renderer process exited: ${details.reason}`));
  });
  win.webContents.on("console-message", (_event, level, message, line, sourceId) => {
    if (level >= 2) {
      console.error(`renderer console: ${message} (${sourceId}:${line})`);
    }
  });

  await loadFile(win, rendererHtml);
  await waitFor(win, () => Boolean(document.querySelector(".conversation-pane")), 5000);

  await waitFor(
    win,
    () => {
      const button = document.querySelector(".sidebar-account-trigger");
      if (!(button instanceof HTMLButtonElement) || button.disabled) {
        return null;
      }
      button.click();
      return true;
    },
    3000
  );
  await waitFor(win, () => Boolean(document.querySelector(".sidebar-account-menu")), 3000);
  await evaluate(win, () => {
    const button = document.querySelector('[data-settings-page="providers"]');
    if (!(button instanceof HTMLButtonElement)) throw new Error("Settings menu item not found.");
    button.click();
  });
  await waitFor(win, () => Boolean(document.querySelector(".settings-shell")), 3000);
  const debugSettingVisible = await evaluate(win, () => Boolean(document.querySelector(".settings-switch")));
  assert.equal(debugSettingVisible, false, "Production desktop builds must not expose the debug controls setting.");
  await evaluate(win, () => {
    const button = document.querySelector('[data-wuu-component="conversations-section"]');
    if (!(button instanceof HTMLButtonElement)) {
      throw new Error("Conversations navigation button not found.");
    }
    button.click();
  });
  await waitFor(win, () => !document.querySelector('.app-shell[data-settings-open]') && Boolean(document.querySelector(".conversation-pane")), 3000);

  const primaryScroll = await evaluate(win, () => {
    const node = document.querySelector(".scroll-region");
    return {
      exists: node instanceof HTMLElement,
      tagName: node?.tagName ?? "",
      overlayInitialized: node instanceof HTMLElement && node.hasAttribute("data-overlayscrollbars-initialize"),
      overflowY: node instanceof HTMLElement ? getComputedStyle(node).overflowY : ""
    };
  });
  assert.equal(primaryScroll.exists, true, "Primary conversation scroll region should exist.");
  assert.equal(primaryScroll.tagName, "DIV", "Primary conversation scroll region should use a native element.");
  assert.equal(
    primaryScroll.overlayInitialized,
    false,
    "Primary conversation scroll region should not be driven by OverlayScrollbars during live window resize."
  );
  assert.match(primaryScroll.overflowY, /auto|scroll/, "Primary conversation scroll region should use native scrolling.");

  const settingsReturnScrollAway = await evaluate(win, () => {
    const node = document.querySelector(".scroll-region");
    if (!(node instanceof HTMLElement)) {
      throw new Error("Primary conversation scroll region not found after returning from Settings.");
    }
    node.scrollTop = node.scrollHeight;
    const maxBefore = Math.max(0, node.scrollHeight - node.clientHeight);
    node.dispatchEvent(new WheelEvent("wheel", { bubbles: true, cancelable: true, deltaY: -160 }));
    node.scrollTop = Math.max(0, node.scrollTop - 160);
    node.dispatchEvent(new Event("scroll", { bubbles: true }));
    return {
      maxBefore,
      afterScrollTop: node.scrollTop
    };
  });
  assert.ok(
    settingsReturnScrollAway.afterScrollTop < settingsReturnScrollAway.maxBefore,
    `Conversation must not snap back to bottom after Settings return: ${JSON.stringify(settingsReturnScrollAway)}`
  );
  const settingsReturnJumpVisible = await waitFor(
    win,
    () => Boolean(document.querySelector(".jump-to-latest-pill")) || null,
    1000
  );
  assert.equal(
    settingsReturnJumpVisible,
    true,
    "Jump-to-latest should appear after scrolling away from the remounted conversation."
  );
  await evaluate(win, () => {
    const node = document.querySelector(".scroll-region");
    if (node instanceof HTMLElement) {
      node.scrollTop = node.scrollHeight;
      node.dispatchEvent(new Event("scroll", { bubbles: true }));
    }
  });
  await waitFor(
    win,
    () => {
      const node = document.querySelector(".scroll-region");
      if (!(node instanceof HTMLElement)) {
        return null;
      }
      const max = Math.max(0, node.scrollHeight - node.clientHeight);
      return Math.abs(node.scrollTop - max) <= 2 ? true : null;
    },
    1000
  );

  const scrollbarInitiallyHidden = await waitFor(
    win,
    () => {
      const node = document.querySelector(".scroll-region");
      return node instanceof HTMLElement && !node.classList.contains("scrollbar-visible") ? true : null;
    },
    2000
  );
  assert.equal(scrollbarInitiallyHidden, true, "Conversation scrollbar should hide once the initial scroll settles.");
  await evaluate(win, () => {
    const node = document.querySelector(".scroll-region");
    if (!(node instanceof HTMLElement)) {
      throw new Error("Primary conversation scroll region not found.");
    }
    node.scrollTop = Math.max(0, node.scrollTop - 160);
    node.dispatchEvent(new Event("scroll", { bubbles: true }));
  });
  const scrollbarVisibleDuringScroll = await waitFor(
    win,
    () => document.querySelector(".scroll-region")?.classList.contains("scrollbar-visible") || null,
    1000
  );
  assert.equal(scrollbarVisibleDuringScroll, true, "Conversation scrollbar should appear while scrolling.");
  const scrollbarHiddenAfterIdle = await waitFor(
    win,
    () => {
      const node = document.querySelector(".scroll-region");
      return node instanceof HTMLElement && !node.classList.contains("scrollbar-visible") ? true : null;
    },
    2000
  );
  assert.equal(scrollbarHiddenAfterIdle, true, "Conversation scrollbar should hide after scrolling stops.");

  // The information panel is disabled in the product. Resizing must not
  // retain its obsolete inset or misalign the message flow and composer.
  const defaultLayout = await waitFor(win, () => layoutInsetSnapshot(), 5000);
  const defaultFlow = await waitFor(win, () => flowSnapshot(), 1000);
  assertFlowInset(defaultFlow, "Default-window");
  assert.ok(defaultLayout.scrollPaddingRight <= 1 && Math.abs(defaultLayout.composerPaddingRight - defaultLayout.scrollbarInset) <= 1,
    `Conversation should use the available width. Layout=${JSON.stringify(defaultLayout)}`);

  win.setSize(1720, 860);
  await waitFor(win, () => !document.documentElement.classList.contains("window-resizing"), 1000);
  const wideLayout = await waitFor(win, () => layoutInsetSnapshot(), 1000);
  const wideFlow = await waitFor(win, () => flowSnapshot(), 1000);
  assertFlowInset(wideFlow, "Wide-screen");
  assert.ok(wideFlow.conversationContentWidth <= 990,
    `Extra width should preserve the readable message-flow cap. Flow=${JSON.stringify(wideFlow)}`);
  assert.ok(wideLayout.scrollPaddingRight <= 1 && Math.abs(wideLayout.composerPaddingRight - wideLayout.scrollbarInset) <= 1,
    `A wider window must not reserve space for the disabled information panel. Layout=${JSON.stringify(wideLayout)}`);

  await evaluate(win, () => {
    const button = Array.from(document.querySelectorAll(".sidebar-toggle-button")).find((candidate) =>
      candidate.getAttribute("aria-label")?.includes("收起左侧栏")
    );
    if (!(button instanceof HTMLButtonElement)) {
      throw new Error("Sidebar collapse button not found.");
    }
    button.click();
  });
  await waitFor(
    win,
    () => {
      const shell = document.querySelector(".app-shell");
      const pane = document.querySelector(".conversation-pane");
      const ribbon = document.querySelector(".sidebar-ribbon");
      if (!(shell instanceof HTMLElement) || !(pane instanceof HTMLElement) || !(ribbon instanceof HTMLElement)) {
        return null;
      }
      return shell.classList.contains("sidebar-collapsed") &&
        !shell.classList.contains("sidebar-animating") &&
        Math.abs(pane.getBoundingClientRect().left - ribbon.getBoundingClientRect().right) <= 1
        ? true
        : null;
    },
    1200
  );
  win.setSize(2000, 860);
  await waitFor(win, () => !document.documentElement.classList.contains("window-resizing"), 1000);
  const collapsedWideFlow = await waitFor(win, () => flowSnapshot(), 1000);
  assertFlowInset(collapsedWideFlow, "Collapsed-sidebar");
  assert.ok(
    Math.abs(collapsedWideFlow.conversationContentCenter - collapsedWideFlow.paneCenter) <= 6,
    `With no fixed sidebar, the full-screen message flow should return to the pane center. Flow=${JSON.stringify(collapsedWideFlow)}`
  );
  assert.ok(
    collapsedWideFlow.conversationContentLeft >= collapsedWideFlow.sidebarOpenWidth + 24,
    `The centered message flow should stay clear of the hover sidebar drawer. Flow=${JSON.stringify(collapsedWideFlow)}`
  );
  await evaluate(win, () => {
    const button = Array.from(document.querySelectorAll(".sidebar-toggle-button")).find((candidate) =>
      candidate.getAttribute("aria-label")?.includes("展开左侧栏")
    );
    if (!(button instanceof HTMLButtonElement)) {
      throw new Error("Sidebar expand button not found.");
    }
    button.click();
  });
  await waitFor(
    win,
    () => {
      const shell = document.querySelector(".app-shell");
      return shell instanceof HTMLElement &&
        !shell.classList.contains("sidebar-collapsed") &&
        !shell.classList.contains("sidebar-animating")
        ? true
        : null;
    },
    1200
  );
  win.setSize(1720, 860);
  await waitFor(win, () => !document.documentElement.classList.contains("window-resizing"), 1000);

  win.setSize(1280, 640);
  const duringResize = await waitFor(win, () => {
    const snapshot = layoutInsetSnapshot();
    return snapshot && snapshot.resizing ? snapshot : null;
  }, 120);
  assert.match(duringResize.scrollTransitionDuration, /^0s(, 0s)*$/,
    "Scroll region padding must not animate during viewport resizing.");
  assert.match(duringResize.composerTransitionDuration, /^0s(, 0s)*$/,
    "Composer padding must not animate during viewport resizing.");
  assert.ok(duringResize.scrollPaddingRight <= 1 && Math.abs(duringResize.composerPaddingRight - duringResize.scrollbarInset) <= 1,
    `Live resize must keep the full conversation width. Layout=${JSON.stringify(duringResize)}`);
  await waitFor(win, () => !document.documentElement.classList.contains("window-resizing"), 1000);
  const afterResize = await waitFor(win, () => layoutInsetSnapshot(), 1000);
  assert.ok(afterResize.scrollPaddingRight <= 1 && Math.abs(afterResize.composerPaddingRight - afterResize.scrollbarInset) <= 1,
    `Settled resize must keep the full conversation width. Layout=${JSON.stringify(afterResize)}`);
  const flowResize = await waitFor(win, () => flowSnapshot(), 1000);
  assertFlowInset(flowResize, "Resized-window");
  assert.ok(
    flowResize.conversationContentWidth <= 990,
    `Message flow should keep a capped reading content width during right-side window resize. Flow=${JSON.stringify(flowResize)}`
  );
  assert.ok(
    Math.abs(flowResize.conversationCenter - flowResize.scrollContentCenter) <= 2,
    "Message flow should stay centered in the live scroll region during right-side window resize."
  );

  await delay(300);
  await evaluate(win, () => {
    const probe = { samples: [] };
    window.__flowProbe = probe;
    let previousTs;
    const sample = (ts) => {
      const flow = window.flowSnapshot?.();
      if (flow) {
        probe.samples.push({
          ...flow,
          dt: previousTs === undefined ? 0 : ts - previousTs,
          innerWidth: window.innerWidth
        });
      }
      previousTs = ts;
      if (probe.samples.length < 120) {
        window.requestAnimationFrame(sample);
      }
    };
    window.requestAnimationFrame(sample);
  });
  for (let width = 1280; width <= 1720; width += 22) {
    win.setSize(width, 640);
    await delay(16);
  }
  await delay(240);
  const flowProbe = await evaluate(win, () => window.__flowProbe);
  const maxFrameMs = Math.max(...flowProbe.samples.map((sample) => sample.dt));
  const maxConversationContentWidth = Math.max(...flowProbe.samples.map((sample) => sample.conversationContentWidth));
  const maxFlowCenterDelta = Math.max(
    ...flowProbe.samples.map((sample) => Math.abs(sample.conversationCenter - sample.scrollContentCenter))
  );
  const maxComposerInsetDelta = Math.max(
    ...flowProbe.samples.flatMap((sample) => [
      Math.abs(sample.conversationContentLeft - sample.composerStackLeft),
      Math.abs(sample.composerStackRight - sample.conversationContentRight)
    ])
  );
  fs.writeFileSync(path.join(resizeEvidence, "continuous-resize.json"), JSON.stringify({
    maxFrameMs, maxConversationContentWidth, maxFlowCenterDelta, maxComposerInsetDelta, samples: flowProbe.samples,
  }, null, 2));
  assert.ok(
    maxConversationContentWidth <= 990,
    `Message flow content should stay capped throughout continuous right-side resize. Max=${maxConversationContentWidth}`
  );
  assert.ok(maxFlowCenterDelta <= 2, "Message flow should stay centered throughout continuous right-side resize.");
  assert.ok(
    maxComposerInsetDelta <= 1,
    `Message flow and composer should share their left and right edges throughout resize. Max delta=${maxComposerInsetDelta}`
  );
  assert.ok(maxFrameMs < 80, `Continuous right-side resize should not stall the renderer for ${Math.round(maxFrameMs)}ms.`);

  await waitFor(win, () => !document.documentElement.classList.contains("window-resizing"), 1000);
  win.setSize(1280, 860);
  await delay(180);
  await waitFor(win, () => !document.documentElement.classList.contains("window-resizing"), 1000);

  const resizeObserverName = await evaluate(win, () => ResizeObserver.name);
  assert.notEqual(resizeObserverName, "FileTreeResizeObserverGate", "ResizeObserver must not be gated during live window resize.");

  const appShellIdleTransitions = await evaluate(win, () => {
    const style = getComputedStyle(document.querySelector(".app-shell"));
    return {
      property: style.transitionProperty,
      duration: style.transitionDuration
    };
  });
  assert.doesNotMatch(
    appShellIdleTransitions.property,
    /grid-template-(columns|rows)/,
    "Top-level grid layout must not transition because viewport-driven columns should track live resize immediately."
  );
  assert.doesNotMatch(
    appShellIdleTransitions.property,
    /(?:^|,\s*)(?:all|width|height|inline-size|block-size|padding(?:-[a-z]+)?|margin(?:-[a-z]+)?|left|right|top|bottom|transform)(?:,|$)/,
    "Idle shell transitions must not animate viewport geometry; scrollbar color may still fade."
  );

  await evaluate(win, () => {
    const button = Array.from(document.querySelectorAll(".side-panel-toggle-button")).find((candidate) =>
      candidate.getAttribute("aria-label")?.includes("右侧栏")
    );
    const panel = document.querySelector(".workspace-right-panel");
    if (!(button instanceof HTMLButtonElement)) {
      throw new Error("Right panel toggle button not found.");
    }
    if (panel?.getAttribute("aria-hidden") !== "false") {
      button.click();
    }
  });
  await waitFor(win, () => document.querySelector(".workspace-right-panel")?.getAttribute("aria-hidden") === "false", 3000);
  await evaluate(win, () => {
    const picker = document.querySelector(".workspace-panel-add");
    if (picker instanceof HTMLButtonElement) {
      picker.click();
    }
  });

  const rightPanelToolLabels = await waitFor(
    win,
    () => {
      const labels = Array.from(document.querySelectorAll(".workspace-right-panel .workspace-tool-menu-item strong"))
        .map((node) => node.textContent?.trim())
        .filter(Boolean);
      return labels.length > 0 ? labels : null;
    },
    3000
  );
  assert.deepEqual(
    rightPanelToolLabels,
    ["文件", "审查", "终端", "浏览器"],
    "Opening the right panel should show the workspace tool picker before a tool detail."
  );
  const rightPanelWidthBefore = await evaluate(win, () => {
    const shell = document.querySelector(".app-shell");
    const resizer = document.querySelector(".workspace-right-panel-resizer");
    if (!(shell instanceof HTMLElement) || !(resizer instanceof HTMLElement)) {
      throw new Error("Right panel resizer not found.");
    }
    return Number.parseFloat(getComputedStyle(shell).getPropertyValue("--workspace-right-panel-width"));
  });
  await evaluate(win, () => {
    const resizer = document.querySelector(".workspace-right-panel-resizer");
    if (!(resizer instanceof HTMLElement)) {
      throw new Error("Right panel resizer not found.");
    }
    const rect = resizer.getBoundingClientRect();
    const startX = rect.left + rect.width / 2;
    resizer.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true, button: 0, clientX: startX, pointerId: 1 }));
  });
  await waitFor(win, () => document.querySelector(".app-shell")?.classList.contains("resizing-right-panel"), 1000);
  await evaluate(win, () => {
    const resizer = document.querySelector(".workspace-right-panel-resizer");
    if (!(resizer instanceof HTMLElement)) {
      throw new Error("Right panel resizer not found.");
    }
    const rect = resizer.getBoundingClientRect();
    const startX = rect.left + rect.width / 2;
    window.dispatchEvent(new PointerEvent("pointermove", { bubbles: true, button: 0, clientX: startX - 96, pointerId: 1 }));
    window.dispatchEvent(new PointerEvent("pointerup", { bubbles: true, button: 0, clientX: startX - 96, pointerId: 1 }));
  });
  let rightPanelWidthAfter = rightPanelWidthBefore;
  const rightPanelResizeStarted = Date.now();
  while (Date.now() - rightPanelResizeStarted < 1000) {
    rightPanelWidthAfter = await evaluate(win, () => {
      const shell = document.querySelector(".app-shell");
      if (!(shell instanceof HTMLElement)) {
        return 0;
      }
      return Number.parseFloat(getComputedStyle(shell).getPropertyValue("--workspace-right-panel-width"));
    });
    if (rightPanelWidthAfter >= rightPanelWidthBefore + 72) {
      break;
    }
    await delay(40);
  }
  assert.ok(
    rightPanelWidthAfter >= rightPanelWidthBefore + 72,
    `Dragging the right panel resizer should increase width. Before: ${rightPanelWidthBefore}, after: ${rightPanelWidthAfter}.`
  );
  const titlebarAfterRightPanelResize = await evaluate(win, () => {
    const titlebar = document.querySelector(".titlebar");
    const title = document.querySelector(".title-block h1") ?? document.querySelector(".session-tab.active .session-tab-title");
    const actions = document.querySelector(".title-actions");
    const rightPanel = document.querySelector(".workspace-right-panel");
    if (
      !(titlebar instanceof HTMLElement) ||
      !(title instanceof HTMLElement) ||
      !(actions instanceof HTMLElement) ||
      !(rightPanel instanceof HTMLElement)
    ) {
      throw new Error("Titlebar or right panel elements not found.");
    }
    const titlebarRect = titlebar.getBoundingClientRect();
    const titleRect = title.getBoundingClientRect();
    const actionsRect = actions.getBoundingClientRect();
    const rightPanelRect = rightPanel.getBoundingClientRect();
    return {
      actionsLeft: actionsRect.left,
      actionsRight: actionsRect.right,
      rightPanelLeft: rightPanelRect.left,
      titleRight: titleRect.right,
      titlebarClientWidth: titlebar.clientWidth,
      titlebarRight: titlebarRect.right,
      titlebarScrollWidth: titlebar.scrollWidth
    };
  });
  assert.ok(
    titlebarAfterRightPanelResize.actionsRight <= titlebarAfterRightPanelResize.rightPanelLeft + 1,
    "Right panel resize should squeeze the active session title before covering titlebar action buttons."
  );
  assert.ok(
    titlebarAfterRightPanelResize.titleRight <= titlebarAfterRightPanelResize.actionsLeft - 8,
    "Long active session titles should truncate before the titlebar action group."
  );
  assert.ok(
    titlebarAfterRightPanelResize.titlebarScrollWidth <= titlebarAfterRightPanelResize.titlebarClientWidth + 1,
    "Active session titlebar should not overflow its squeezed main column."
  );

  await evaluate(win, () => {
    const terminalTool = Array.from(document.querySelectorAll(".workspace-right-panel .workspace-tool-menu-item")).find(
      (candidate) => candidate.textContent?.includes("终端")
    );
    if (!(terminalTool instanceof HTMLButtonElement)) {
      throw new Error("Right panel terminal tool button not found.");
    }
    terminalTool.click();
  });
  await waitFor(win, () => Boolean(document.querySelector(".workspace-terminal-host .xterm")), 1000);
  const terminalText = await waitFor(
    win,
    () => {
      const screen = document.querySelector(".workspace-terminal-screen");
      const text = screen?.textContent ?? "";
      return text.includes("~/wuu") && text.includes("resize-e2e") ? text : null;
    },
    1000
  );
  assert.match(terminalText, /~\/wuu/, "Terminal prompt should show the workspace directory.");
  assert.match(terminalText, /resize-e2e/, "Terminal prompt should show the git branch.");
  await evaluate(win, () => {
    const screen = document.querySelector(".workspace-terminal-screen");
    if (!(screen instanceof HTMLElement)) {
      throw new Error("Terminal screen not found.");
    }
    screen.dispatchEvent(new MouseEvent("mousedown", { bubbles: true }));
  });
  for (const char of "echo terminal-ready") {
    win.webContents.sendInputEvent({ type: "char", keyCode: char });
  }
  win.webContents.sendInputEvent({ type: "keyDown", keyCode: "Enter" });
  win.webContents.sendInputEvent({ type: "keyUp", keyCode: "Enter" });
  await waitFor(
    win,
    () => document.querySelector(".workspace-terminal-screen")?.textContent?.includes("mock terminal output: echo terminal-ready"),
    1000
  );

  await evaluate(win, () => {
    const closeTerminal = Array.from(document.querySelectorAll(".workspace-right-panel button")).find(
      (button) => button.getAttribute("aria-label") === "关闭终端"
    );
    if (closeTerminal instanceof HTMLButtonElement) {
      closeTerminal.click();
      return;
    }
    const picker = document.querySelector(".workspace-panel-add");
    if (picker instanceof HTMLButtonElement) {
      picker.click();
      return;
    }
    throw new Error("Right panel tool picker button not found.");
  });
  await waitFor(
    win,
    () => {
      const items = Array.from(document.querySelectorAll(".workspace-right-panel .workspace-tool-menu-item"));
      return items.some((item) => item.textContent?.includes("文件")) ? true : null;
    },
    3000
  );
  await evaluate(win, () => {
    const fileTool = Array.from(document.querySelectorAll(".workspace-right-panel .workspace-tool-menu-item"))
      .filter((candidate) => candidate instanceof HTMLButtonElement)
      .find((candidate) => candidate.textContent?.includes("文件"));
    if (!(fileTool instanceof HTMLButtonElement)) {
      throw new Error("Right panel file tool button not found.");
    }
    fileTool.click();
  });

  const sessionTabCountBeforeFileOpen = await evaluate(
    win,
    () => document.querySelectorAll(".session-tab").length
  );
  await waitFor(
    win,
    () => Boolean(document.querySelector(".workspace-file-tree-frame file-tree-container")?.shadowRoot?.querySelector("[data-item-path]")) || null,
    3000
  );
  const openedArtifactPath = await evaluate(win, () => {
    const row = document.querySelector(".workspace-file-tree-frame file-tree-container")?.shadowRoot?.querySelector("[data-item-path]");
    if (!(row instanceof HTMLElement)) {
      throw new Error("Workspace file row not found.");
    }
    const path = row.getAttribute("data-item-path") ?? "";
    row.click();
    return path;
  });
  await waitFor(
    win,
    () => Boolean(document.querySelector(".workspace-file-resource.active")) || null,
    3000
  );
  const dockedArtifactState = await evaluate(win, () => {
    const activeResource = document.querySelector(".workspace-file-resource.active");
    const activeWorkspaceTab = document.querySelector(".workspace-tool-tab.active .workspace-tool-tab-main");
    const conversation = document.querySelector(".conversation-pane");
    return {
      resourceID: activeResource?.getAttribute("data-workspace-tab-id") ?? "",
      tabTitle: activeWorkspaceTab?.getAttribute("aria-label") ?? "",
      sessionTabs: document.querySelectorAll(".session-tab").length,
      conversationWidth:
        conversation instanceof HTMLElement ? conversation.getBoundingClientRect().width : 0
    };
  });
  assert.equal(
    dockedArtifactState.sessionTabs,
    sessionTabCountBeforeFileOpen,
    "Opening a workspace file must not create a center session tab."
  );
  assert.equal(
    dockedArtifactState.tabTitle,
    openedArtifactPath,
    "The opened file should become the active right-workspace tab."
  );
  assert.ok(
    dockedArtifactState.conversationWidth > 300,
    "Docked artifact review must keep the conversation visible beside it."
  );

  await waitFor(win, () => Boolean(document.querySelector(".workspace-file-resource.active .monaco-editor textarea")) || null, 3000);
  const previewText = await evaluate(win, () => {
    window.__resizeArtifactViewer = document.querySelector(".workspace-file-resource.active .monaco-editor");
    return document.querySelector(".workspace-file-resource.active .view-lines")?.textContent ?? "";
  });
  assert.ok(previewText.includes("resize-fixture-000.ts"), "The active preview should display the selected file contents.");

  await evaluate(win, () => {
    const expand = document.querySelector('[aria-label="展开为全面板"]');
    if (!(expand instanceof HTMLButtonElement)) {
      throw new Error("Full-panel expand button not found.");
    }
    const conversation = document.querySelector(".conversation-pane");
    const panel = document.querySelector(".workspace-right-panel");
    window.__fullPanelBounds = { left: conversation.getBoundingClientRect().left, right: panel.getBoundingClientRect().right };
    expand.click();
  });
  await waitFor(
    win,
    () => document.querySelector(".app-shell")?.classList.contains("right-panel-globalized") || null,
    3000
  );
  await waitFor(
    win,
    () => {
      const panel = document.querySelector(".workspace-right-panel");
      if (!(panel instanceof HTMLElement)) {
        return null;
      }
      const rect = panel.getBoundingClientRect();
      return Math.abs(rect.left - window.__fullPanelBounds.left) <= 1 && Math.abs(rect.right - window.__fullPanelBounds.right) <= 1 ? true : null;
    },
    3000
  );
  const fullPanelArtifactState = await evaluate(win, () => {
    const shell = document.querySelector(".app-shell");
    const panel = document.querySelector(".workspace-right-panel");
    const activeResource = document.querySelector(".workspace-file-resource.active");
    if (!(shell instanceof HTMLElement) || !(panel instanceof HTMLElement)) {
      throw new Error("Full-panel workspace shell not found.");
    }
    const panelRect = panel.getBoundingClientRect();
    return {
      globalized: shell.classList.contains("right-panel-globalized"),
      resourceID: activeResource?.getAttribute("data-workspace-tab-id") ?? "",
      sameViewer: document.querySelector(".workspace-file-resource.active .monaco-editor") === window.__resizeArtifactViewer,
      editorText: document.querySelector(".workspace-file-resource.active .view-lines")?.textContent ?? "",
      panelLeft: panelRect.left,
      panelRight: panelRect.right,
      canvasLeft: window.__fullPanelBounds.left,
      canvasRight: window.__fullPanelBounds.right,
      exitControlVisible: Boolean(document.querySelector('[aria-label="退出全面板"]'))
    };
  });
  assert.equal(fullPanelArtifactState.globalized, true, "The workspace should enter full-panel mode.");
  assert.equal(
    fullPanelArtifactState.resourceID,
    dockedArtifactState.resourceID,
    "Full-panel mode must preserve the active artifact tab."
  );
  assert.equal(fullPanelArtifactState.sameViewer, true, "Full-panel mode must preserve the mounted file preview.");
  assert.equal(fullPanelArtifactState.editorText, previewText, "Full-panel mode must preserve the displayed file contents.");
  assert.ok(
    Math.abs(fullPanelArtifactState.panelLeft - fullPanelArtifactState.canvasLeft) <= 1,
    `Full-panel workspace should cover the conversation up to the sidebar edge: ${JSON.stringify(fullPanelArtifactState)}`
  );
  assert.ok(
    Math.abs(fullPanelArtifactState.panelRight - fullPanelArtifactState.canvasRight) <= 1,
    "Full-panel workspace should preserve the shared canvas right edge."
  );
  assert.equal(fullPanelArtifactState.exitControlVisible, true, "Full-panel mode should expose its exit control.");

  await evaluate(win, () => {
    const exit = document.querySelector('[aria-label="退出全面板"]');
    if (!(exit instanceof HTMLButtonElement)) {
      throw new Error("Full-panel exit button not found.");
    }
    exit.click();
  });
  await waitFor(
    win,
    () => !document.querySelector(".app-shell")?.classList.contains("right-panel-globalized") || null,
    3000
  );
  await waitFor(
    win,
    () => {
      const conversation = document.querySelector(".conversation-pane");
      return conversation instanceof HTMLElement && conversation.getBoundingClientRect().width > 300
        ? true
        : null;
    },
    3000
  );
  const restoredArtifactState = await evaluate(win, () => ({
    resourceID:
      document.querySelector(".workspace-file-resource.active")?.getAttribute("data-workspace-tab-id") ?? "",
    sameViewer: document.querySelector(".workspace-file-resource.active .monaco-editor") === window.__resizeArtifactViewer,
    editorText: document.querySelector(".workspace-file-resource.active .view-lines")?.textContent ?? "",
    sessionTabs: document.querySelectorAll(".session-tab").length,
    conversationWidth:
      document.querySelector(".conversation-pane") instanceof HTMLElement
        ? document.querySelector(".conversation-pane").getBoundingClientRect().width
        : 0
  }));
  assert.equal(
    restoredArtifactState.resourceID,
    dockedArtifactState.resourceID,
    "Exiting full-panel mode must keep the same artifact active."
  );
  assert.equal(
    restoredArtifactState.sessionTabs,
    sessionTabCountBeforeFileOpen,
    "Full-panel round trips must not alter conversation tabs."
  );
  assert.equal(restoredArtifactState.sameViewer, true, "Docked mode must preserve the mounted file preview.");
  assert.equal(restoredArtifactState.editorText, previewText, "Docked mode must preserve the displayed file contents.");
  await evaluate(win, () => {
    const input = document.querySelector(".workspace-file-resource.active .monaco-editor textarea");
    if (!(input instanceof HTMLTextAreaElement)) throw new Error("Restored Monaco input not found.");
    input.focus();
  });
  await waitFor(win, () => document.activeElement === document.querySelector(".workspace-file-resource.active .monaco-editor textarea"), 1000);
  assert.ok(restoredArtifactState.conversationWidth > 300, "Exiting full-panel mode should restore the conversation.");

  await evaluate(win, () => {
    const picker = document.querySelector(".workspace-panel-add");
    if (!(picker instanceof HTMLButtonElement)) throw new Error("Right panel tool picker not found after full-panel round trip.");
    picker.click();
  });
  await waitFor(win, () => Array.from(document.querySelectorAll(".workspace-right-panel .workspace-tool-menu-item")).some(item => item.textContent?.includes("文件")) || null, 3000);
  await evaluate(win, () => {
    const files = Array.from(document.querySelectorAll(".workspace-right-panel .workspace-tool-menu-item")).find(item => item.textContent?.includes("文件"));
    if (!(files instanceof HTMLButtonElement)) throw new Error("Right panel file tool not found after full-panel round trip.");
    files.click();
  });
  await waitFor(
    win,
    () => Boolean(document.querySelector(".workspace-file-tree-frame file-tree-container")?.shadowRoot?.querySelector("[data-item-path]")) || null,
    3000
  );

  fs.writeFileSync(path.join(resizeEvidence, "wide.png"), (await win.webContents.capturePage()).toPNG());
  await evaluate(win, () => { window.__resizeTreeHost = document.querySelector(".workspace-file-tree-frame file-tree-container"); });
  const before = await waitFor(win, () => treeSnapshot(), 5000);
  assert.ok(before.frameHeight > 500, "Initial file tree frame should be tall enough for resize verification.");
  assert.ok(before.renderedRows > 20, "Initial file tree should render workspace rows.");

  await evaluate(win, () => {
    const style = document.createElement("style");
    style.textContent = `
      @keyframes resize-e2e-spin {
        from { transform: rotate(0deg); }
        to { transform: rotate(360deg); }
      }
      .resize-e2e-animation-probe {
        width: 10px;
        height: 10px;
        opacity: 0;
        pointer-events: none;
        animation: resize-e2e-spin 1000ms linear infinite;
        transition: width 1000ms linear;
      }
    `;
    document.head.append(style);
    const probe = document.createElement("div");
    probe.className = "resize-e2e-animation-probe conversation-status-capsule";
    document.body.append(probe);
  });
  await evaluate(win, () => {
    const probe = { samples: [] };
    window.__resizeProbe = probe;
    const startedAt = performance.now();
    const sample = () => {
      const snapshot = window.treeSnapshot?.();
      if (snapshot) {
        probe.samples.push(snapshot);
      }
      if (performance.now() - startedAt < 240) {
        window.requestAnimationFrame(sample);
      }
    };
    window.requestAnimationFrame(sample);
  });
  win.setSize(980, 560);
  await delay(280);
  const resizeProbe = await evaluate(win, () => window.__resizeProbe);
  const liveResizeSamples = resizeProbe.samples.filter((sample) => sample.resizing);
  const during =
    liveResizeSamples.find(
      (sample) =>
        sample.frameHeight < before.frameHeight - 180 &&
        sample.scrollHeight < before.scrollHeight - 180
    ) ??
    liveResizeSamples.find(
      (sample) => sample.frameHeight < before.frameHeight - 180
    ) ?? resizeProbe.samples.find((sample) => sample.frameHeight < before.frameHeight - 180);

  assert.ok(liveResizeSamples.length > 0, "Programmatic BrowserWindow resizing should set the window resize marker.");
  assert.ok(during, "Resize probe should capture a shrunken file tree frame during programmatic resize.");

  assert.equal(during.resizing, true, "Window resize marker should be set by programmatic BrowserWindow resizing.");
  assert.equal(during.shellTransitionProperty, during.sidebarAnimating ? "grid-template-columns" : "none",
    "Live resize may animate only the bounded sidebar auto-collapse transition.");
  assert.equal(during.animationProbePlayState, "paused", "Targeted resize animations should pause during live window resize.");
  assert.equal(during.animationProbeTransitionProperty, "none", "Targeted resize transitions should be disabled during live window resize.");
  assert.ok(during.viewportHeight < before.viewportHeight - 180, "Renderer viewport should shrink with the Electron window.");
  assert.ok(during.frameHeight < before.frameHeight - 180, "File tree frame should shrink with the Electron window.");
  assert.ok(
    during.scrollHeight < before.scrollHeight - 180,
    "File tree scroll frame should shrink with the resized container."
  );
  assert.ok(
    Math.abs(during.frameHeight - during.scrollHeight - (before.frameHeight - before.scrollHeight)) <= 2,
    "File tree scroll frame should preserve its internal chrome while tracking container height."
  );
  assert.equal(during.sameTreeHost, true, "The file tree should stay mounted while the panel resizes.");
  assert.equal(during.totalRows, before.totalRows, "Resizing must preserve the file tree contents.");
  assert.equal(during.firstPath, before.firstPath, "Resizing must preserve the file tree scroll position.");
  assert.ok(during.renderedRows > 0 && during.renderedRows <= before.renderedRows,
    "The virtualized file tree should keep rows visible while adapting to the smaller viewport.");
  await waitFor(win, () => !document.documentElement.classList.contains("window-resizing"), 1000);
  await waitFor(win, () => !document.querySelector(".app-shell")?.classList.contains("sidebar-animating"), 1500);

  fs.writeFileSync(path.join(resizeEvidence, "narrow.png"), (await win.webContents.capturePage()).toPNG());
  fs.writeFileSync(path.join(resizeEvidence, "report.json"), JSON.stringify({ passed: true,
    defaultLayout, wideLayout, duringResize, afterResize, appShellIdleTransitions, maxFrameMs, maxConversationContentWidth,
    maxFlowCenterDelta, maxComposerInsetDelta, before, during, liveResizeSamples,
  }, null, 2));
  console.log("resize e2e passed");
  app.exit(0);
}

function assertFlowInset(flow, label) {
  const leftInset = flow.conversationContentLeft - flow.composerStackLeft;
  const rightInset = flow.composerStackRight - flow.conversationContentRight;
  assert.ok(
    Math.abs(leftInset) <= 1 && Math.abs(rightInset) <= 1,
    `${label} message flow and composer should share their left and right edges. Left=${leftInset} Right=${rightInset} Flow=${JSON.stringify(flow)}`
  );
}

function loadFile(win, file) {
  return new Promise((resolve, reject) => {
    win.webContents.once("did-fail-load", (_event, _code, description) => reject(new Error(description)));
    win.webContents.once("did-finish-load", () => resolve());
    win.loadFile(file);
  });
}

async function waitFor(win, predicate, timeoutMs) {
  const started = Date.now();
  let lastValue;
  while (Date.now() - started < timeoutMs) {
    lastValue = await evaluate(win, predicate);
    if (lastValue) {
      return lastValue;
    }
    await delay(40);
  }
  throw new Error(`Timed out waiting for condition. Last value: ${JSON.stringify(lastValue)}`);
}

async function evaluate(win, fn) {
  const source = `(() => {
    window.treeSnapshot = () => {
      const frame = document.querySelector(".workspace-file-tree-frame");
      const host = frame?.querySelector("file-tree-container");
      const tree = host?.shadowRoot;
      const scroll = tree?.querySelector("[data-file-tree-virtualized-scroll]");
      const list = tree?.querySelector("[data-file-tree-virtualized-list]");
      const firstRow = tree?.querySelector("[data-item-path]");
      const animationProbe = document.querySelector(".resize-e2e-animation-probe");
      const animationProbeStyle = animationProbe ? getComputedStyle(animationProbe) : undefined;
      if (!frame || !scroll || !list) {
        return null;
      }
      return {
        animationProbePlayState: animationProbeStyle?.animationPlayState ?? "",
        animationProbeTransitionProperty: animationProbeStyle?.transitionProperty ?? "",
        frameHeight: frame.getBoundingClientRect().height,
        renderedRows: tree.querySelectorAll("[data-item-path]").length,
        totalRows: Number(firstRow?.getAttribute("aria-setsize")),
        firstPath: firstRow?.getAttribute("data-item-path"),
        sameTreeHost: host === window.__resizeTreeHost,
        resizing: document.documentElement.classList.contains("window-resizing"),
        sidebarAnimating: document.querySelector(".app-shell")?.classList.contains("sidebar-animating") === true,
        shellTransitionProperty: getComputedStyle(document.querySelector(".app-shell")).transitionProperty,
        scrollHeight: scroll.getBoundingClientRect().height,
        viewportHeight: window.innerHeight,
        viewportWidth: window.innerWidth,
        contentHeight: list.getBoundingClientRect().height
      };
    };
    window.layoutInsetSnapshot = () => {
      const pane = document.querySelector(".conversation-pane");
      const scroll = document.querySelector(".scroll-region");
      const composer = document.querySelector(".dock-composer-wrap");
      if (!pane || !scroll || !composer) {
        return null;
      }
      const scrollStyle = getComputedStyle(scroll);
      const composerStyle = getComputedStyle(composer);
      return {
        resizing: document.documentElement.classList.contains("window-resizing"),
        scrollbarInset: (scroll.getBoundingClientRect().width - scroll.clientWidth) / 2,
        scrollPaddingRight: Number.parseFloat(scrollStyle.paddingRight || "0"),
        composerPaddingRight: Number.parseFloat(composerStyle.paddingRight || "0"),
        scrollTransitionProperty: scrollStyle.transitionProperty,
        scrollTransitionDuration: scrollStyle.transitionDuration,
        composerTransitionProperty: composerStyle.transitionProperty,
        composerTransitionDuration: composerStyle.transitionDuration
      };
    };
    window.flowSnapshot = () => {
      const scroll = document.querySelector(".scroll-region");
      const conversation = document.querySelector(".conversation-width");
      const composer = document.querySelector(".dock-composer-wrap");
      const stack = composer?.querySelector(".composer-stack");
      const pane = document.querySelector(".conversation-pane");
      const shell = document.querySelector(".app-shell");
      if (!scroll || !conversation || !composer || !stack || !pane || !shell) {
        return null;
      }
      const scrollRect = scroll.getBoundingClientRect();
      const conversationRect = conversation.getBoundingClientRect();
      const composerRect = composer.getBoundingClientRect();
      const stackRect = stack.getBoundingClientRect();
      const paneRect = pane.getBoundingClientRect();
      const shellStyle = getComputedStyle(shell);
      const conversationStyle = getComputedStyle(conversation);
      const scrollStyle = getComputedStyle(scroll);
      const composerStyle = getComputedStyle(composer);
      const conversationPaddingLeft = Number.parseFloat(conversationStyle.paddingLeft || "0");
      const conversationPaddingRight = Number.parseFloat(conversationStyle.paddingRight || "0");
      const scrollPaddingRight = Number.parseFloat(scrollStyle.paddingRight || "0");
      const composerPaddingRight = Number.parseFloat(composerStyle.paddingRight || "0");
      const conversationContentLeft = conversationRect.left + conversationPaddingLeft;
      const conversationContentRight = conversationRect.right - conversationPaddingRight;
      return {
        composerContentRight: composerRect.left + composer.clientWidth - composerPaddingRight,
        composerStackCenter: stackRect.left + stackRect.width / 2,
        composerStackLeft: stackRect.left,
        composerStackRight: stackRect.right,
        composerStackWidth: stackRect.width,
        conversationContentCenter: conversationContentLeft + (conversationContentRight - conversationContentLeft) / 2,
        conversationContentLeft,
        conversationContentRight,
        conversationContentWidth: conversationContentRight - conversationContentLeft,
        paneCenter: paneRect.left + paneRect.width / 2,
        sidebarOpenWidth: Number.parseFloat(shellStyle.getPropertyValue("--sidebar-open-width") || "0"),
        conversationCenter: conversationRect.left + conversationRect.width / 2,
        conversationRight: conversationRect.right,
        conversationWidth: conversationRect.width,
        scrollContentCenter: scrollRect.left + scroll.clientLeft + (scroll.clientWidth - scrollPaddingRight) / 2,
        scrollContentRight: scrollRect.left + scroll.clientLeft + scroll.clientWidth - scrollPaddingRight
      };
    };
    return (${fn.toString()})();
  })()`;
  return win.webContents.executeJavaScript(source, true);
}

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function fail(error) {
  console.error(error);
  fs.writeFileSync(path.join(resizeEvidence, "failure.txt"), String(error.stack || error));
  const window = BrowserWindow.getAllWindows()[0];
  try {
    if (window && !window.isDestroyed()) fs.writeFileSync(path.join(resizeEvidence, "failure.png"), (await window.webContents.capturePage()).toPNG());
  } finally { app.exit(1); }
}
