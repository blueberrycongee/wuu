import { useEffect, useRef } from "react";
import { createRoot } from "react-dom/client";
import type { UsageOverviewResponse } from "../../src/shared/protocol";
import { EmptyConversationHome } from "../../src/renderer/LoadingViews";
import { EmptyHomeOverview } from "../../src/renderer/EmptyHomeOverview";
import { startEmptyHomePlay } from "../../src/renderer/useEmptyHomePlay";
import "../../src/renderer/styles.css";
import "./preview.css";

const params = new URLSearchParams(location.search);
document.documentElement.dataset.theme = params.get("theme") ?? "light";
document.documentElement.style.setProperty("--conversation-message-font-size", `${params.get("font") ?? 14}px`);
const days = params.has("empty") ? [] : Array.from({ length: 365 }, (_, index) => {
  const date = new Date();
  date.setDate(date.getDate() - index);
  return {
    date: `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, "0")}-${String(date.getDate()).padStart(2, "0")}`,
    input_tokens: index % 5 ? (index % 11) * 1_000 : 0,
    output_tokens: 0, cache_creation_tokens: 0, cache_read_tokens: 0,
    cache_hit_rate: 0, turns: 0, agents: 0,
  };
});
const usage: UsageOverviewResponse = {
  total_sessions: days.length ? 256 : 0,
  metrics: {
    input_tokens: days.length ? 1_280_000 : 0, output_tokens: 0,
    cache_creation_tokens: 0, cache_read_tokens: 0, cache_hit_rate: 0,
    prompt_tokens: 0, context_tokens: 0, turns: 0, agents: 0,
    date_range: ["", ""], active_days: days.filter((day) => day.input_tokens > 0).length,
  },
  days,
};
// Only the usage read exists in this fixture. No product bridge or user data.
Object.assign(window, { wuu: { getUsageOverview: async () => usage } });

function Preview() {
  const play = useRef<ReturnType<typeof startEmptyHomePlay>>(undefined);
  useEffect(() => () => play.current?.stop(), []);
  return <>
    <nav aria-label="Preview scenes">
      {(["bounce", "snake", "breakout"] as const).map((kind) => <button key={kind} onClick={() => {
        play.current?.stop();
        const card = document.querySelector<HTMLElement>(".empty-home-overview");
        if (card) play.current = startEmptyHomePlay(card, kind, () => { play.current = undefined; });
      }}>{kind}</button>)}
      <button onClick={() => { play.current?.stop(); play.current = undefined; }}>Stop</button>
    </nav>
    <EmptyConversationHome title={params.has("long") ? "夜深了，今天的灵感还没写完，要不要再一起试一试？" : "夜深了，还要继续吗？"}>
      <EmptyHomeOverview />
    </EmptyConversationHome>
  </>;
}

createRoot(document.getElementById("root")!).render(<Preview />);
