import { useEffect } from "react";

// Tells the main process where the conversation column and its obstacles are,
// in viewport pixels. The card snaps to a corner of that column.
const HOST_SELECTOR = "[data-pip-anchor-host]";
const OBSTACLE_SELECTOR = "[data-pip-obstacle]";

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

type HostLayoutReporter = (payload: HostLayoutReport | null) => void;

export function BrowserPiPHostReporter(): null {
  useEffect(() => {
    const report = (window.wuu as { reportBrowserPiPHostLayout?: HostLayoutReporter } | undefined)
      ?.reportBrowserPiPHostLayout;
    if (!report || !document.body) return undefined;

    let frame = 0;
    let last = "";
    const send = (): void => {
      frame = 0;
      const payload = readBrowserPiPHostLayout(document);
      const key = JSON.stringify(payload);
      if (key === last) return;
      last = key;
      report(payload);
    };
    const schedule = (): void => {
      if (frame) return;
      frame = requestAnimationFrame(send);
    };

    const observed = new Set<Element>();
    let resizeObserver: ResizeObserver | undefined;
    if (typeof ResizeObserver === "function") {
      resizeObserver = new ResizeObserver(schedule);
    }
    const watchSizes = (): void => {
      if (!resizeObserver) return;
      const next = new Set<Element>();
      const host = document.querySelector(HOST_SELECTOR);
      if (host) next.add(host);
      document.querySelectorAll(OBSTACLE_SELECTOR).forEach((element) => next.add(element));
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
    // Child-list only. Message text and class updates must not walk the tree;
    // size changes come from ResizeObserver on the column and its obstacles.
    mutationObserver.observe(document.body, { childList: true, subtree: true });
    window.addEventListener("resize", schedule);
    window.addEventListener("focus", schedule);
    watchSizes();
    schedule();
    return () => {
      if (frame) cancelAnimationFrame(frame);
      resizeObserver?.disconnect();
      mutationObserver.disconnect();
      window.removeEventListener("resize", schedule);
      window.removeEventListener("focus", schedule);
      report(null);
    };
  }, []);
  return null;
}

export function readBrowserPiPHostLayout(root: ParentNode): HostLayoutReport | null {
  const hostElement = root.querySelector(HOST_SELECTOR);
  const host = hostElement ? visibleRect(hostElement) : null;
  if (!host) return null;
  const obstacles: HostRect[] = [];
  root.querySelectorAll(OBSTACLE_SELECTOR).forEach((element) => {
    const rect = visibleRect(element);
    if (rect) obstacles.push(rect);
  });
  return { host, obstacles };
}

export function hostLayoutMutationMatters(records: MutationRecord[]): boolean {
  return records.some((record) => {
    if (record.type === "attributes") {
      return elementMentionsHost(record.target);
    }
    return [...record.addedNodes, ...record.removedNodes].some(elementMentionsHost);
  });
}

function elementMentionsHost(node: Node): boolean {
  if (!(node instanceof Element)) return false;
  return node.matches(`${HOST_SELECTOR},${OBSTACLE_SELECTOR}`)
    || node.querySelector(`${HOST_SELECTOR},${OBSTACLE_SELECTOR}`) != null;
}

function visibleRect(element: Element): HostRect | null {
  if (!(element instanceof HTMLElement) || element.hidden) return null;
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
