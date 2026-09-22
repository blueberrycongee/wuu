import { Settings2 } from "./WuuIcons";
import { useEffect, useMemo, useRef, useState, type PointerEvent as ReactPointerEvent, type WheelEvent as ReactWheelEvent } from "react";
import type { ChannelAgentInsight, ChannelRoom, NamedAgent } from "../shared/protocol";
import { AgentAvatarMark } from "./AgentAvatarMark";
import { useI18n } from "./i18n";

const GRAPH_WIDTH = 960;
const GRAPH_HEIGHT = 560;

export function graphDensityScale(nodeCount: number): number {
  return Math.max(0.68, Math.min(1.35, Math.sqrt(6 / Math.max(nodeCount, 1))));
}

type GraphNode = {
  id: string;
  kind: "agent" | "room";
  label: string;
  avatarKey?: string;
  avatarImage?: string;
  status?: "idle" | "thinking";
  x: number;
  y: number;
  vx: number;
  vy: number;
  radius: number;
};

type GraphLink = {
  id: string;
  kind: "relationship" | "membership";
  source: GraphNode;
  target: GraphNode;
  weight: number;
};

function buildGraph(agents: NamedAgent[], rooms: ChannelRoom[]): { nodes: GraphNode[]; links: GraphLink[] } {
  const nodes: GraphNode[] = [];
  const byID = new Map<string, GraphNode>();
  agents.forEach((agent, index) => {
    const angle = (index / Math.max(agents.length, 1)) * Math.PI * 2;
    const node: GraphNode = {
      id: `agent:${agent.id}`,
      kind: "agent",
      label: agent.name,
      avatarKey: agent.avatar_key,
      avatarImage: agent.avatar_image,
      status: agent.activity_status === "thinking" ? "thinking" : "idle",
      x: GRAPH_WIDTH / 2 + Math.cos(angle) * 130,
      y: GRAPH_HEIGHT / 2 + Math.sin(angle) * 100,
      vx: 0,
      vy: 0,
      radius: 24,
    };
    nodes.push(node);
    byID.set(node.id, node);
  });
  const linkedRooms = rooms.filter((room) => room.members.filter((member) => member.member_type === "agent" && byID.has(`agent:${member.member_id}`)).length >= 2);
  linkedRooms.forEach((room, index) => {
    const angle = ((index + 0.5) / Math.max(linkedRooms.length, 1)) * Math.PI * 2;
    const node: GraphNode = {
      id: `room:${room.id}`,
      kind: "room",
      label: `# ${room.name}`,
      x: GRAPH_WIDTH / 2 + Math.cos(angle) * 230,
      y: GRAPH_HEIGHT / 2 + Math.sin(angle) * 140,
      vx: 0,
      vy: 0,
      radius: 8,
    };
    nodes.push(node);
    byID.set(node.id, node);
  });
  const links: GraphLink[] = [];
  const relationships = new Map<string, GraphLink>();
  for (const room of linkedRooms) {
    const target = byID.get(`room:${room.id}`);
    if (!target) continue;
    const members = room.members
      .filter((member) => member.member_type === "agent")
      .map((member) => byID.get(`agent:${member.member_id}`))
      .filter((node): node is GraphNode => Boolean(node));
    for (const source of members) {
      links.push({ id: `membership:${source.id}:${target.id}`, kind: "membership", source, target, weight: 1 });
    }
    for (let leftIndex = 0; leftIndex < members.length; leftIndex++) {
      for (let rightIndex = leftIndex + 1; rightIndex < members.length; rightIndex++) {
        const pair = [members[leftIndex], members[rightIndex]].sort((left, right) => left.id.localeCompare(right.id));
        const id = `relationship:${pair[0].id}:${pair[1].id}`;
        const existing = relationships.get(id);
        if (existing) existing.weight += 1;
        else relationships.set(id, { id, kind: "relationship", source: pair[0], target: pair[1], weight: 1 });
      }
    }
  }
  return { nodes, links: [...relationships.values(), ...links] };
}

function relativeActivity(value: string | undefined, locale: string, t: ReturnType<typeof useI18n>["t"]): string {
  if (!value) return t("channels.agentPreviewNoActivity");
  const timestamp = new Date(value).getTime();
  if (!Number.isFinite(timestamp)) return t("channels.agentPreviewNoActivity");
  const elapsedMinutes = Math.max(0, Math.round((Date.now() - timestamp) / 60_000));
  if (elapsedMinutes < 1) return t("channels.agentPreviewJustNow");
  const formatter = new Intl.RelativeTimeFormat(locale, { numeric: "auto", style: "short" });
  if (elapsedMinutes < 60) return formatter.format(-elapsedMinutes, "minute");
  const elapsedHours = Math.round(elapsedMinutes / 60);
  if (elapsedHours < 24) return formatter.format(-elapsedHours, "hour");
  return formatter.format(-Math.round(elapsedHours / 24), "day");
}

function AgentPreviewCard({ agent, insight, inheritedProvider, inheritedModel, onPointerEnter, onPointerLeave }: {
  agent: NamedAgent;
  insight?: ChannelAgentInsight;
  inheritedProvider?: string;
  inheritedModel?: string;
  onPointerEnter: () => void;
  onPointerLeave: () => void;
}): JSX.Element {
  const { formatNumber, locale, t } = useI18n();
  const provider = agent.provider_override || inheritedProvider || t("settings.unknownProvider");
  const model = agent.model_override || inheritedModel || t("settings.unknownModel");
  const status = agent.activity_status === "thinking" ? "thinking" : "idle";
  const compact = (value: number): string => formatNumber(value, { notation: "compact", maximumFractionDigits: 1 });
  const percent = (value: number): string => formatNumber(value, { style: "percent", maximumFractionDigits: 0 });
  const statParts: JSX.Element[] = [];
  if (insight) {
    if (insight.files_changed > 0) {
      statParts.push(<span key="files"><strong>{formatNumber(insight.files_changed)}</strong> {t("channels.agentPreviewFiles")}</span>);
      statParts.push(<span key="diff"><i>+{compact(insight.additions)}</i><em>−{compact(insight.deletions)}</em></span>);
    }
    const tokens = insight.input_tokens + insight.output_tokens;
    if (tokens > 0) {
      statParts.push(<span key="tokens"><strong>{compact(tokens)}</strong> {t("channels.agentPreviewTokens")}</span>);
    }
  }
  const languages = insight?.languages.slice(0, 3) ?? [];

  return (
    <aside className="channel-agent-preview-card" data-testid="channel-agent-preview-card" aria-live="polite" onPointerEnter={onPointerEnter} onPointerLeave={onPointerLeave}>
      <header className="channel-agent-preview-header">
        <div className="channel-agent-preview-avatar"><AgentAvatarMark seed={agent.id} avatarKey={agent.avatar_key || "abstract-1"} avatarImage={agent.avatar_image} status={status} /></div>
        <div className="channel-agent-preview-identity">
          <strong className="channel-agent-preview-name">{agent.name}</strong>
          <span className="channel-agent-preview-activity">{status === "thinking" ? t("channels.agentStatus.thinking") : relativeActivity(insight?.last_active_at, locale, t)}</span>
        </div>
        <span className={`channel-agent-preview-dot ${status}`} role="img" aria-label={t(`channels.agentStatus.${status}`)} />
      </header>
      <p className="channel-agent-preview-model" title={`${provider} · ${model}`}>
        <strong>{model}</strong>
        <span>{provider}</span>
      </p>
      {insight ? (
        <div className="channel-agent-preview-body">
          <span className="channel-agent-preview-window">{t("channels.agentPreviewWindow", { count: insight.window_days })}</span>
          {statParts.length > 0 ? <p className="channel-agent-preview-stats">{statParts}</p> : <p className="channel-agent-preview-empty">{t("channels.agentPreviewNoChanges")}</p>}
          {languages.length > 0 ? (
            <div className="channel-agent-preview-languages">
              <div className="channel-agent-preview-langbar">{languages.map((language, index) => <i key={language.name} data-rank={index} style={{ width: `${Math.max(3, language.share * 100)}%` }} />)}</div>
              <span>{languages.map((language) => `${language.name} ${percent(language.share)}`).join(" · ")}</span>
            </div>
          ) : null}
        </div>
      ) : <div className="channel-agent-preview-loading" aria-label={t("channels.agentPreviewLoading")}><i /><i /></div>}
    </aside>
  );
}

export function AgentRelationshipGraph({ agents, rooms, insights, inheritedProvider, inheritedModel, onSelectAgent, ariaLabel, zoomInLabel, zoomOutLabel, resetViewLabel }: {
  agents: NamedAgent[];
  rooms: ChannelRoom[];
  insights?: Record<string, ChannelAgentInsight>;
  inheritedProvider?: string;
  inheritedModel?: string;
  onSelectAgent: (agent: NamedAgent) => void;
  ariaLabel: string;
  zoomInLabel: string;
  zoomOutLabel: string;
  resetViewLabel: string;
}): JSX.Element {
  const { t } = useI18n();
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [previewAgentID, setPreviewAgentID] = useState("");
  const [settings, setSettings] = useState({ repulsion: 2800, linkLength: 136, centerForce: 0.015, nodeScale: 1, showLabels: true });
  const [zoomPercent, setZoomPercent] = useState(100);
  const settingsRef = useRef(settings);
  const userNavigatedRef = useRef(false);
  const fitRef = useRef<(options?: { animate?: boolean }) => void>(() => undefined);
  const viewportTweenRef = useRef<{ frame: number } | null>(null);
  const agentSignature = agents.map((agent) => `${agent.id}:${agent.name}:${agent.avatar_key}:${agent.activity_status}`).join("|");
  const roomSignature = rooms.map((room) => `${room.id}:${room.name}:${room.members.map((member) => `${member.member_type}:${member.member_id}`).join(",")}`).join("|");
  const graph = useMemo(() => buildGraph(agents, rooms), [agentSignature, roomSignature]);
  const densityScale = graphDensityScale(graph.nodes.length);
  const nodeRefs = useRef(new Map<string, SVGGElement>());
  const linkRefs = useRef(new Map<string, SVGLineElement>());
  const frameRef = useRef<number | null>(null);
  const canvasRef = useRef<SVGSVGElement | null>(null);
  const viewportElementRef = useRef<SVGGElement | null>(null);
  const viewportRef = useRef({ x: 0, y: 0, scale: 1 });
  const restartRef = useRef<() => void>(() => undefined);
  const paintRef = useRef<() => void>(() => undefined);
  const dragRef = useRef<{ node: GraphNode; pointerID: number; moved: boolean; downClientX: number; downClientY: number; offsetX: number; offsetY: number } | null>(null);
  const panRef = useRef<{ pointerID: number; x: number; y: number; originX: number; originY: number } | null>(null);
  const previewShowTimerRef = useRef<number | null>(null);
  const previewHideTimerRef = useRef<number | null>(null);
  const previewAgent = agents.find((agent) => agent.id === previewAgentID);
  const activeNodeIDs = useMemo(() => {
    if (!previewAgentID) return new Set<string>();
    const selectedID = `agent:${previewAgentID}`;
    const ids = new Set<string>([selectedID]);
    for (const link of graph.links) {
      if (link.source.id === selectedID) ids.add(link.target.id);
      if (link.target.id === selectedID) ids.add(link.source.id);
    }
    return ids;
  }, [graph, previewAgentID]);

  function clearPreviewTimer(ref: { current: number | null }): void {
    if (ref.current !== null) window.clearTimeout(ref.current);
    ref.current = null;
  }

  function showPreview(agentID: string, immediate = false): void {
    clearPreviewTimer(previewHideTimerRef);
    clearPreviewTimer(previewShowTimerRef);
    if (immediate) setPreviewAgentID(agentID);
    else previewShowTimerRef.current = window.setTimeout(() => setPreviewAgentID(agentID), 140);
  }

  function schedulePreviewHide(): void {
    clearPreviewTimer(previewShowTimerRef);
    clearPreviewTimer(previewHideTimerRef);
    previewHideTimerRef.current = window.setTimeout(() => setPreviewAgentID(""), 180);
  }

  useEffect(() => () => {
    clearPreviewTimer(previewShowTimerRef);
    clearPreviewTimer(previewHideTimerRef);
  }, []);

  useEffect(() => {
    // Obsidian-style force simulation. Dragging re-heats the graph so
    // neighbours react with elastic lag, but the animation must stop after it
    // cools. Keeping this O(nodes²) loop alive forever burns a renderer core
    // and continuously allocates SVG attribute strings even while the graph is
    // idle.
    const ALPHA_FLOOR = 0.012;
    const DRAG_ALPHA_TARGET = 0.3;
    const VELOCITY_DECAY = 0.86;
    const MAX_SPEED = 14;
    const BOUNDS_MARGIN = 40;
    const WARM_UP_TICKS = 180;
    let alpha = 0.08;
    let stopped = false;
    userNavigatedRef.current = false;
    const paint = (): void => {
      for (const link of graph.links) {
        const element = linkRefs.current.get(link.id);
        if (!element) continue;
        element.setAttribute("x1", String(link.source.x));
        element.setAttribute("y1", String(link.source.y));
        element.setAttribute("x2", String(link.target.x));
        element.setAttribute("y2", String(link.target.y));
      }
      for (const node of graph.nodes) {
        nodeRefs.current.get(node.id)?.setAttribute("transform", `translate(${node.x} ${node.y})`);
      }
    };
    paintRef.current = paint;
    const step = (stepAlpha: number): void => {
      for (let leftIndex = 0; leftIndex < graph.nodes.length; leftIndex++) {
        const left = graph.nodes[leftIndex];
        for (let rightIndex = leftIndex + 1; rightIndex < graph.nodes.length; rightIndex++) {
          const right = graph.nodes[rightIndex];
          let dx = right.x - left.x;
          let dy = right.y - left.y;
          const distanceSquared = Math.max(dx * dx + dy * dy, 16);
          const distance = Math.sqrt(distanceSquared);
          dx /= distance;
          dy /= distance;
          const repulsionStrength = (left.kind === "agent" && right.kind === "agent" ? settingsRef.current.repulsion : settingsRef.current.repulsion * 0.32) * densityScale;
          const repulsion = (repulsionStrength * stepAlpha) / distanceSquared;
          left.vx -= dx * repulsion;
          left.vy -= dy * repulsion;
          right.vx += dx * repulsion;
          right.vy += dy * repulsion;
          // Positional overlap correction: energy-free, so nodes resting in
          // contact separate steadily instead of vibrating.
          const overlap = (left.radius + right.radius) * settingsRef.current.nodeScale * densityScale + 16 - distance;
          if (overlap > 0) {
            const push = overlap * 0.5 * Math.max(stepAlpha, 0.08);
            if (dragRef.current?.node !== left) {
              left.x -= dx * push;
              left.y -= dy * push;
            }
            if (dragRef.current?.node !== right) {
              right.x += dx * push;
              right.y += dy * push;
            }
          }
        }
      }
      for (const link of graph.links) {
        const dx = link.target.x - link.source.x;
        const dy = link.target.y - link.source.y;
        const distance = Math.max(Math.hypot(dx, dy), 1);
        const densityLengthScale = Math.max(0.76, Math.min(1.15, densityScale));
        const desiredLength = link.kind === "relationship"
          ? Math.max(64, (settingsRef.current.linkLength - link.weight * 12) * densityLengthScale)
          : settingsRef.current.linkLength * 0.78 * densityLengthScale;
        const strength = link.kind === "relationship" ? 0.09 : 0.055;
        const pull = (distance - desiredLength) * strength * stepAlpha;
        link.source.vx += (dx / distance) * pull;
        link.source.vy += (dy / distance) * pull;
        link.target.vx -= (dx / distance) * pull;
        link.target.vy -= (dy / distance) * pull;
      }
      for (const node of graph.nodes) {
        if (dragRef.current?.node === node) {
          node.vx = 0;
          node.vy = 0;
          continue;
        }
        node.vx += (GRAPH_WIDTH / 2 - node.x) * settingsRef.current.centerForce * stepAlpha;
        node.vy += (GRAPH_HEIGHT / 2 - node.y) * settingsRef.current.centerForce * stepAlpha;
        // Soft bounds: a gentle push back instead of a hard clamp.
        if (node.x < BOUNDS_MARGIN) node.vx += (BOUNDS_MARGIN - node.x) * 0.04;
        else if (node.x > GRAPH_WIDTH - BOUNDS_MARGIN) node.vx -= (node.x - (GRAPH_WIDTH - BOUNDS_MARGIN)) * 0.04;
        if (node.y < BOUNDS_MARGIN) node.vy += (BOUNDS_MARGIN - node.y) * 0.04;
        else if (node.y > GRAPH_HEIGHT - BOUNDS_MARGIN) node.vy -= (node.y - (GRAPH_HEIGHT - BOUNDS_MARGIN)) * 0.04;
        node.vx *= VELOCITY_DECAY;
        node.vy *= VELOCITY_DECAY;
        const speed = Math.hypot(node.vx, node.vy);
        if (speed > MAX_SPEED) {
          node.vx = (node.vx / speed) * MAX_SPEED;
          node.vy = (node.vy / speed) * MAX_SPEED;
        }
        node.x += node.vx;
        node.y += node.vy;
      }
    };
    // Relax the deterministic circle layout invisibly before the first
    // paint, so the camera frames the settled graph exactly once up front
    // and never needs an automatic refit later.
    let warmAlpha = 1;
    for (let warmTick = 0; warmTick < WARM_UP_TICKS; warmTick++) {
      step(warmAlpha);
      warmAlpha *= 0.96;
    }
    const tick = (): void => {
      frameRef.current = null;
      if (stopped) return;
      const alphaTarget = dragRef.current ? DRAG_ALPHA_TARGET : 0;
      alpha += (alphaTarget - alpha) * 0.04;
      if (alpha < ALPHA_FLOOR) alpha = ALPHA_FLOOR;
      step(alpha);
      paint();
      if (dragRef.current || alpha > ALPHA_FLOOR) {
        frameRef.current = window.requestAnimationFrame(tick);
      }
    };
    restartRef.current = () => {
      alpha = Math.max(alpha, 0.55);
      if (frameRef.current === null) frameRef.current = window.requestAnimationFrame(tick);
    };
    paint();
    fitRef.current();
    restartRef.current();
    return () => {
      stopped = true;
      cancelViewportTween();
      if (frameRef.current !== null) window.cancelAnimationFrame(frameRef.current);
      frameRef.current = null;
    };
  }, [graph]);

  function updateSettings(next: typeof settings): void {
    settingsRef.current = next;
    setSettings(next);
    restartRef.current();
  }

  function viewBoxPoint(clientX: number, clientY: number): { x: number; y: number } {
    const svg = canvasRef.current;
    const ctm = svg?.getScreenCTM?.();
    if (svg && ctm) {
      const point = new DOMPoint(clientX, clientY).matrixTransform(ctm.inverse());
      return { x: point.x, y: point.y };
    }
    // Fallback for environments without SVG geometry (tests): assumes the
    // viewBox fills the element without letterboxing.
    const bounds = svg?.getBoundingClientRect();
    if (!bounds || bounds.width === 0 || bounds.height === 0) return { x: GRAPH_WIDTH / 2, y: GRAPH_HEIGHT / 2 };
    return { x: ((clientX - bounds.left) / bounds.width) * GRAPH_WIDTH, y: ((clientY - bounds.top) / bounds.height) * GRAPH_HEIGHT };
  }

  function worldPointFromEvent(event: ReactPointerEvent<SVGGElement>): { x: number; y: number } {
    const point = viewBoxPoint(event.clientX, event.clientY);
    const viewport = viewportRef.current;
    return { x: (point.x - viewport.x) / viewport.scale, y: (point.y - viewport.y) / viewport.scale };
  }

  function applyViewport(): void {
    const { x, y, scale } = viewportRef.current;
    viewportElementRef.current?.setAttribute("transform", `translate(${x} ${y}) scale(${scale})`);
    const percent = Math.round(scale * 100);
    setZoomPercent((current) => (current === percent ? current : percent));
  }

  // Fit the camera to the content's bounding box so the graph opens at a
  // sensible size instead of swimming in the fixed 960x560 world.
  const FIT_PADDING = 90;
  function cancelViewportTween(): void {
    if (viewportTweenRef.current !== null) {
      window.cancelAnimationFrame(viewportTweenRef.current.frame);
      viewportTweenRef.current = null;
    }
  }

  function animateViewportTo(target: { x: number; y: number; scale: number }, duration = 420): void {
    cancelViewportTween();
    const from = { ...viewportRef.current };
    const start = performance.now();
    const step = (now: number): void => {
      const progress = Math.min((now - start) / duration, 1);
      const eased = 1 - Math.pow(1 - progress, 3);
      viewportRef.current = {
        x: from.x + (target.x - from.x) * eased,
        y: from.y + (target.y - from.y) * eased,
        scale: from.scale + (target.scale - from.scale) * eased,
      };
      applyViewport();
      viewportTweenRef.current = progress < 1 ? { frame: window.requestAnimationFrame(step) } : null;
    };
    viewportTweenRef.current = { frame: window.requestAnimationFrame(step) };
  }

  function fitViewport(options?: { animate?: boolean }): void {
    if (graph.nodes.length === 0) return;
    let minX = Infinity;
    let minY = Infinity;
    let maxX = -Infinity;
    let maxY = -Infinity;
    for (const node of graph.nodes) {
      minX = Math.min(minX, node.x);
      minY = Math.min(minY, node.y);
      maxX = Math.max(maxX, node.x);
      maxY = Math.max(maxY, node.y);
    }
    const width = Math.max(maxX - minX, 1) + FIT_PADDING * 2;
    const height = Math.max(maxY - minY, 1) + FIT_PADDING * 2;
    const scale = Math.max(0.55, Math.min(1.6, Math.min(GRAPH_WIDTH / width, GRAPH_HEIGHT / height)));
    const target = {
      x: GRAPH_WIDTH / 2 - ((minX + maxX) / 2) * scale,
      y: GRAPH_HEIGHT / 2 - ((minY + maxY) / 2) * scale,
      scale,
    };
    if (options?.animate) animateViewportTo(target);
    else {
      cancelViewportTween();
      viewportRef.current = target;
      applyViewport();
    }
  }
  fitRef.current = fitViewport;

  function zoomAt(nextScale: number, centerX: number, centerY: number): void {
    userNavigatedRef.current = true;
    cancelViewportTween();
    const viewport = viewportRef.current;
    const scale = Math.max(0.55, Math.min(2.5, nextScale));
    const worldX = (centerX - viewport.x) / viewport.scale;
    const worldY = (centerY - viewport.y) / viewport.scale;
    viewportRef.current = { x: centerX - worldX * scale, y: centerY - worldY * scale, scale };
    applyViewport();
  }

  function handleWheel(event: ReactWheelEvent<SVGSVGElement>): void {
    event.preventDefault();
    const point = viewBoxPoint(event.clientX, event.clientY);
    zoomAt(viewportRef.current.scale * Math.exp(-event.deltaY * 0.0015), point.x, point.y);
  }

  function handleCanvasPointerDown(event: ReactPointerEvent<SVGSVGElement>): void {
    if (event.target !== event.currentTarget) return;
    event.currentTarget.setPointerCapture(event.pointerId);
    userNavigatedRef.current = true;
    cancelViewportTween();
    const point = viewBoxPoint(event.clientX, event.clientY);
    panRef.current = {
      pointerID: event.pointerId,
      x: point.x,
      y: point.y,
      originX: viewportRef.current.x,
      originY: viewportRef.current.y,
    };
  }

  function handleCanvasPointerMove(event: ReactPointerEvent<SVGSVGElement>): void {
    const pan = panRef.current;
    if (!pan || pan.pointerID !== event.pointerId) return;
    const point = viewBoxPoint(event.clientX, event.clientY);
    viewportRef.current.x = pan.originX + (point.x - pan.x);
    viewportRef.current.y = pan.originY + (point.y - pan.y);
    applyViewport();
  }

  function handleCanvasPointerUp(event: ReactPointerEvent<SVGSVGElement>): void {
    if (panRef.current?.pointerID !== event.pointerId) return;
    event.currentTarget.releasePointerCapture(event.pointerId);
    panRef.current = null;
  }

  function handlePointerDown(event: ReactPointerEvent<SVGGElement>, node: GraphNode): void {
    if (node.kind === "agent") showPreview(node.id.slice("agent:".length), true);
    event.currentTarget.setPointerCapture(event.pointerId);
    const point = worldPointFromEvent(event);
    dragRef.current = {
      node,
      pointerID: event.pointerId,
      moved: false,
      downClientX: event.clientX,
      downClientY: event.clientY,
      offsetX: node.x - point.x,
      offsetY: node.y - point.y,
    };
    restartRef.current();
  }

  function handlePointerMove(event: ReactPointerEvent<SVGGElement>): void {
    const drag = dragRef.current;
    if (!drag || drag.pointerID !== event.pointerId) return;
    if (!drag.moved) {
      if (Math.hypot(event.clientX - drag.downClientX, event.clientY - drag.downClientY) < 4) return;
      drag.moved = true;
    }
    const point = worldPointFromEvent(event);
    drag.node.x = point.x + drag.offsetX;
    drag.node.y = point.y + drag.offsetY;
    drag.node.vx = 0;
    drag.node.vy = 0;
    paintRef.current();
  }

  function handlePointerUp(event: ReactPointerEvent<SVGGElement>): void {
    const drag = dragRef.current;
    if (!drag || drag.pointerID !== event.pointerId) return;
    event.currentTarget.releasePointerCapture(event.pointerId);
    dragRef.current = null;
    if (!drag.moved && drag.node.kind === "agent") {
      const agent = agents.find((candidate) => `agent:${candidate.id}` === drag.node.id);
      if (agent) onSelectAgent(agent);
    }
  }

  function resetGraph(): void {
    userNavigatedRef.current = true;
    fitViewport({ animate: true });
    for (const node of graph.nodes) {
      node.vx = 0;
      node.vy = 0;
    }
    restartRef.current();
  }

  return (
    <div className="channel-agent-graph-shell">
      <div className="channel-agent-graph-surface">
      <button className="icon-button channel-agent-graph-settings-toggle" type="button" aria-label={t("channels.graphSettings")} aria-expanded={settingsOpen} onClick={() => setSettingsOpen((open) => !open)}><Settings2 className="icon" /></button>
      <svg
        ref={canvasRef}
        className="channel-agent-graph-canvas"
        viewBox={`0 0 ${GRAPH_WIDTH} ${GRAPH_HEIGHT}`}
        role="img"
        aria-label={ariaLabel}
        onWheel={handleWheel}
        onPointerDown={handleCanvasPointerDown}
        onPointerMove={handleCanvasPointerMove}
        onPointerUp={handleCanvasPointerUp}
        onPointerCancel={handleCanvasPointerUp}
        onDoubleClick={(event) => { if (event.target === event.currentTarget) resetGraph(); }}
      >
        <g className="channel-agent-graph-viewport" ref={viewportElementRef}>
          <g className="channel-agent-graph-links">
            {graph.links.map((link) => {
              const active = previewAgentID && (link.source.id === `agent:${previewAgentID}` || link.target.id === `agent:${previewAgentID}`);
              return <line className={`${link.kind}${previewAgentID ? (active ? " is-active" : " is-muted") : ""}`} key={link.id} ref={(element) => { if (element) linkRefs.current.set(link.id, element); }} />;
            })}
          </g>
          <g className="channel-agent-graph-nodes">
        {graph.nodes.map((node) => (
          <g
            className={`channel-agent-graph-node ${node.kind}${previewAgentID ? (activeNodeIDs.has(node.id) ? " is-active" : " is-muted") : ""}`}
            key={node.id}
            ref={(element) => { if (element) nodeRefs.current.set(node.id, element); }}
            role={node.kind === "agent" ? "button" : undefined}
            tabIndex={node.kind === "agent" ? 0 : undefined}
            aria-label={node.kind === "agent" ? node.label : undefined}
            onPointerEnter={() => { if (node.kind === "agent") showPreview(node.id.slice("agent:".length)); }}
            onPointerLeave={() => { if (node.kind === "agent") schedulePreviewHide(); }}
            onFocus={() => { if (node.kind === "agent") showPreview(node.id.slice("agent:".length), true); }}
            onBlur={() => { if (node.kind === "agent") schedulePreviewHide(); }}
            onPointerDown={(event) => handlePointerDown(event, node)}
            onPointerMove={handlePointerMove}
            onPointerUp={handlePointerUp}
            onPointerCancel={handlePointerUp}
            onDragStart={(event) => event.preventDefault()}
            onKeyDown={(event) => {
              if (node.kind !== "agent" || (event.key !== "Enter" && event.key !== " ")) return;
              event.preventDefault();
              const agent = agents.find((candidate) => `agent:${candidate.id}` === node.id);
              if (agent) onSelectAgent(agent);
            }}
          >
            <g transform={`scale(${settings.nodeScale * densityScale})`}>
              {node.kind === "agent" ? (
              <>
                <circle className="channel-agent-graph-hit-target" r="26" />
                <foreignObject x="-18" y="-18" width="36" height="36" pointerEvents="none">
                  <div className="channel-agent-graph-avatar"><AgentAvatarMark seed={node.id.slice("agent:".length)} avatarKey={node.avatarKey ?? "abstract-1"} avatarImage={node.avatarImage} status={node.status} /></div>
                </foreignObject>
                <circle className={`channel-agent-graph-status ${node.status}`} cx="15" cy="-15" r="3.5" />
              </>
              ) : <circle r="6" />}
              {settings.showLabels ? <text y={node.kind === "agent" ? 34 : 22} textAnchor="middle">{node.label}</text> : null}
            </g>
          </g>
        ))}
          </g>
        </g>
      </svg>
      {previewAgent ? (
        <AgentPreviewCard
          agent={previewAgent}
          insight={insights?.[previewAgent.id]}
          inheritedProvider={inheritedProvider}
          inheritedModel={inheritedModel}
          onPointerEnter={() => clearPreviewTimer(previewHideTimerRef)}
          onPointerLeave={schedulePreviewHide}
        />
      ) : null}
      {settingsOpen ? (
        <aside className="channel-agent-graph-settings">
          <strong>{t("channels.graphSettings")}</strong>
          <label><span>{t("channels.nodeRepulsion")} <output>{settings.repulsion}</output></span><input type="range" min="800" max="6000" step="100" value={settings.repulsion} onChange={(event) => updateSettings({ ...settings, repulsion: Number(event.target.value) })} /></label>
          <label><span>{t("channels.linkLength")} <output>{settings.linkLength}</output></span><input type="range" min="70" max="240" step="5" value={settings.linkLength} onChange={(event) => updateSettings({ ...settings, linkLength: Number(event.target.value) })} /></label>
          <label><span>{t("channels.centerForce")} <output>{settings.centerForce.toFixed(3)}</output></span><input type="range" min="0" max="0.03" step="0.001" value={settings.centerForce} onChange={(event) => updateSettings({ ...settings, centerForce: Number(event.target.value) })} /></label>
          <label><span>{t("channels.nodeSize")} <output>{settings.nodeScale.toFixed(1)}x</output></span><input type="range" min="0.7" max="1.5" step="0.1" value={settings.nodeScale} onChange={(event) => updateSettings({ ...settings, nodeScale: Number(event.target.value) })} /></label>
          <label className="channel-agent-graph-check"><input type="checkbox" checked={settings.showLabels} onChange={(event) => updateSettings({ ...settings, showLabels: event.target.checked })} /><span>{t("channels.showLabels")}</span></label>
        </aside>
      ) : null}
      <div className="channel-agent-graph-controls">
        <button type="button" aria-label={zoomOutLabel} onClick={() => zoomAt(viewportRef.current.scale / 1.25, GRAPH_WIDTH / 2, GRAPH_HEIGHT / 2)}>−</button>
        <button type="button" aria-label={resetViewLabel} onClick={() => { userNavigatedRef.current = true; fitViewport({ animate: true }); }}>{zoomPercent}%</button>
        <button type="button" aria-label={zoomInLabel} onClick={() => zoomAt(viewportRef.current.scale * 1.25, GRAPH_WIDTH / 2, GRAPH_HEIGHT / 2)}>+</button>
      </div>
      </div>
    </div>
  );
}
