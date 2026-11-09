import { useEffect } from "react";
import type { ActivityPreviewAppearance } from "../shared/activityPreview";
import { MESSAGE_FLOW_FONT_SIZE_RANGE } from "../shared/protocol";
import { useI18n } from "./i18n";
import { prefersReducedMotion, subscribeReducedMotion } from "./motion";

// Tells the main process where the conversation column and its obstacles are,
// in viewport pixels. The card snaps to a corner of that column.
const HOST_SELECTOR = "[data-pip-anchor-host]";
const OBSTACLE_SELECTOR = "[data-pip-obstacle]";
const MODAL_SELECTOR = '[aria-modal="true"], dialog[open]';
const LAYOUT_SELECTOR = `${HOST_SELECTOR},${OBSTACLE_SELECTOR},${MODAL_SELECTOR}`;

interface HostRect {
  x: number;
  y: number;
  width: number;
  height: number;
}

interface HostLayoutReport {
  host: HostRect;
  obstacles: HostRect[];
}

interface HostLayoutElements {
  host: Element | null;
  obstacles: Element[];
  modals: Element[];
}

type HostLayoutReporter = (payload: HostLayoutReport | null) => void;

export function BrowserPiPHostReporter(): null {
  const { locale } = useI18n();
  useEffect(() => {
    const report = (window.wuu as { reportActivityPreviewAppearance?: (appearance: ActivityPreviewAppearance) => void } | undefined)
      ?.reportActivityPreviewAppearance;
    if (!report) return;
    const root = document.documentElement;
    let previous = "";
    const update = (): void => {
      const style = getComputedStyle(root);
      const appearance: ActivityPreviewAppearance = {
        reducedMotion: prefersReducedMotion(),
        fontSize: Number.parseFloat(style.getPropertyValue("--conversation-message-font-size")) || MESSAGE_FLOW_FONT_SIZE_RANGE.default,
        fontFamily: style.getPropertyValue("--appearance-ui-font").trim() || style.fontFamily,
        locale,
        surface: style.getPropertyValue("--surface-1").trim(),
        text: style.getPropertyValue("--ink-strong").trim(),
        muted: style.getPropertyValue("--ink-soft").trim(),
      };
      const next = JSON.stringify(appearance);
      if (next === previous) return;
      previous = next;
      report(appearance);
    };
    const observer = new MutationObserver(update);
    observer.observe(root, { attributes: true, attributeFilter: ["style", "data-theme", "data-appearance-motion"] });
    const stopMotionWatch = subscribeReducedMotion(update);
    update();
    return () => { observer.disconnect(); stopMotionWatch(); };
  }, [locale]);
  useEffect(() => {
    const report = (window.wuu as { reportBrowserPiPHostLayout?: HostLayoutReporter } | undefined)
      ?.reportBrowserPiPHostLayout;
    if (!report || !document.body) return undefined;

    let frame = 0;
    let last = "";
    let elements: HostLayoutElements;
    const send = (): void => {
      if (frame) cancelAnimationFrame(frame);
      frame = 0;
      const payload = measureHostLayout(elements);
      // A fixed-width column may not change during a window resize. Still
      // refresh main's window-size baseline so subsequent moves can follow it.
      const key = JSON.stringify([payload, window.innerWidth, window.innerHeight]);
      if (key === last) return;
      last = key;
      report(payload);
    };
    const schedule = (): void => {
      if (frame) return;
      frame = requestAnimationFrame(send);
    };
    const onFocus = (): void => {
      // Focus transfers ownership even when this window's geometry (or its
      // unavailable layout while a dialog is open) has not changed.
      last = "";
      schedule();
    };

    const observed = new Set<Element>();
    let resizeObserver: ResizeObserver | undefined;
    if (typeof ResizeObserver === "function") {
      // ResizeObserver already runs after layout, before paint. Deferring its
      // measurement to rAF puts the native card a frame behind the conversation.
      resizeObserver = new ResizeObserver(send);
    }
    const watchSizes = (): void => {
      // The set changes on relevant DOM mutations, not on each window frame.
      elements = findHostLayoutElements(document);
      if (!resizeObserver) return;
      const next = new Set<Element>();
      if (elements.host) next.add(elements.host);
      for (const element of [...elements.obstacles, ...elements.modals]) next.add(element);
      for (const element of observed) {
        if (!next.has(element)) resizeObserver.unobserve(element);
      }
      for (const element of next) {
        if (!observed.has(element)) resizeObserver.observe(element);
      }
      observed.clear();
      for (const element of next) observed.add(element);
    };
    const onMutation = (records: MutationRecord[]): void => {
      if (!hostLayoutMutationMatters(records)) return;
      watchSizes();
      schedule();
    };
    const mutationObserver = new MutationObserver(onMutation);
    // Ignore streaming text and class updates. Size changes come from
    // ResizeObserver; dialogs and retained native sections can change visibility
    // without mounting a node or changing the conversation's dimensions. The
    // conversation advertises its anchor only after scroll restoration.
    mutationObserver.observe(document.body, {
      childList: true, subtree: true, attributes: true,
      attributeFilter: [
        "aria-modal", "open", "hidden", "inert", "data-pip-anchor-host", "data-pip-obstacle", "data-settings-open", "data-native-section", "data-primary-plugin-view", "data-web-workspace-open",
      ],
    });
    window.addEventListener("resize", schedule);
    window.addEventListener("focus", onFocus);
    watchSizes();
    schedule();
    return () => {
      if (frame) cancelAnimationFrame(frame);
      resizeObserver?.disconnect();
      mutationObserver.disconnect();
      window.removeEventListener("resize", schedule);
      window.removeEventListener("focus", onFocus);
      report(null);
    };
  }, []);
  return null;
}

export function readBrowserPiPHostLayout(root: ParentNode): HostLayoutReport | null {
  return measureHostLayout(findHostLayoutElements(root));
}

function findHostLayoutElements(root: ParentNode): HostLayoutElements {
  return {
    host: root.querySelector(HOST_SELECTOR),
    obstacles: [...root.querySelectorAll(OBSTACLE_SELECTOR)],
    modals: [...root.querySelectorAll(MODAL_SELECTOR)],
  };
}

function measureHostLayout(elements: HostLayoutElements): HostLayoutReport | null {
  // Native child windows sit above renderer overlays regardless of CSS z-index.
  // Suspend their presentation while a dialog owns the foreground; reporting
  // the column again restores existing cards without changing task control.
  for (const modal of elements.modals) {
    if (visibleRect(modal)) return null;
  }
  const host = elements.host ? visibleRect(elements.host) : null;
  if (!host) return null;
  const obstacles: HostRect[] = [];
  elements.obstacles.forEach((element) => {
    const rect = visibleRect(element);
    if (rect) obstacles.push(rect);
  });
  return { host, obstacles };
}

export function hostLayoutMutationMatters(records: MutationRecord[]): boolean {
  return records.some((record) => {
    if (record.type === "attributes") {
      return record.attributeName === "aria-modal" || record.attributeName === "open"
        || record.attributeName === "data-pip-anchor-host" || record.attributeName === "data-pip-obstacle"
        || elementMentionsHost(record.target);
    }
    return [...record.addedNodes, ...record.removedNodes].some(elementMentionsHost);
  });
}

function elementMentionsHost(node: Node): boolean {
  if (!(node instanceof Element)) return false;
  return node.matches(LAYOUT_SELECTOR)
    || node.querySelector(LAYOUT_SELECTOR) != null;
}

function visibleRect(element: Element): HostRect | null {
  if (!(element instanceof HTMLElement) || element.hidden || element.closest("[inert]")) return null;
  const style = getComputedStyle(element);
  if (style.display === "none" || style.visibility === "hidden") return null;
  const rect = element.getBoundingClientRect();
  if (rect.width <= 0 || rect.height <= 0) return null;
  return {
    x: Math.round(rect.left),
    y: Math.round(rect.top),
    width: Math.round(rect.width),
    height: Math.round(rect.height),
  };
}
