import { useEffect, useState, type JSX } from "react";
import { createRoot } from "react-dom/client";
import { AgentAvatarMark } from "../../src/renderer/AgentAvatarMark";
import { MorphAvatar, MORPH_STATES, CHARACTER_STATES, ALL_MOTION_STATES, type PreviewMotion } from "./MorphAvatar";
import { ProcessSurfaceMascot } from "../../src/renderer/ProcessSurface";
import type { WuuMascotActivity } from "../../src/renderer/wuu-mascot-spec";
import "./collaboration-motion.css";

const identities = [
  { seed: "research", name: "Research", key: "mascot-v1:round:none:150" },
  { seed: "builder", name: "Builder", key: "mascot-v1:capsule:beanie:202" },
  { seed: "reviewer", name: "Reviewer", key: "mascot-v1:rounded-square:headset:33" },
];

function CollaborationMotion(): JSX.Element {
  const [activity, setActivity] = useState<WuuMascotActivity>("thinking");
  const [status, setStatus] = useState<PreviewMotion>("dots");
  const [playing, setPlaying] = useState(false);
  const [paused, setPaused] = useState(false);
  const [dark, setDark] = useState(false);
  const [identity, setIdentity] = useState(0);
  const [replay, setReplay] = useState(0);
  const agent = identities[identity];
  const label = ALL_MOTION_STATES.find(([value]) => value === status)![1];

  useEffect(() => {
    if (!playing || paused) return;
    const index = ALL_MOTION_STATES.findIndex(([value]) => value === status);
    const timer = window.setTimeout(() => {
      if (index + 1 === ALL_MOTION_STATES.length) { setPlaying(false); setStatus("idle"); }
      else setStatus(ALL_MOTION_STATES[index + 1][0]);
    }, 4500);
    return () => window.clearTimeout(timer);
  }, [playing, paused, status]);

  useEffect(() => { document.documentElement.dataset.theme = dark ? "dark" : "light"; }, [dark]);
  const select = (mode: PreviewMotion) => { setPlaying(false); setStatus(mode); setReplay(value => value + 1); };

  return <main className="motion-preview">
    <header className="motion-toolbar">
      <div><span className="motion-wordmark">wuu</span><span className="motion-subtitle">Collaboration / 动效预览</span></div>
      <button type="button" aria-pressed={dark} onClick={() => setDark(!dark)}>深色</button>
    </header>
    <section className="motion-production" aria-label="产品实际状态">
      <label>产品状态 <select value={activity} onChange={event => setActivity(event.target.value as WuuMascotActivity)}>
        {([ ["idle", "就绪"], ["thinking", "思考"], ["search", "搜索"], ["read", "阅读"], ["edit", "书写"], ["command", "命令"], ["tool", "工具"], ["compact", "压缩"], ["sending", "发送"], ["responding", "回复"], ["queued", "排队"], ["waiting", "等待用户"], ["failed", "失败"], ["interrupted", "中断"] ] as const).map(([value, label]) => <option key={value} value={value}>{label}</option>)}
      </select></label>
      <span>Collaboration <span className="motion-product-avatar"><AgentAvatarMark seed={agent.seed} avatarKey={agent.key} activity={activity} /></span></span>
      <span>Harness <ProcessSurfaceMascot active activity={activity} /></span>
    </section>
    <div className="motion-workbench">
      <aside className="motion-sidebar" aria-label="Agent 身份">
        {identities.map((item, index) => <button type="button" key={item.seed} aria-pressed={index === identity} onClick={() => setIdentity(index)}>
          <AgentAvatarMark seed={item.seed} avatarKey={item.key} motion="subtle" />
          <span>{item.name}<small>{index === identity ? label : "就绪"}</small></span>
        </button>)}
      </aside>
      <section className="motion-stage" aria-label="放大观察">
        <div className="motion-hero"><MorphAvatar avatarKey={agent.key} mode={status} paused={paused} replay={replay} /></div>
        <div className="motion-caption"><strong>{agent.name}</strong><span aria-live="polite">{label}</span></div>
        <div className="motion-controls" aria-label="变形动作">
          {MORPH_STATES.map(([value, name]) => <button type="button" key={value} data-select-motion={value} aria-pressed={value === status} onClick={() => select(value)}>{name}</button>)}
        </div>
        <div className="motion-controls motion-character-controls" aria-label="角色状态">
          {CHARACTER_STATES.map(([value, name]) => <button type="button" key={value} data-select-motion={value} aria-pressed={value === status} onClick={() => select(value)}>{name}</button>)}
        </div>
        <div className="motion-playback">
          <button className="motion-play" type="button" onClick={() => { setPlaying(!playing); setPaused(false); if (!playing) { setStatus("dots"); setReplay(value => value + 1); } }}>{playing ? "停止轮播" : "全部播放"}</button>
          <button type="button" aria-pressed={paused} onClick={() => setPaused(!paused)}>{paused ? "继续" : "暂停"}</button>
          <button type="button" onClick={() => select("idle")}>恢复角色</button>
        </div>
      </section>
    </div>
    <section className="motion-context" aria-label="实际尺寸">
      <div className="motion-conversation">
        <p className="motion-human">帮我看看这次改动。</p>
        <div className="motion-history"><AgentAvatarMark seed="history" avatarKey={agent.key} motion="subtle" /><p>我先检查相关实现和调用关系。</p></div>
        <div className="motion-live"><MorphAvatar avatarKey={agent.key} mode={status} paused={paused} replay={replay} /><span><strong>{agent.name}</strong><small>{label}</small></span></div>
      </div>
      <div className="motion-sizes">{[24, 32, 48].map(size => <figure key={size}>
        <div style={{ width: size, height: size }}><MorphAvatar avatarKey={agent.key} mode={status} paused={paused} replay={replay} /></div><figcaption>{size}px</figcaption>
      </figure>)}</div>
    </section>
    <section className="motion-state-sheet" aria-label="全部变形对照">
      {MORPH_STATES.map(([value, name]) => <button type="button" key={value} aria-pressed={status === value} onClick={() => select(value)}>
        <span className="motion-sheet-avatar"><MorphAvatar avatarKey={agent.key} mode={value} paused={paused} /></span><span>{name}</span>
      </button>)}
    </section>
  </main>;
}

createRoot(document.getElementById("root")!).render(<CollaborationMotion />);
