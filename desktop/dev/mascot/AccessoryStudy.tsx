import { useState, type CSSProperties } from "react";
import { WuuMascot, WUU_MASCOT_ACCESSORIES, type WuuMascotAccessory, type WuuMascotActivity } from "../../src/renderer/WuuMascot";
import { AgentAvatarMark, AGENT_AVATAR_SHAPES, serializeAgentAvatarConfig } from "../../src/renderer/AgentAvatarMark";
import { WUU_MASCOT_ACTIVITY_PERSPECTIVES, WUU_MASCOT_TRAITS } from "../../src/renderer/wuu-mascot-spec";
import "./beanie-study.css";

const LABELS = { none: "本体", beanie: "软帽", "hard-hat": "安全帽", headset: "耳麦", bandana: "三角巾", leaf: "小叶子" };

export function AccessoryStudy(): JSX.Element {
  const [accessory, setAccessory] = useState<WuuMascotAccessory>("beanie");
  const [dark, setDark] = useState(false);
  const [hue, setHue] = useState(14);
  const [follow, setFollow] = useState(true);
  const [activity, setActivity] = useState<WuuMascotActivity>("idle");
  const [yaw, setYaw] = useState(0);
  const [pitch, setPitch] = useState(0);
  const camera = { "--mo-yaw": WUU_MASCOT_ACTIVITY_PERSPECTIVES[activity].yaw + yaw, "--mo-pitch": WUU_MASCOT_ACTIVITY_PERSPECTIVES[activity].pitch + pitch } as CSSProperties;
  return <main className="beanie-study" data-theme={dark ? "dark" : "light"}>
    <header>
      <h1>配饰 · 造型与配色</h1>
      <label><input type="checkbox" checked={dark} onChange={e => setDark(e.target.checked)} />深色背景</label>
      <a href="/">全部配饰</a>
    </header>
    <section className="accessory-collection" aria-label="选择配饰">
      {WUU_MASCOT_ACCESSORIES.map(item => <button type="button" key={item} aria-pressed={accessory === item} aria-label={LABELS[item]} onClick={() => setAccessory(item)}>
        <WuuMascot size={72} accessory={item} identityHue={hue} activity={activity} style={camera} />
        <span>{LABELS[item]}</span>
      </button>)}
    </section>
    <section className="beanie-hero" aria-label="配饰与本体对照">
      <figure>
        <WuuMascot size={200} accessory="none" identityHue={hue} activity={activity} followPointer={follow} style={camera} />
        <figcaption>本体</figcaption>
      </figure>
      <figure>
        <WuuMascot size={200} accessory={accessory} identityHue={hue} activity={activity} followPointer={follow} style={camera} />
        <figcaption>{LABELS[accessory]}</figcaption>
      </figure>
    </section>
    <div className="beanie-controls">
      <label>颜色<input aria-label="颜色" type="range" min="0" max="359" value={hue} onChange={e => setHue(Number(e.target.value))} /></label>
      <label><input type="checkbox" checked={follow} onChange={e => setFollow(e.target.checked)} />跟随鼠标</label>
      <label>动作<select value={activity} onChange={e => setActivity(e.target.value as WuuMascotActivity)}>
        {Object.keys(WUU_MASCOT_ACTIVITY_PERSPECTIVES).map(state => <option key={state} value={state}>{state}</option>)}
      </select></label>
      <label>左右<input aria-label="左右" type="range" min="-18" max="18" value={yaw} onChange={e => setYaw(Number(e.target.value))} /></label>
      <label>俯仰<input aria-label="俯仰" type="range" min="-12" max="12" value={pitch} onChange={e => setPitch(Number(e.target.value))} /></label>
    </div>
    <section className="beanie-sizes" aria-label="实际尺寸">
      {[28, 40, 64, 96].map(size => <figure key={size}>
        <div><WuuMascot size={size} accessory={accessory} identityHue={hue} activity={activity} style={camera} /></div>
        <figcaption>{size} px</figcaption>
      </figure>)}
    </section>
    <section className="beanie-colors" aria-label="不同球体颜色">
      {[14, 52, 96, 150, 202, 222, 288, 350].map(color => <WuuMascot key={color} size={64} accessory={accessory} identityHue={color} activity={activity} style={camera} />)}
    </section>
    <section className="shapes" aria-label="不同球形">
      {AGENT_AVATAR_SHAPES.map(shape => <figure key={shape.id}><AgentAvatarMark seed={shape.id} avatarKey={serializeAgentAvatarConfig({ shape: shape.id, accessory, hue })} /><figcaption>{shape.id}</figcaption></figure>)}
    </section>
    <section className="shapes" aria-label="异形与动作">
      {AGENT_AVATAR_SHAPES.map(shape => <figure key={shape.id}>
        {[0, 1, 2].map(seed => <WuuMascot key={seed} size={64} identityName={`accessory-fit:${seed}`} identityTraits={{ ...WUU_MASCOT_TRAITS, shape: shape.trait }} identityHue={hue} accessory={accessory} activity={activity} style={camera} />)}
        <figcaption>{shape.id}</figcaption>
      </figure>)}
    </section>
  </main>;
}
