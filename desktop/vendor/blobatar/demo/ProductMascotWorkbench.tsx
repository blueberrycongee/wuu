import { useState } from "react";
import { SurfaceWorkbench } from "./SurfaceWorkbench";
import {
  WUU_MASCOT_ACTIVITY_EXPRESSIONS,
  WUU_MASCOT_EYES,
  WuuMascot,
  type WuuMascotAccessory,
  type WuuMascotActivity,
} from "../../../src/renderer/WuuMascot";
import {
  WUU_MASCOT_ACTIVITY_PERSPECTIVES,
  WUU_MASCOT_DEFAULT_HUE,
  WUU_MASCOT_NAME,
  WUU_MASCOT_TRAITS,
} from "../../../src/renderer/wuu-mascot-spec";

const ACTIVITIES: readonly WuuMascotActivity[] = [
  "idle",
  "compose",
  "thinking",
  "compact",
  "search",
  "edit",
  "command",
  "read",
  "tool",
];

const ACTIVITY_LABELS: Readonly<Record<WuuMascotActivity, string>> = {
  idle: "待机",
  compose: "输入",
  thinking: "思考",
  compact: "压缩上下文",
  search: "搜索",
  edit: "编辑",
  command: "执行命令",
  read: "阅读",
  tool: "调用工具",
};

const ACCESSORIES: readonly WuuMascotAccessory[] = [
  "none",
  "cap",
  "beanie",
  "top-hat",
  "sprout",
  "crown",
  "headphones",
  "scarf",
  "beret",
  "party-hat",
  "wizard-hat",
  "chef-hat",
  "flower",
  "halo",
  "bow-tie",
  "graduation-cap",
  "cowboy-hat",
  "propeller-cap",
  "mushroom-cap",
  "bunny-ears",
  "cat-ears",
  "ribbon",
  "necktie",
];

const PRODUCT_SIZES = [24, 28, 32, 48, 64, 96] as const;
type ProductView = "activities" | "accessories" | "sizes" | "surface";

export function ProductMascotWorkbench({
  onOpenTuning,
}: {
  onOpenTuning: () => void;
}): JSX.Element {
  const [view, setView] = useState<ProductView>("activities");
  const [activity, setActivity] = useState<WuuMascotActivity>("idle");
  const [accessory, setAccessory] = useState<WuuMascotAccessory>("none");
  const [size, setSize] = useState(72);
  const [provider, setProvider] = useState("");
  const [followPointer, setFollowPointer] = useState(false);
  const perspective = WUU_MASCOT_ACTIVITY_PERSPECTIVES[activity];
  const pose = WUU_MASCOT_ACTIVITY_EXPRESSIONS[activity]?.p;

  return (
    <div className="product-mascot-workbench">
      <aside className="product-mascot-panel">
        <div className="playground-title">
          <div>
            <strong>Wuu 小球工作台</strong>
            <small>直接渲染产品中的 WuuMascot</small>
          </div>
          <button type="button" onClick={onOpenTuning}>底层调参</button>
        </div>

        <nav className="product-view-tabs" aria-label="工作台视图">
          {(["activities", "accessories", "sizes", "surface"] as const).map((item) => (
            <button
              key={item}
              type="button"
              className={view === item ? "is-active" : ""}
              onClick={() => setView(item)}
            >
              {item === "activities" ? "活动状态" : item === "accessories" ? "配饰" : item === "surface" ? "曲面造型" : "真实尺寸"}
            </button>
          ))}
        </nav>

        <section className="product-focus-preview">
          <WuuMascot
            className="process-surface-blobatar"
            activity={activity}
            accessory={accessory}
            provider={provider || undefined}
            followPointer={followPointer}
            size={180}
          />
          <div>
            <strong>{ACTIVITY_LABELS[activity]}</strong>
            <code>{activity}</code>
          </div>
        </section>

        <details open>
          <summary>预览控制</summary>
          <div className="control-grid">
            <label>
              活动状态
              <select value={activity} onChange={(event) => setActivity(event.target.value as WuuMascotActivity)}>
                {ACTIVITIES.map((item) => <option key={item} value={item}>{ACTIVITY_LABELS[item]}</option>)}
              </select>
            </label>
            <label>
              配饰
              <select value={accessory} onChange={(event) => setAccessory(event.target.value as WuuMascotAccessory)}>
                {ACCESSORIES.map((item) => <option key={item}>{item}</option>)}
              </select>
            </label>
            <label>
              网格尺寸
              <input type="number" min="24" max="140" value={size} onChange={(event) => setSize(Number(event.target.value))} />
            </label>
            <label>
              Provider 色
              <input value={provider} placeholder="默认颜色" onChange={(event) => setProvider(event.target.value)} />
            </label>
          </div>
          <label className="inline-check">
            <input type="checkbox" checked={followPointer} onChange={(event) => setFollowPointer(event.target.checked)} />
            调试鼠标追视（产品进程状态默认关闭）
          </label>
        </details>

        <details open>
          <summary>当前产品参数</summary>
          <dl className="mascot-parameter-list">
            <div><dt>name</dt><dd>{WUU_MASCOT_NAME}</dd></div>
            <div><dt>hue</dt><dd>{WUU_MASCOT_DEFAULT_HUE}</dd></div>
            <div><dt>eye.long</dt><dd>宽 {WUU_MASCOT_EYES.long.esx} × 高 {WUU_MASCOT_EYES.long.esy}</dd></div>
            <div><dt>perspective</dt><dd>{perspective ? `yaw ${perspective.yaw} · pitch ${perspective.pitch} · strength ${perspective.strength}` : "平面"}</dd></div>
            {pose ? Object.entries(pose).map(([key, value]) => (
              <div key={key}><dt>{key}</dt><dd>{value}</dd></div>
            )) : null}
          </dl>
        </details>

        <details>
          <summary>固定身份参数 · {Object.keys(WUU_MASCOT_TRAITS).length}</summary>
          <dl className="mascot-parameter-list">
            {Object.entries(WUU_MASCOT_TRAITS).map(([key, value]) => (
              <div key={key}><dt>{key}</dt><dd>{Number(value).toFixed(3)}</dd></div>
            ))}
          </dl>
        </details>
      </aside>

      <main className="product-mascot-stage">
        <header>
          <div>
            <h1>{view === "activities" ? "9 种产品活动状态" : view === "accessories" ? `${ACCESSORIES.length} 种配饰` : view === "surface" ? "曲面与透视" : "产品实际显示尺寸"}</h1>
            <p>这里使用桌面端同一个组件与配置，不是近似复刻。</p>
          </div>
        </header>

        {view === "surface" ? <SurfaceWorkbench /> : view === "activities" ? (
          <section className="mascot-state-grid" style={{ "--mascot-preview-size": `${size}px` } as React.CSSProperties}>
            {ACTIVITIES.map((item) => {
              const itemPerspective = WUU_MASCOT_ACTIVITY_PERSPECTIVES[item];
              const itemPose = WUU_MASCOT_ACTIVITY_EXPRESSIONS[item]?.p;
              return (
                <button key={item} type="button" className={activity === item ? "is-selected" : ""} onClick={() => setActivity(item)}>
                  <WuuMascot className="process-surface-blobatar" activity={item} accessory={accessory} provider={provider || undefined} followPointer={followPointer} />
                  <strong>{ACTIVITY_LABELS[item]}</strong>
                  <code>{item}</code>
                  <small>{itemPerspective ? `yaw ${itemPerspective.yaw} · pitch ${itemPerspective.pitch}` : "平面"}</small>
                  <small>{item === "compact" ? "5.4s 挤压循环" : itemPose ? `眼睛 ${itemPose.esx} × ${itemPose.esy}` : "默认眼睛"}</small>
                </button>
              );
            })}
          </section>
        ) : view === "accessories" ? (
          <section className="mascot-state-grid" style={{ "--mascot-preview-size": `${size}px` } as React.CSSProperties}>
            {ACCESSORIES.map((item) => (
              <button key={item} type="button" className={accessory === item ? "is-selected" : ""} onClick={() => setAccessory(item)}>
                <WuuMascot className="process-surface-blobatar" activity={activity} accessory={item} provider={provider || undefined} />
                <strong>{item}</strong>
              </button>
            ))}
          </section>
        ) : (
          <section className="mascot-size-grid">
            {PRODUCT_SIZES.map((item) => (
              <figure key={item}>
                <div><WuuMascot className="process-surface-blobatar" activity={activity} accessory={accessory} provider={provider || undefined} size={item} /></div>
                <figcaption><strong>{item}px</strong><small>{item <= 32 ? "进程行 / 紧凑界面" : item === 64 ? "欢迎页" : "放大检查"}</small></figcaption>
              </figure>
            ))}
          </section>
        )}
      </main>
    </div>
  );
}
