import { useState } from "react";
import { Blobatar } from "../src/react";
import { idle, happy, wink, sleepy } from "../src/expression";
import { WUU_MASCOT_NAME, WUU_MASCOT_TRAITS } from "../../../src/renderer/wuu-mascot-spec";

const expressions = { idle, happy, wink, sleepy };

/** The product's chart and camera, with no hand-drawn alternative preview. */
export function SurfaceWorkbench() {
  const [yaw, setYaw] = useState(30);
  const [pitch, setPitch] = useState(12);
  const [expression, setExpression] = useState<keyof typeof expressions>("idle");
  const [motion, setMotion] = useState(false);
  const opts = {
    name: WUU_MASCOT_NAME,
    traits: WUU_MASCOT_TRAITS,
    hue: 14,
    background: false as const,
    expression: expressions[expression],
  };
  return <section aria-label="曲面造型" className="surface-workbench">
    <div className="control-grid">
      <label>左右转动 · {yaw}°<input aria-label="Yaw" type="range" min="-55" max="55" value={yaw} onChange={(e) => setYaw(+e.target.value)} /></label>
      <label>上下转动 · {pitch}°<input aria-label="Pitch" type="range" min="-45" max="45" value={pitch} onChange={(e) => setPitch(+e.target.value)} /></label>
      <label>表情<select aria-label="Expression" value={expression} onChange={(e) => setExpression(e.target.value as keyof typeof expressions)}>
        {Object.keys(expressions).map((key) => <option key={key}>{key}</option>)}
      </select></label>
      <label className="inline-check"><input type="checkbox" checked={motion} onChange={(e) => setMotion(e.target.checked)} />眨眼与视线动画</label>
    </div>
    <div style={{ display: "flex", alignItems: "center", justifyContent: "center", gap: 32, padding: 24 }}>
      <Blobatar {...opts} title="实时曲面" size={240} animate="always" perspective={{ yaw, pitch, strength: 1 }} style={{ "--mo-amp": motion ? 1 : 0 } as React.CSSProperties} />
      <figure style={{ textAlign: "center" }}>
        <Blobatar {...opts} title="静态导出" size={120} perspective={{ yaw, pitch, strength: 1 }} />
        <figcaption>同一参数的静态导出</figcaption>
      </figure>
    </div>
    <p>拖动角度，观察远侧眼睛的弯曲、收窄和遮挡。表情先改变球面上的形状，再经过同一相机投影。</p>
    <div className="mascot-size-grid">
      {[24, 32, 48, 96].map((size) => <figure key={size}>
        <Blobatar {...opts} size={size} perspective={{ yaw, pitch, strength: 1 }} />
        <figcaption>{size}px</figcaption>
      </figure>)}
    </div>
  </section>;
}
