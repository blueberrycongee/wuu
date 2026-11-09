import mermaid from "mermaid";

// Only this diagram surface is web content; conversation text stays native.
type Input = { code: string; live: boolean; dark: boolean; fontSize: number };
declare global {
  interface Window {
    renderWuuMermaid: (input: Input) => void;
    webkit?: { messageHandlers: { diagramHeight: { postMessage: (height: number) => void } } };
    WuuDiagram?: { height: (height: number) => void };
  }
}
const surface = document.getElementById("diagram")!;
const staging = document.getElementById("staging")!;
let desired: Input | undefined;
let revision = 0;
let renderedKey = "";
let attemptedKey = "";
let busy = false;
let timer: ReturnType<typeof setTimeout> | undefined;
let lastStarted = -Infinity;
let sequence = 0;
const interval = 500;

function reportHeight(): void {
  const height = Math.ceil(surface.getBoundingClientRect().height);
  window.webkit?.messageHandlers.diagramHeight.postMessage(height);
  window.WuuDiagram?.height(height);
}
new ResizeObserver(reportHeight).observe(surface);

function schedule(): void {
  if (busy || timer !== undefined || !desired) return;
  // A fixed cadence keeps rendering during continuous deltas, without queueing
  // every token or requiring the model to pause before a diagram can appear.
  timer = setTimeout(() => { timer = undefined; void render(); },
    desired.live ? Math.max(0, interval - (performance.now() - lastStarted)) : 0);
}

async function render(): Promise<void> {
  const input = desired!;
  const stamp = revision;
  const keyFor = (code: string) => JSON.stringify([code.replace(/\n$/, ""), input.dark, input.fontSize]);
  const key = keyFor(input.code);
  const attemptKey = JSON.stringify([key, input.live]);
  if (key === renderedKey || attemptKey === attemptedKey) return;
  attemptedKey = attemptKey;
  if (!input.code.trim() && input.live) return;
  busy = true;
  lastStarted = performance.now();
  try {
    mermaid.initialize({
      startOnLoad: false, securityLevel: "strict", suppressErrorRendering: true,
      theme: input.dark ? "dark" : "default",
      fontFamily: "system-ui, -apple-system, sans-serif",
      // Source directives cannot relax the security or resource limits.
      secure: ["secure", "securityLevel", "startOnLoad", "maxTextSize", "maxEdges", "suppressErrorRendering"],
    });
    let code = input.code;
    let result;
    try {
      result = await mermaid.render(`native-mermaid-${++sequence}`, code, staging);
    } catch (error) {
      // Native Markdown parsers normalize the final newline, so it cannot tell
      // us whether the model finished that line. Try the full source first;
      // a failed live parse can still render its completed preceding lines.
      const lineBreak = code.lastIndexOf("\n");
      if (!input.live || lineBreak < 0) throw error;
      code = code.slice(0, lineBreak);
      if (keyFor(code) === renderedKey) return;
      staging.replaceChildren();
      result = await mermaid.render(`native-mermaid-${++sequence}`, code, staging);
    }
    // Appends may arrive faster than rendering. Commit useful prefix frames;
    // replacements and theme changes must never display a stale result.
    if (desired!.dark !== input.dark || desired!.fontSize !== input.fontSize ||
        (revision !== stamp && !desired!.code.startsWith(input.code.replace(/\n$/, "")))) return;
    surface.innerHTML = result.svg;
    const svg = surface.querySelector("svg")!;
    const box = svg.viewBox.baseVal;
    if (box.width > 0 && box.height > 0) {
      svg.style.maxWidth = "none";
      svg.style.width = `${box.width * input.fontSize / 16}px`;
      svg.style.height = `${box.height * input.fontSize / 16}px`;
    }
    surface.dataset.state = "rendered";
    renderedKey = keyFor(code);
  } catch {
    if (revision !== stamp) return;
    // An unfinished statement is normal while streaming. Keep the last good
    // picture; once stopped, expose the actual source instead of a stale graph.
    if (!input.live) {
      const message = document.createElement("p");
      message.textContent = "无法渲染图表，请检查 Mermaid 语法。";
      const code = document.createElement("pre");
      code.textContent = input.code;
      surface.replaceChildren(message, code);
      surface.dataset.state = "error";
      renderedKey = "";
    }
  } finally {
    staging.replaceChildren();
    busy = false;
    reportHeight();
    if (revision !== stamp) schedule();
  }
}

window.renderWuuMermaid = (input) => {
  if (desired && !input.code.startsWith(desired.code.replace(/\n$/, ""))) {
    surface.textContent = "正在绘制图表…";
    surface.dataset.state = "loading";
    renderedKey = "";
    attemptedKey = "";
  }
  desired = input;
  revision++;
  document.documentElement.style.colorScheme = input.dark ? "dark" : "light";
  document.body.style.fontSize = `${input.fontSize}px`;
  // Finalization is immediate, including when only the live flag changed.
  if (!input.live && timer !== undefined) { clearTimeout(timer); timer = undefined; }
  schedule();
};
// Mermaid diagrams are read-only: no source-defined links or navigation.
document.addEventListener("click", event => event.preventDefault(), true);
