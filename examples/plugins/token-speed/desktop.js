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
  const { createElement: h, useEffect, useState, useSyncExternalStore } = api.react;
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

  function Speed({ threadId, turnId, running, inToolRail, translate, locale }) {
    useSyncExternalStore(
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

    const now = Date.now();
    const tokens = entry?.samples.reduce((total, sample) => sample.at > now - WINDOW_MS ? total + sample.tokens : total, 0);
    const elapsed = entry ? Math.max(MIN_SAMPLE_MS, Math.min(WINDOW_MS, now - entry.startedAt)) : 0;
    const speed = tokens === undefined ? undefined : Math.round(tokens * 1_000 / elapsed);
    const fullSpeed = speed === undefined ? "" : new Intl.NumberFormat(locale).format(speed);
    const compactParts = speed === undefined ? [] : new Intl.NumberFormat(locale, {
      notation: "compact", maximumFractionDigits: 0,
    }).formatToParts(speed);
    const compactSpeed = speed === undefined ? "—" : compactParts.map((part) => part.value).join("");
    const label = speed === undefined ? translate("tokenSpeed.waiting")
      : tokens === 0 ? translate("tokenSpeed.paused") : translate("tokenSpeed.estimated", { speed: fullSpeed });
    const readout = h("span", {
      className: "plugin-token-speed",
      "data-token-speed": "",
      "data-state": speed === undefined ? "idle" : tokens === 0 ? "paused" : "streaming",
      "data-placement": inToolRail ? "rail" : "footer",
      role: "img",
      tabIndex: 0,
      "aria-label": label,
      title: label,
    }, inToolRail
      ? [h("span", { key: "speed", className: "plugin-token-speed-value" }, speed === undefined ? h("span", { className: "plugin-token-speed-symbol" }, compactSpeed) : [
        h("span", { key: "approx", className: "plugin-token-speed-symbol" }, "≈"),
        ...compactParts.map((part, index) => h("span", {
          key: index, className: part.type === "integer" ? undefined : "plugin-token-speed-symbol",
        }, part.value)),
      ]), h("span", { key: "unit", className: "plugin-token-speed-unit" }, "/s")]
      : h("span", { className: "plugin-token-speed-value" }, `${speed === undefined ? "—" : `≈${compactSpeed}`} tok/s`));
    return readout;
  }

  api.registerUIContribution({
    id: "output-speed",
    target: "wuu.default-composer/composer.runtime",
    render: ({ context, translate, locale }) => context.mainConversation
      ? h(Speed, { ...context, translate, locale }) : null,
  });
  api.registerCSSSnippet({ id: "token-speed", css: `
    .plugin-token-speed {
      display: inline-flex;
      align-items: center;
      justify-content: center;
      min-width: 0;
      min-height: calc(var(--wuu-font-size-ui, 15px) * 2);
      color: var(--wuu-color-text, currentColor);
      font-size: var(--wuu-font-size-ui, 15px);
      font-variant-numeric: tabular-nums;
      line-height: 1.2;
      white-space: nowrap;
      cursor: default;
    }
    .plugin-token-speed[data-placement="rail"] { max-width: min(6ch, 100%); }
    .plugin-token-speed-value { min-width: 0; overflow: hidden; text-overflow: ellipsis; }
    .plugin-token-speed-symbol,
    .plugin-token-speed-unit { font-size: max(10px, 0.75em); }
    .plugin-token-speed-unit {
      flex: none;
      margin-inline-start: 0.125em;
      color: var(--wuu-color-text-muted, currentColor);
    }
    @supports not ((position-anchor: --anchor) and (anchor-scope: --anchor) and (position-visibility: anchors-visible)) {
      .plugin-token-speed[data-placement="rail"] { display: inline-grid; justify-items: center; }
      .plugin-token-speed[data-placement="rail"] .plugin-token-speed-unit { margin-inline-start: 0; }
    }
    .plugin-token-speed[data-state="idle"] { color: var(--wuu-color-text-muted, currentColor); }
    .plugin-token-speed:focus-visible {
      outline: 2px solid var(--wuu-color-accent, currentColor);
      outline-offset: 2px;
      border-radius: 4px;
    }
  ` });
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
