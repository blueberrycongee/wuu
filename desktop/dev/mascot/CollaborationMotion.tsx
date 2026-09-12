import { useEffect, useState, type JSX } from "react";
import { createRoot } from "react-dom/client";
import { AgentAvatarMark, type AgentAvatarStatus } from "../../src/renderer/AgentAvatarMark";
import "./collaboration-motion.css";

const states: [AgentAvatarStatus, string][] = [
  ["idle", "就绪"], ["thinking", "思考"], ["responding", "回复"], ["sending", "发送"],
  ["queued", "排队"], ["waiting", "等待"], ["failed", "失败"], ["interrupted", "中断"],
];
const identities = [
  { seed: "research", name: "Research", key: "mascot-v1:round:none:150" },
  { seed: "builder", name: "Builder", key: "mascot-v1:organic:beanie:202" },
  { seed: "reviewer", name: "Reviewer", key: "mascot-v1:boxy:headphones:33" },
];

function CollaborationMotion(): JSX.Element {
  const [status, setStatus] = useState<AgentAvatarStatus>("idle");
  const [playing, setPlaying] = useState(false);
  const [dark, setDark] = useState(false);
  const [identity, setIdentity] = useState(0);
  const [turnSignal, setTurnSignal] = useState(0);
  const agent = identities[identity];
  const label = states.find(([value]) => value === status)![1];

  useEffect(() => {
    if (!playing) return;
    setStatus("idle");
    const timers = [
      window.setTimeout(() => setStatus("thinking"), 900),
      window.setTimeout(() => setStatus("responding"), 6500),
      window.setTimeout(() => setStatus("idle"), 10500),
      window.setTimeout(() => setPlaying(false), 11600),
    ];
    return () => timers.forEach(window.clearTimeout);
  }, [playing]);

  useEffect(() => { document.documentElement.dataset.theme = dark ? "dark" : "light"; }, [dark]);

  return <main className="motion-preview">
    <header className="motion-toolbar">
      <div><span className="motion-wordmark">wuu</span><span className="motion-subtitle">Collaboration / 动效预览</span></div>
      <button type="button" aria-pressed={dark} onClick={() => setDark(!dark)}>深色</button>
    </header>
    <div className="motion-workbench">
      <aside className="motion-sidebar" aria-label="Agent 身份">
        {identities.map((item, index) => <button type="button" key={item.seed} aria-pressed={index === identity} onClick={() => setIdentity(index)}>
          <AgentAvatarMark seed={item.seed} avatarKey={item.key} status={index === identity ? status : "idle"} motion="subtle" />
          <span>{item.name}<small>{index === identity ? label : "就绪"}</small></span>
        </button>)}
      </aside>
      <section className="motion-stage" aria-label="放大观察">
        <div className="motion-hero"><AgentAvatarMark seed={agent.seed} avatarKey={agent.key} status={status} turnSignal={turnSignal} /></div>
        <div className="motion-caption"><strong>{agent.name}</strong><span aria-live="polite">{label}</span></div>
        <div className="motion-controls" aria-label="动画状态">
          {states.map(([value, name]) => <button type="button" key={value} aria-pressed={value === status} onClick={() => { setPlaying(false); setStatus(value); }}>{name}</button>)}
        </div>
        <div className="motion-playback"><button className="motion-play" type="button" onClick={() => setPlaying(!playing)}>{playing ? "停止播放" : "播放一轮"}</button>
          <button type="button" onClick={() => setTurnSignal(value => value + 1)}>转个圈</button></div>
      </section>
    </div>
    <section className="motion-context" aria-label="实际尺寸">
      <div className="motion-conversation">
        <p className="motion-human">帮我看看这次改动。</p>
        <div className="motion-history"><AgentAvatarMark seed="history" avatarKey={agent.key} /><p>我先检查相关实现和调用关系。</p></div>
        <div className="motion-live"><AgentAvatarMark seed={agent.seed} avatarKey={agent.key} status={status} turnSignal={turnSignal} /><span><strong>{agent.name}</strong><small>{label}</small></span></div>
      </div>
      <div className="motion-sizes">{[24, 32, 48].map(size => <figure key={size}>
        <div style={{ width: size, height: size }}><AgentAvatarMark seed={agent.seed} avatarKey={agent.key} status={status} turnSignal={turnSignal} /></div><figcaption>{size}px</figcaption>
      </figure>)}</div>
    </section>
    <section className="motion-state-sheet" aria-label="全部状态对照">
      {states.map(([value, name]) => <figure key={value}><div><AgentAvatarMark seed={`sheet-${value}`} avatarKey={agent.key} status={value} motion="subtle" /></div><figcaption>{name}</figcaption></figure>)}
    </section>
  </main>;
}

createRoot(document.getElementById("root")!).render(<CollaborationMotion />);
