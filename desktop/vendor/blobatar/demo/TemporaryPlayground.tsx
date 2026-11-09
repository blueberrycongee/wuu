import { useMemo, useState, type CSSProperties } from "react";
import { _layout, traits, type Animate, type BlobatarOptions, type TraitOverrides } from "blobatar";
import { happy, idle, love, mad, sad, scared, shy, sick, sleepy, smug, surprised, unsure, wink, type Expression, type Pose } from "blobatar/expression";
import { Blobatar } from "blobatar/react";
import { ProductMascotWorkbench } from "./ProductMascotWorkbench";

const EXPRESSIONS = { idle, happy, sad, mad, surprised, wink, sleepy, smug, unsure, scared, love, shy, sick } satisfies Record<string, Expression>;
const TRAIT_GROUPS = {
  identity: ["shape", "hue", "tone"],
  eyes: ["gaze.x", "gaze.y", "eye.rx", "eye.ratio", "eye.scale", "eye.stretch", "eye.gap", "eye.n", "eye.lean", "eye.lean2", "eye.dy"],
} as const;
const ALL_TRAITS = Object.values(TRAIT_GROUPS).flat();
type TraitKey = (typeof ALL_TRAITS)[number];
const POSE_RANGES: Record<keyof Pose, readonly [number, number, number]> = {
  esx: [0.1, 2.5, 0.01], esy: [0.1, 2, 0.01], tilt: [-45, 45, 1], edy: [-8, 8, 0.1], edx: [-5, 5, 0.1],
  esx2: [-1, 1, 0.01], esy2: [-1, 1, 0.01], tilt2: [-45, 45, 1], lock: [0, 1, 0.01], heat: [0, 1, 0.01], shake: [0, 1, 0.01], bdy: [-8, 8, 0.1],
};
type Background = "none" | "circle" | "square" | "squircle";

function Slider({ label, value, min, max, step, enabled = true, onEnabled, onChange }: { label: string; value: number; min: number; max: number; step: number; enabled?: boolean; onEnabled?: (value: boolean) => void; onChange: (value: number) => void }) {
  return <label className={`tune-row${enabled ? "" : " is-off"}`}>
    <span className="tune-label">{onEnabled ? <input type="checkbox" checked={enabled} onChange={(event) => onEnabled(event.target.checked)} /> : null}<code>{label}</code></span>
    <input type="range" min={min} max={max} step={step} value={value} disabled={!enabled} onChange={(event) => onChange(Number(event.target.value))} />
    <input className="tune-number" type="number" min={min} max={max} step={step} value={Number(value.toFixed(3))} disabled={!enabled} onChange={(event) => onChange(Number(event.target.value))} />
  </label>;
}

export function TemporaryPlayground() {
  const [showProductWorkbench, setShowProductWorkbench] = useState(true);
  return showProductWorkbench
    ? <ProductMascotWorkbench onOpenTuning={() => setShowProductWorkbench(false)} />
    : <BlobatarTuningPlayground onBack={() => setShowProductWorkbench(true)} />;
}

function BlobatarTuningPlayground({ onBack }: { onBack: () => void }) {
  const [prefix, setPrefix] = useState("user-");
  const [count, setCount] = useState(120);
  const [size, setSize] = useState(56);
  const [background, setBackground] = useState<Background>("none");
  const [animate, setAnimate] = useState<Animate | "">("");
  const [expressionName, setExpressionName] = useState<keyof typeof EXPRESSIONS>("idle");
  const [pose, setPose] = useState<Pose>({ ...idle.p });
  const [poseCustom, setPoseCustom] = useState(false);
  const [traitValues, setTraitValues] = useState<Record<TraitKey, number>>(() => Object.fromEntries(ALL_TRAITS.map((key) => [key, 0.5])) as Record<TraitKey, number>);
  const [enabledTraits, setEnabledTraits] = useState<Set<TraitKey>>(new Set());
  const [hue, setHue] = useState<number | null>(null);
  const [tone, setTone] = useState<number | null>(null);
  const [contrast, setContrast] = useState(true);
  const [perspective, setPerspective] = useState({ yaw: -35, pitch: 18, strength: 1 });
  const [palette, setPalette] = useState({ bg: "", head: "", eye: "" });
  const [expressionsView, setExpressionsView] = useState(false);
  const [focus, setFocus] = useState("wuu");
  const selectedBase = EXPRESSIONS[expressionName];
  const expression = useMemo<Expression>(() => poseCustom ? { ...selectedBase, p: pose } : selectedBase, [poseCustom, pose, selectedBase]);
  const overrides = useMemo<TraitOverrides>(() => Object.fromEntries([...enabledTraits].map((key) => [key, traitValues[key]])), [enabledTraits, traitValues]);
  const paletteOverrides = useMemo(() => Object.fromEntries(Object.entries(palette).filter(([, value]) => value)), [palette]);
  const options = useMemo<BlobatarOptions>(() => ({ size, background: background === "none" ? false : background, animate: animate || undefined, expression, traits: overrides, hue: hue ?? undefined, tone: tone ?? undefined, contrast, palette: paletteOverrides, perspective }), [size, background, animate, expression, overrides, hue, tone, contrast, paletteOverrides, perspective]);
  const seeds = useMemo(() => Array.from({ length: count }, (_, index) => `${prefix}${index}`), [prefix, count]);
  const focusedLayout = _layout(focus, options);
  const flatOptions = { ...options, perspective: { ...perspective, strength: 0 } };
  const eyeReadout = (layout: typeof focusedLayout) => layout.eyes.map((eye) =>
    `x ${eye.cx.toFixed(1)} · 宽 ${((eye.rx / layout.body.rx) * 100).toFixed(1)}% · 角 ${(eye.rot + (eye.surfaceRot ?? 0)).toFixed(1)}° · 弯 ${(eye.bend ?? 0).toFixed(1)}`,
  );
  const chooseExpression = (name: keyof typeof EXPRESSIONS) => { setExpressionName(name); setPose({ ...EXPRESSIONS[name].p }); setPoseCustom(false); };
  const setTrait = (key: TraitKey, value: number) => setTraitValues((current) => ({ ...current, [key]: Math.max(0, Math.min(0.999, value)) }));
  const toggleTrait = (key: TraitKey, enabled: boolean) => setEnabledTraits((current) => { const next = new Set(current); if (enabled) next.add(key); else next.delete(key); return next; });
  const loadSeedTraits = () => { const seeded = traits(focus); setTraitValues(Object.fromEntries(ALL_TRAITS.map((key) => [key, seeded(key)])) as Record<TraitKey, number>); setEnabledTraits(new Set(ALL_TRAITS)); };

  return <div className="playground-shell">
    <aside className="playground-panel">
      <div className="playground-title"><div><strong>Blobatar 底层调参</strong><small>种子、生成参数、表情与球面透视</small></div><div className="playground-title-actions"><button type="button" onClick={onBack}>返回产品状态</button><button onClick={() => setEnabledTraits(new Set())}>清空覆盖</button></div></div>
      <details open><summary>展示</summary><div className="control-grid">
        <label>视图<select value={expressionsView ? "expressions" : "varieties"} onChange={(event) => setExpressionsView(event.target.value === "expressions")}><option value="varieties">种子变化</option><option value="expressions">全部表情</option></select></label>
        <label>种子前缀<input value={prefix} onChange={(event) => setPrefix(event.target.value)} /></label>
        <label>数量<input type="number" min="12" max="400" value={count} onChange={(event) => setCount(Number(event.target.value))} /></label>
        <label>尺寸<input type="number" min="16" max="160" value={size} onChange={(event) => setSize(Number(event.target.value))} /></label>
        <label>背景<select value={background} onChange={(event) => setBackground(event.target.value as Background)}><option value="none">透明</option><option value="circle">圆</option><option value="squircle">圆角方</option><option value="square">方形</option></select></label>
        <label>动画<select value={animate} onChange={(event) => setAnimate(event.target.value as Animate | "")}><option value="">关闭</option><option value="hover">悬停</option><option value="always">始终</option></select></label>
      </div></details>
      <details open><summary>球面透视</summary>
        <Slider label="yaw" value={perspective.yaw} min={-55} max={55} step={1} onChange={(value) => setPerspective((current) => ({ ...current, yaw: value }))} />
        <Slider label="pitch" value={perspective.pitch} min={-45} max={45} step={1} onChange={(value) => setPerspective((current) => ({ ...current, pitch: value }))} />
        <Slider label="strength" value={perspective.strength} min={0} max={1} step={0.01} onChange={(value) => setPerspective((current) => ({ ...current, strength: value }))} />
      </details>
      <details open><summary>颜色</summary>
        <Slider label="hue" value={hue ?? 14} min={0} max={360} step={1} enabled={hue !== null} onEnabled={(enabled) => setHue(enabled ? 14 : null)} onChange={setHue} />
        <Slider label="tone" value={tone ?? 0.5} min={0} max={0.999} step={0.01} enabled={tone !== null} onEnabled={(enabled) => setTone(enabled ? 0.5 : null)} onChange={setTone} />
        <label className="inline-check"><input type="checkbox" checked={contrast} onChange={(event) => setContrast(event.target.checked)} />强制对比度</label>
        <div className="color-row">{(["bg", "head", "eye"] as const).map((key) => <label key={key}>{key}<input type="color" value={palette[key] || "#ffffff"} onChange={(event) => setPalette((current) => ({ ...current, [key]: event.target.value }))} /><input type="checkbox" checked={Boolean(palette[key])} onChange={(event) => setPalette((current) => ({ ...current, [key]: event.target.checked ? (current[key] || "#ffffff") : "" }))} /></label>)}</div>
      </details>
      <details open><summary>表情 · {Object.keys(EXPRESSIONS).length} 种</summary>
        <select value={expressionName} onChange={(event) => chooseExpression(event.target.value as keyof typeof EXPRESSIONS)}>{Object.keys(EXPRESSIONS).map((name) => <option key={name}>{name}</option>)}</select>
        <label className="inline-check"><input type="checkbox" checked={poseCustom} onChange={(event) => setPoseCustom(event.target.checked)} />自定义表情参数</label>
        {Object.entries(POSE_RANGES).map(([key, [min, max, step]]) => <Slider key={key} label={key} value={pose[key as keyof Pose]} min={min} max={max} step={step} enabled={poseCustom} onChange={(value) => setPose((current) => ({ ...current, [key]: value }))} />)}
      </details>
      {Object.entries(TRAIT_GROUPS).map(([group, keys]) => <details key={group} open={group === "eyes"}><summary>{group} · {keys.length}</summary>{keys.map((key) => <Slider key={key} label={key} value={traitValues[key]} min={0} max={0.999} step={0.001} enabled={enabledTraits.has(key)} onEnabled={(enabled) => toggleTrait(key, enabled)} onChange={(value) => setTrait(key, value)} />)}</details>)}
    </aside>
    <main className="playground-stage">
      <header className="stage-header"><div><h1>{expressionsView ? `${Object.keys(EXPRESSIONS).length} 种表情` : `${count} 个种子变化`}</h1><p>6 种轮廓 · {ALL_TRAITS.length} 个生成参数 · 12 个表情参数 · 当前覆盖 {enabledTraits.size} 项</p></div><div className="focus-preview"><div className="perspective-compare"><figure><Blobatar name={focus} {...flatOptions} size={120} /><figcaption>平面</figcaption></figure><figure><Blobatar name={focus} {...options} size={120} /><figcaption>当前球面</figcaption></figure></div><span><strong>{focus}</strong><small>平面　{eyeReadout(_layout(focus, flatOptions)).join(" / ")}</small><small>球面　{eyeReadout(focusedLayout).join(" / ")}</small></span><button onClick={loadSeedTraits}>读取此种子的全部参数</button></div></header>
      <section className="playground-grid" style={{ "--preview-size": `${size}px` } as CSSProperties}>{expressionsView ? Object.entries(EXPRESSIONS).map(([name, item]) => <button key={name} onClick={() => chooseExpression(name as keyof typeof EXPRESSIONS)}><Blobatar name={focus} {...options} expression={item} /><span>{name}</span></button>) : seeds.map((seed) => <button key={seed} className={seed === focus ? "is-selected" : ""} onClick={() => setFocus(seed)}><Blobatar name={seed} {...options} /><span>{seed}<small>{_layout(seed, options).shape}</small></span></button>)}</section>
    </main>
  </div>;
}
