// Keep the observation window independent of the display's easing frames.
const WINDOW_MS = 2_000;
const MIN_SAMPLE_MS = 300;
const SAMPLE_BUCKET_MS = 50;
const NOTIFY_MS = 250;
const RETAINED_TURNS = 24;
const OUTPUT_DELTAS = new Set([
  "item/agentMessage/delta",
  "item/reasoning/delta",
  "item/toolCall/delta",
]);

export function activate(api) {
  const { createElement: h, useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } = api.react;
  const turns = new Map();
  const listeners = new Set();
  let revision = 0;
  let notifyTimer;

  function notify() {
    revision += 1;
    for (const listener of listeners) listener();
  }

  api.onHostEvent((event) => {
    if (event?.kind === "connection-reset" || event?.kind === "server-exit") {
      turns.clear();
      if (notifyTimer !== undefined) clearTimeout(notifyTimer);
      notifyTimer = undefined;
      notify();
      return;
    }
    if (event?.kind !== "notification" || event.replayed || !OUTPUT_DELTAS.has(event.message?.method)) return;
    const { thread_id: threadId, turn_id: turnId, delta } = event.message.params ?? {};
    if (typeof threadId !== "string" || !threadId || typeof turnId !== "string" || !turnId || typeof delta !== "string" || !delta) return;

    const key = JSON.stringify([threadId, turnId]);
    const now = Date.now();
    let entry = turns.get(key);
    if (!entry) {
      entry = { startedAt: now, lastAt: now, samples: [] };
      turns.set(key, entry);
      while (turns.size > RETAINED_TURNS) turns.delete(turns.keys().next().value);
    }
    entry.lastAt = now;
    const samples = entry.samples;
    const at = Math.floor(now / SAMPLE_BUCKET_MS) * SAMPLE_BUCKET_MS;
    const last = samples.at(-1);
    if (last?.at === at) last.tokens += estimateTokens(delta);
    else samples.push({ at, tokens: estimateTokens(delta) });
    while (samples.length && samples[0].at <= now - WINDOW_MS) samples.shift();
    if (notifyTimer === undefined) {
      notifyTimer = setTimeout(() => { notifyTimer = undefined; notify(); }, NOTIFY_MS);
    }
  });

  api.registerCleanup(() => {
    if (notifyTimer !== undefined) clearTimeout(notifyTimer);
    turns.clear();
    listeners.clear();
  });

  api.registerLocale({ id: "token-speed-en", locale: "en-US", entries: {
    "tokenSpeed.waiting": "Output speed is unavailable until the model streams observable output.",
    "tokenSpeed.paused": "No observable model output in the last two seconds. Some reasoning and tool-call arguments may not be streamed.",
    "tokenSpeed.estimated": "Estimated observable output speed: {speed} tokens per second. Based on streamed text, reasoning, and tool-call arguments; some output may be hidden and the model's tokenizer may differ.",
  } });
  api.registerLocale({ id: "token-speed-zh", locale: "zh-CN", entries: {
    "tokenSpeed.waiting": "模型开始输出可观察的流式内容后显示速度。",
    "tokenSpeed.paused": "最近两秒没有可观察的模型输出。部分推理和工具调用参数可能不会流式传出。",
    "tokenSpeed.estimated": "估计的可观察输出速度：每秒 {speed} token。根据流式文字、推理和工具调用参数估算，部分输出可能不可见，实际分词也可能不同。",
  } });

  function useAnimatedSpeed(target) {
    const [displayed, setDisplayed] = useState(target);
    const current = useRef(target);
    useEffect(() => {
      const reducedMotion = matchMedia("(prefers-reduced-motion: reduce)");
      let frame;
      const from = current.current;
      const started = performance.now();
      const finish = () => {
        cancelAnimationFrame(frame);
        current.current = target;
        setDisplayed(target);
      };
      const animate = (now) => {
        const progress = Math.min(1, (now - started) / 320);
        current.current = from + (target - from) * (1 - Math.pow(1 - progress, 3));
        setDisplayed(current.current);
        if (progress < 1) frame = requestAnimationFrame(animate);
      };
      if (reducedMotion.matches || from === target) finish();
      else frame = requestAnimationFrame(animate);
      reducedMotion.addEventListener("change", finish);
      return () => {
        cancelAnimationFrame(frame);
        reducedMotion.removeEventListener("change", finish);
      };
    }, [target]);
    return Math.round(displayed);
  }

  function Speed({ threadId, turnId, running, translate, locale }) {
    const sampleRevision = useSyncExternalStore(
      (listener) => { listeners.add(listener); return () => listeners.delete(listener); },
      () => revision,
      () => revision,
    );
    const [tick, setTick] = useState(0);
    const entry = threadId && turnId && running ? turns.get(JSON.stringify([threadId, turnId])) : undefined;
    const lastAt = entry?.lastAt;
    useEffect(() => {
      if (lastAt === undefined) return undefined;
      const remaining = lastAt + WINDOW_MS - Date.now();
      if (remaining <= 0) return undefined;
      const timer = setTimeout(() => setTick((value) => value + 1), Math.min(NOTIFY_MS, remaining));
      return () => clearTimeout(timer);
    }, [entry, lastAt, tick]);

    // Animation frames must not become new measurement samples.
    const { tokens, speed } = useMemo(() => {
      const now = Date.now();
      const tokens = entry?.samples.reduce((total, sample) => sample.at > now - WINDOW_MS ? total + sample.tokens : total, 0);
      const elapsed = entry ? Math.max(MIN_SAMPLE_MS, Math.min(WINDOW_MS, now - entry.startedAt)) : 0;
      return { tokens, speed: tokens === undefined ? undefined : Math.round(tokens * 1_000 / elapsed) };
    }, [entry, sampleRevision, tick]);
    const displayed = useAnimatedSpeed(speed ?? 0);
    const fullSpeed = speed === undefined ? "" : new Intl.NumberFormat(locale).format(speed);
    const compactParts = speed === undefined ? [] : formatSpeedParts(displayed, locale);
    const label = speed === undefined ? translate("tokenSpeed.waiting")
      : tokens === 0 ? translate("tokenSpeed.paused") : translate("tokenSpeed.estimated", { speed: fullSpeed });
    const railValue = (parts, className) => h("span", { key: className, className }, [
      h("span", { key: "value", className: "plugin-token-speed-value" }, speed === undefined ? "—" : [
        h("span", { key: "digits" }, parts
          .filter((part) => part.type !== "compact")
          .map((part, index) => h("span", {
            key: index, className: part.type === "integer" ? undefined : "plugin-token-speed-symbol",
          }, part.value))),
      ]),
      h("span", { key: "unit", className: "plugin-token-speed-unit" }, `${parts.find((part) => part.type === "compact")?.value ?? "t"}/s`),
    ]);
    const readout = h("span", {
      className: "plugin-token-speed",
      "data-wuu-component": "token-speed",
      "data-state": speed === undefined ? "idle" : tokens === 0 ? "paused" : "streaming",
      "data-placement": "rail",
      role: "img",
      tabIndex: 0,
      "aria-label": label,
      title: label,
    }, [
      railValue(compactParts, "plugin-token-speed-rail-value"),
      railValue(speed === undefined ? [] : formatSpeedParts(displayed, locale, true), "plugin-token-speed-rail-value plugin-token-speed-narrow"),
    ]);
    return readout;
  }

  function RailSpeed({ application, translate, locale }) {
    const ownerTabId = useSyncExternalStore(
      useCallback(listener => application.subscribe(listener), [application]),
      () => application.getSnapshot().tabs.activeTabId,
    );
    const presentation = application.composerPresentation;
    const view = useSyncExternalStore(
      useCallback(listener => presentation.subscribe(listener), [presentation]),
      () => presentation.getSnapshot(ownerTabId),
    );
    return h(Speed, {
      key: JSON.stringify([view.threadId, view.turnId]),
      threadId: view.threadId, turnId: view.turnId, running: view.turnRunning,
      translate, locale,
    });
  }

  api.registerUIContribution({
    id: "output-speed",
    target: "wuu.default-preview/workspace.rail",
    render: RailSpeed,
  });
  api.registerCSSSnippet({ id: "token-speed", css: `
    .plugin-token-speed {
      display: inline-flex;
      align-items: center;
      justify-content: center;
      height: calc(var(--wuu-font-size-ui, 15px) * 2.5);
      box-sizing: border-box;
      overflow: hidden;
      color: var(--wuu-color-text, currentColor);
      transition: color 320ms ease-out;
      font-size: var(--wuu-font-size-ui, 15px);
      font-variant-numeric: tabular-nums;
      line-height: 1.2;
      white-space: nowrap;
      cursor: default;
    }
    .plugin-token-speed[data-placement="rail"] {
      flex: none;
      width: 100%;
      min-width: 0;
      max-width: 100%;
      container: token-speed / inline-size;
    }
    .plugin-token-speed-rail-value {
      display: flex;
      width: 100%;
      flex-direction: column;
      align-items: center;
    }
    .plugin-token-speed-rail-value .plugin-token-speed-value {
      display: flex;
      max-width: 100%;
      flex-wrap: nowrap;
      align-items: baseline;
      justify-content: center;
    }
    .plugin-token-speed-narrow { display: none; }
    /* Use fewer digits only when three full-size digits cannot fit. */
    @container token-speed (max-width: 3ch) {
      .plugin-token-speed-rail-value { display: none; }
      .plugin-token-speed-narrow { display: flex; }
    }
    .plugin-token-speed-value { min-width: 0; }
    .plugin-token-speed-symbol,
    .plugin-token-speed-unit { font-size: max(10px, 0.75em); }
    .plugin-token-speed-unit {
      color: var(--wuu-color-text-muted, currentColor);
    }
    .plugin-token-speed[data-state="idle"] { color: var(--wuu-color-text-muted, currentColor); }
    .plugin-token-speed:focus-visible {
      outline: 2px solid var(--wuu-color-accent, currentColor);
      outline-offset: 2px;
      border-radius: 4px;
    }
    .plugin-token-speed[data-placement="rail"]:focus-visible { outline-offset: -2px; }
    @media (prefers-reduced-motion: reduce) {
      .plugin-token-speed { transition: none; }
    }
  ` });
}

function formatSpeedParts(speed, locale, narrow = false) {
  const suffixes = ["", "k", "M", "B", "T", "P"];
  let scaled = speed;
  let unit = 0;
  while (scaled >= 1_000 && unit < suffixes.length - 1) {
    scaled /= 1_000;
    unit += 1;
  }
  if (scaled >= (narrow ? 99.5 : 999.5) && unit < suffixes.length - 1) {
    scaled /= 1_000;
    unit += 1;
  }
  const parts = new Intl.NumberFormat(locale, {
    useGrouping: false,
    maximumFractionDigits: unit > 0 && scaled < 10 ? 1 : 0,
  }).formatToParts(scaled);
  if (unit > 0) parts.push({ type: "compact", value: suffixes[unit] });
  return parts;
}

function estimateTokens(text) {
  let ascii = 0;
  let other = 0;
  for (const character of text) {
    if (character.codePointAt(0) <= 0x7f) ascii += 1;
    else other += 1;
  }
  return ascii / 4 + other / 1.7;
}
