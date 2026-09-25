// The motion vocabulary on the production stylesheet: the duration ladder,
// the shared keyframes, and production surfaces that use them. The Motion
// switch drives the in-app preference; emulate the OS setting from DevTools
// (Rendering > prefers-reduced-motion) to check the other source.
import { useEffect, useState, type CSSProperties } from "react";
import { createRoot } from "react-dom/client";
import { startFocusModality } from "../../src/renderer/FocusModality";
import { applyMessageFlowFontSize } from "../../src/renderer/MessageFlowFontSizeSection";
import { motionDurationMs, useReducedMotion } from "../../src/renderer/motion";
import { Archive, X } from "../../src/renderer/WuuIcons";
import "../../src/renderer/styles.css";
import "./fixture.css";

const params = new URLSearchParams(location.search);
const root = document.documentElement;
root.dataset.theme = params.get("theme") === "dark" ? "dark" : "light";
root.dataset.appearanceMotion = params.get("motion") === "reduce" ? "reduce" : "system";
startFocusModality();
applyMessageFlowFontSize(Number(params.get("size")) || 14);

const LADDER = ["--motion-fast", "--motion-base", "--motion-slow", "--motion-slower"];

const SAMPLES: Array<{ name: string; detail: string; className: string; shape: "card" | "dot" | "ring" }> = [
  { name: "wuu-fade-in", detail: "--motion-base", className: "motion-sample-fade", shape: "card" },
  { name: "wuu-enter", detail: "--enter-y: 8px", className: "motion-sample-rise", shape: "card" },
  { name: "wuu-enter", detail: "--enter-y: -8px; --enter-scale: 0.982", className: "motion-sample-drop", shape: "card" },
  { name: "wuu-enter", detail: "--enter-x: 100%", className: "motion-sample-slide", shape: "card" },
  { name: "wuu-exit", detail: "--exit-y: -4px; --ease-in; ends hidden", className: "motion-sample-exit", shape: "card" },
  { name: "menu-enter", detail: "--menu-enter-duration", className: "motion-sample-menu", shape: "card" },
  { name: "content-swap-enter", detail: "--content-swap-duration", className: "motion-sample-swap", shape: "card" },
  { name: "environment-panel-enter", detail: "reference panel motion", className: "motion-sample-panel", shape: "card" },
  { name: "wuu-pulse", detail: "loop; stops when reduced", className: "motion-sample-pulse", shape: "dot" },
  { name: "wuu-spin", detail: "--motion-spin; keeps turning", className: "motion-sample-spin", shape: "ring" },
];

function Fixture(): JSX.Element {
  const reduced = useReducedMotion();
  const [preference, setPreference] = useState(root.dataset.appearanceMotion);
  const [run, setRun] = useState(0);
  const [moved, setMoved] = useState(false);
  const [closing, setClosing] = useState(false);
  const [durations, setDurations] = useState<number[]>([]);

  useEffect(() => {
    setDurations(LADDER.map((token) => motionDurationMs(token, Number.NaN)));
  }, [reduced]);

  const replay = (): void => {
    setClosing(false);
    setRun((value) => value + 1);
  };

  return <main className="motion-review">
    <header>
      <h1>Motion</h1>
      <p>Ladder tokens, shared keyframes, and reduced motion from either source. Reduced: {reduced ? "yes" : "no"}.</p>
      <nav>
        <button data-testid="replay" onClick={replay}>Replay entrances</button>
        <button data-testid="move" aria-pressed={moved} onClick={() => setMoved((value) => !value)}>Run transitions</button>
        <button data-testid="close" aria-pressed={closing} onClick={() => setClosing((value) => !value)}>Close surfaces</button>
        <button data-testid="motion" aria-pressed={preference === "reduce"} onClick={() => {
          const next = preference === "reduce" ? "system" : "reduce";
          root.dataset.appearanceMotion = next;
          setPreference(next);
        }}>Motion: {preference === "reduce" ? "reduce" : "follow system"}</button>
        <button onClick={() => { root.dataset.theme = root.dataset.theme === "dark" ? "light" : "dark"; }}>Theme</button>
      </nav>
    </header>

    <h2>Duration ladder</h2>
    <div className="motion-ladder">
      {LADDER.map((token, index) => <LadderRow key={token} token={token} ms={durations[index]} moved={moved} />)}
    </div>

    <h2>Shared keyframes</h2>
    <div className="motion-grid" key={`samples-${run}`}>
      {SAMPLES.map((sample) => <div className="motion-tile" key={sample.className}>
        <div className="motion-stage">
          <div className={`motion-${sample.shape} ${sample.className}`} data-sample={sample.className} />
        </div>
        <strong>{sample.name}</strong>
        <code>{sample.detail}</code>
      </div>)}
    </div>

    <h2>Production surfaces</h2>
    <div className="motion-surfaces" key={`surfaces-${run}`}>
      <div className="motion-surface" data-surface="search">
        <div className={`app-modal-backdrop conversation-search-overlay${closing ? " closing" : ""}`}>
          <div className={`conversation-search-dialog${closing ? " closing" : ""}`}>Search dialog</div>
        </div>
      </div>
      <div className="motion-surface" data-surface="drawer">
        <div className={`app-modal-backdrop conversation-search-overlay sidebar-name-dialog-overlay sidebar-name-dialog-overlay-drawer${closing ? " closing" : ""}`}>
          <div className={`conversation-search-dialog sidebar-name-dialog sidebar-name-dialog-drawer${closing ? " closing" : ""}`}>Side drawer</div>
        </div>
      </div>
      <div className="motion-surface" data-surface="archive-tip">
        <div className={`archive-tip${closing ? " leaving" : ""}`} role="status">
          <Archive className="archive-tip-icon" aria-hidden="true" />
          <span className="archive-tip-message">Conversation archived</span>
          <button type="button" className="archive-tip-dismiss" aria-label="Dismiss"><X className="icon-sm" /></button>
        </div>
      </div>
    </div>
  </main>;
}

function LadderRow({ token, ms, moved }: { token: string; ms: number | undefined; moved: boolean }): JSX.Element {
  return <>
    <code>{token}</code>
    <span>{ms === undefined || Number.isNaN(ms) ? "–" : `${ms}ms`}</span>
    <div className="motion-track" data-on={moved || undefined} style={{ "--motion-track-duration": `var(${token})` } as CSSProperties}>
      <span className="motion-knob" />
    </div>
  </>;
}

createRoot(document.getElementById("root")!).render(<Fixture />);
