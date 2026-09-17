// Cadence and range tracking adapted from Zeron (MIT), Copyright (c) 2026 Wing.
// The complete upstream notice is retained in desktop/vendor/zeron/LICENSE.
import { useLayoutEffect, useRef, type RefObject } from "react";

const graphemes = new Intl.Segmenter(undefined, { granularity: "grapheme" });

type Chunk = { start: number; end: number; born: number; duration: number; progress: number };

/** UTF-16 ranges match DOM Range offsets; never bisect a Unicode code point. */
export class StreamVeil {
  private previous: string;
  private base = 0;
  private chunks: Chunk[] = [];
  private ema = 160;
  private lastAppend: number | undefined;

  constructor(baseline = "") { this.previous = baseline; }

  advance(text: string, now: number): Array<{ start: number; end: number; opacity: number }> {
    if (text !== this.previous) {
      let prefix = 0;
      while (prefix < text.length && text[prefix] === this.previous[prefix]) prefix++;
      if (prefix < text.length) prefix = graphemes.segment(text).containing(prefix)?.index ?? prefix;
      this.chunks = this.chunks.filter(chunk => {
        chunk.end = Math.min(chunk.end, this.base + prefix);
        return chunk.start < chunk.end;
      });
      if (prefix < text.length) {
        if (this.lastAppend !== undefined) {
          this.ema = this.ema * 0.7 + Math.min(Math.max(0, now - this.lastAppend), 1000) * 0.3;
        }
        this.lastAppend = now;
        this.chunks.push({ start: this.base + prefix, end: this.base + text.length, born: now,
          duration: Math.min(400, Math.max(120, this.ema * 3)), progress: 0 });
      }
      this.previous = text;
    }
    return this.sample(now);
  }

  /** Release text promoted into immutable blocks, retaining its in-flight fades. */
  discardPrefix(length: number): void {
    this.previous = this.previous.slice(length);
    this.base += length;
  }

  /** Animation frames only advance the bounded set of active chunks. */
  sample(now: number): Array<{ start: number; end: number; opacity: number }> {
    const boost = 1 + 0.3 * Math.max(0, this.chunks.length - 2);
    for (const chunk of this.chunks) {
      // A falling backlog must never make already-painted text darker again.
      chunk.progress = Math.max(chunk.progress, Math.min(1, (now - chunk.born) * boost / chunk.duration));
    }
    this.chunks = this.chunks.filter(chunk => chunk.progress < 1);
    return this.chunks.map(chunk => ({ start: chunk.start, end: chunk.end,
      opacity: 1 - Math.pow(1 - chunk.progress, 1.6) }));
  }
}

type HighlightRegistry = Map<string, Set<Range>>;
type HighlightConstructor = new (...ranges: Range[]) => Set<Range>;
type TextEntry = { node: Text; start: number; end: number; color: string };
type PaintGroup = { name: string; highlight: Set<Range>; rule: CSSStyleRule; color: string };
type PaintedSpan = { end: number; entries: TextEntry[]; groups: Map<string, PaintGroup> };
let nextPainter = 0;

/**
 * The first stableBlocks children are immutable Markdown block wrappers.
 * Scan only the previous live tail, including blocks just promoted from it.
 * Stable ranges keep their paint state until they finish, without retaining
 * or rescanning the complete answer. identity changes reset source ownership.
 */
export function useStreamVeil(
  root: RefObject<HTMLDivElement | null>, text: string, live: boolean,
  identity: string, stableBlocks = 0,
): void {
  const painter = useRef<{ update: () => void } | null>(null);
  const latestStableBlocks = useRef(stableBlocks);
  useLayoutEffect(() => { latestStableBlocks.current = stableBlocks; }, [stableBlocks]);
  useLayoutEffect(() => {
    if (!live) return;
    const element = root.current;
    const registry = (globalThis.CSS as unknown as { highlights?: HighlightRegistry } | undefined)?.highlights;
    const HighlightClass = (globalThis as unknown as { Highlight?: HighlightConstructor }).Highlight;
    if (!element || !registry || !HighlightClass) return;
    const motion = window.matchMedia("(prefers-reduced-motion: reduce)");
    const style = document.createElement("style");
    document.head.appendChild(style);
    const sheet = style.sheet!;
    const name = `wuu-stream-${nextPainter++}`;
    const painted = new Map<string, PaintedSpan>();
    let markedParents = new Set<HTMLElement>();
    let groupID = 0;
    let frame: number | undefined;
    let knownStableBlocks = latestStableBlocks.current;
    let offset = 0;
    let veil = new StreamVeil();
    let needsBaseline = true;
    const disabled = (): boolean => motion.matches ||
      document.documentElement.dataset.appearanceMotion === "reduce" || document.hidden;
    const read = (): { nodes: TextEntry[]; flat: string; committed: number } => {
      const nodes: TextEntry[] = [];
      let flat = "";
      let committed = 0;
      const colors = new Map<Element, string>();
      let index = knownStableBlocks;
      for (let child = element.children.item(index); child; child = child.nextElementSibling, index++) {
        const walker = document.createTreeWalker(child, NodeFilter.SHOW_TEXT);
        while (walker.nextNode()) {
          const node = walker.currentNode as Text;
          const parent = node.parentElement!;
          if (!parent.closest("p, li, td, th, code, blockquote") ||
              parent.closest('button, svg, [aria-hidden="true"], .rich-mermaid')) continue;
          const color = colors.get(parent) ?? getComputedStyle(parent).color;
          colors.set(parent, color);
          nodes.push({ node, start: offset + flat.length, end: offset + flat.length + node.length, color });
          flat += node.data;
        }
        if (index < latestStableBlocks.current) committed = flat.length;
      }
      return { nodes, flat, committed };
    };
    const removeGroup = (group: PaintGroup): void => {
      registry.delete(group.name);
      const index = Array.from(sheet.cssRules).indexOf(group.rule);
      if (index >= 0) sheet.deleteRule(index);
    };
    const clear = (): void => {
      if (frame !== undefined) cancelAnimationFrame(frame);
      frame = undefined;
      for (const span of painted.values()) for (const group of span.groups.values()) removeGroup(group);
      painted.clear();
      for (const parent of markedParents) parent.removeAttribute("data-stream-veil");
      markedParents.clear();
    };
    const paint = (now: number): void => {
      if (disabled()) { clear(); needsBaseline = true; return; }
      const spans = veil.sample(now);
      const active = new Set<string>();
      for (const span of spans) {
        const key = String(span.start);
        active.add(key);
        const state = painted.get(key);
        if (!state) continue;
        for (const group of state.groups.values()) {
          // Mutate the existing declaration, not the stylesheet or its rules.
          group.rule.style.color = `color-mix(in srgb, ${group.color} ${span.opacity * 100}%, transparent)`;
        }
      }
      for (const [key, state] of painted) {
        if (active.has(key)) continue;
        for (const group of state.groups.values()) removeGroup(group);
        painted.delete(key);
      }
      // Attribute-scoped rules keep animation invalidation local to active
      // text elements; a universal ::highlight rule dirties long histories.
      const parents = new Set<HTMLElement>();
      for (const state of painted.values()) {
        for (const entry of state.entries) {
          const parent = entry.node.parentElement;
          if (parent) parents.add(parent);
        }
      }
      for (const parent of markedParents) if (!parents.has(parent)) parent.removeAttribute("data-stream-veil");
      for (const parent of parents) if (!markedParents.has(parent)) parent.setAttribute("data-stream-veil", name);
      markedParents = parents;
      if (spans.length && frame === undefined) {
        frame = requestAnimationFrame(time => { frame = undefined; paint(time); });
      } else if (!spans.length && frame !== undefined) {
        cancelAnimationFrame(frame);
        frame = undefined;
      }
    };
    const update = (): void => {
      if (disabled()) { clear(); needsBaseline = true; return; }
      if (knownStableBlocks > latestStableBlocks.current) needsBaseline = true;
      if (needsBaseline) {
        clear();
        knownStableBlocks = latestStableBlocks.current;
        offset = 0;
        veil = new StreamVeil(read().flat);
        needsBaseline = false;
        return;
      }
      // All DOM/style reads finish before changing highlight rules.
      const { nodes, flat, committed } = read();
      const now = performance.now();
      const spans = veil.advance(flat, now);
      for (const span of spans) {
        const key = String(span.start);
        let state = painted.get(key);
        if (state && state.end === span.end && span.end <= offset) continue;
        if (!state) { state = { end: span.end, entries: [], groups: new Map() }; painted.set(key, state); }
        state.end = span.end;
        // React can replace text nodes or reset Range offsets while updating
        // characterData. Rebind the live part, retaining promoted stable ranges.
        state.entries = state.entries.filter(entry => entry.end <= offset).concat(
          nodes.filter(entry => entry.end > span.start && entry.start < span.end),
        );
        const rangesByColor = new Map<string, Range[]>();
        for (const entry of state.entries) {
          const ranges = rangesByColor.get(entry.color) ?? [];
          const range = document.createRange();
          range.setStart(entry.node, Math.max(span.start, entry.start) - entry.start);
          range.setEnd(entry.node, Math.min(span.end, entry.end) - entry.start);
          ranges.push(range);
          rangesByColor.set(entry.color, ranges);
        }
        for (const [color, ranges] of rangesByColor) {
          let group = state.groups.get(color);
          if (!group) {
            const key = `${name}-${groupID++}`;
            const highlight = new HighlightClass();
            const index = sheet.insertRule(`[data-stream-veil="${name}"]::highlight(${key}) { color: transparent; }`, sheet.cssRules.length);
            group = { name: key, highlight, rule: sheet.cssRules[index] as CSSStyleRule, color };
            state.groups.set(color, group);
            registry.set(key, highlight);
          }
          group.highlight.clear();
          for (const range of ranges) group.highlight.add(range);
        }
        for (const [color, group] of state.groups) {
          if (rangesByColor.has(color)) continue;
          removeGroup(group);
          state.groups.delete(color);
        }
      }
      veil.discardPrefix(committed);
      offset += committed;
      knownStableBlocks = latestStableBlocks.current;
      paint(now);
    };
    painter.current = { update };
    const reset = (): void => { needsBaseline = true; update(); };
    const preferenceObserver = new MutationObserver(reset);
    preferenceObserver.observe(document.documentElement, { attributes: true, attributeFilter: ["data-appearance-motion", "data-theme"] });
    motion.addEventListener("change", reset);
    document.addEventListener("visibilitychange", reset);
    return () => {
      clear();
      painter.current = null;
      style.remove();
      preferenceObserver.disconnect();
      motion.removeEventListener("change", reset);
      document.removeEventListener("visibilitychange", reset);
    };
  }, [root, identity, live]);
  useLayoutEffect(() => { if (live) painter.current?.update(); }, [text, live, stableBlocks, identity]);
}
