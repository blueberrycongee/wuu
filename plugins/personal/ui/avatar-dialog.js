import { DEFAULT_PERSONAL_AVATAR, PERSONAL_AVATAR_SHAPES, PERSONAL_AVATAR_ACCESSORIES, PERSONAL_AVATAR_HUES } from "../src/avatar.js";

export const avatarDialogStyles = `
.mosaic-avatar-dialog { position:relative;box-sizing:border-box;width:min(440px,100%);max-height:100%;overflow:auto;display:flex;flex-direction:column;gap:var(--space-4);padding:24px;border-radius:24px;background:var(--paper);box-shadow:0 12px 48px #00000026; }
.mosaic-avatar-dialog h2 { margin:0;padding-right:32px;font-size:var(--font-ui);font-weight:600; }
.mosaic-avatar-preview { display:flex;justify-content:center; }
.mosaic-avatar-dialog fieldset { border:0;margin:0;padding:0;min-width:0; }
.mosaic-avatar-dialog legend { padding:0;margin-bottom:var(--space-2);color:var(--ink-muted); }
.mosaic-avatar-options { display:grid;grid-template-columns:repeat(5,minmax(0,1fr));gap:var(--space-2); }
.mosaic-avatar-options button { min-width:0;min-height:48px;display:grid;place-items:center;padding:4px;border:1px solid transparent;border-radius:var(--radius-md,10px);background:transparent;color:var(--ink);cursor:pointer; }
.mosaic-avatar-options button[aria-pressed="true"] { border-color:var(--accent);background:var(--field-hover-bg); }
.mosaic-avatar-dialog button:focus-visible,.mosaic-avatar-dialog input:focus-visible { outline:2px solid var(--accent);outline-offset:2px; }
.mosaic-avatar-colors { grid-template-columns:repeat(6,minmax(0,1fr)); }
.mosaic-avatar-colors button { min-height:36px; }
.mosaic-avatar-swatch { width:24px;height:24px;border-radius:50%;background:var(--swatch);box-shadow:inset 0 0 0 1px #00000020; }
.mosaic-avatar-hue { display:flex;align-items:center;gap:var(--space-2);margin-top:var(--space-2); }
.mosaic-avatar-hue input { flex:1;min-width:0;accent-color:var(--accent); }
.mosaic-avatar-actions { display:flex;flex-wrap:wrap;justify-content:flex-end;gap:var(--space-2); }
.mosaic-avatar-actions > :first-child { margin-right:auto; }
`;

export function createAvatarDialog({ react: React, ui: { Avatar, Button } }) {
  const h = React.createElement;
  return function AvatarDialog({ agent, locale, onSave, onClose }) {
    const zh = locale.startsWith("zh"), label = (en, cn) => zh ? cn : en;
    const [key, setKey] = React.useState(agent.avatar_key || DEFAULT_PERSONAL_AVATAR);
    const [saving, setSaving] = React.useState(false), [error, setError] = React.useState("");
    const dialog = React.useRef(null), pending = React.useRef(false), alive = React.useRef(true);
    const [, shape, accessory, rawHue] = key.split(":"), hue = Number(rawHue);
    const changed = key !== (agent.avatar_key || DEFAULT_PERSONAL_AVATAR);
    React.useEffect(() => { alive.current = true; dialog.current?.focus(); return () => { alive.current = false; }; }, []);
    const choose = (nextShape, nextAccessory, nextHue) => { setKey(`mascot-v1:${nextShape}:${nextAccessory}:${nextHue}`); setError(""); };
    const save = async event => {
      event.preventDefault();
      if (pending.current || !changed) return;
      pending.current = true; setSaving(true); setError(""); dialog.current?.focus();
      try { await onSave(key); }
      catch (cause) { if (alive.current) setError(`${label("Could not confirm the saved style. Try again.", "未能确认样式已保存，请重试。")} ${cause instanceof Error ? cause.message : String(cause)}`); }
      finally { pending.current = false; if (alive.current) setSaving(false); }
    };
    const shapes = { round: label("Round", "圆形"), "rounded-square": label("Rounded square", "圆角方形"), capsule: label("Capsule", "胶囊"), triangle: label("Triangle", "三角"), diamond: label("Diamond", "菱形") };
    const accessories = { none: label("None", "无配饰"), beanie: label("Beanie", "毛线帽"), "hard-hat": label("Hard hat", "安全帽"), headset: label("Headset", "耳机"), leaf: label("Leaf", "叶子") };
    return h("div", { className: "mosaic-name-backdrop", onPointerDown: event => { if (!pending.current && event.target === event.currentTarget) onClose(); } },
      h("form", { ref: dialog, className: "mosaic-avatar-dialog", tabIndex: -1, role: "dialog", "aria-modal": true, "aria-label": label("Change style", "更换样式"), "aria-busy": saving, onSubmit: save, onKeyDown: event => {
        if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); if (!pending.current) onClose(); }
        if (event.key === "Tab") {
          const controls = [...dialog.current.querySelectorAll("button:not(:disabled),input:not(:disabled)")], first = controls[0], last = controls.at(-1);
          if (!first) { event.preventDefault(); dialog.current.focus(); }
          else if (event.shiftKey && (document.activeElement === first || document.activeElement === dialog.current)) { event.preventDefault(); last.focus(); }
          else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
        }
      } },
      h("h2", null, label("Change style", "更换样式")),
      h("button", { type: "button", className: "mosaic-name-close", disabled: saving, "aria-label": label("Close", "关闭"), onClick: onClose }, "×"),
      h("div", { className: "mosaic-avatar-preview" }, h(Avatar, { size: 112, avatarKey: key, motion: "ambient" })),
      h("fieldset", { disabled: saving }, h("legend", null, label("Shape", "形状")), h("div", { className: "mosaic-avatar-options" }, PERSONAL_AVATAR_SHAPES.map(value => h("button", { key: value, type: "button", "data-avatar-shape": value, "aria-label": shapes[value], title: shapes[value], "aria-pressed": shape === value, onClick: () => choose(value, accessory, hue) }, h(Avatar, { size: 40, avatarKey: `mascot-v1:${value}:${accessory}:${hue}` }))))),
      h("fieldset", { disabled: saving }, h("legend", null, label("Accessory", "配饰")), h("div", { className: "mosaic-avatar-options" }, PERSONAL_AVATAR_ACCESSORIES.map(value => h("button", { key: value, type: "button", "data-avatar-accessory": value, "aria-label": accessories[value], title: accessories[value], "aria-pressed": accessory === value, onClick: () => choose(shape, value, hue) }, h(Avatar, { size: 40, avatarKey: `mascot-v1:${shape}:${value}:${hue}` }))))),
      h("fieldset", { disabled: saving }, h("legend", null, label("Color", "颜色")), h("div", { className: "mosaic-avatar-options mosaic-avatar-colors" }, PERSONAL_AVATAR_HUES.map(value => h("button", { key: value, type: "button", "data-avatar-hue": value, "aria-label": `${label("Hue", "色相")} ${value}°`, "aria-pressed": hue === value, onClick: () => choose(shape, accessory, value) }, h("span", { className: "mosaic-avatar-swatch", style: { "--swatch": `hsl(${value} 65% 65%)` } })))),
        h("label", { className: "mosaic-avatar-hue" }, label("Hue", "色相"), h("input", { type: "range", min: 0, max: 359, step: 1, value: hue, onChange: event => choose(shape, accessory, Number(event.target.value)) }), h("output", null, `${hue}°`))),
      error && h("p", { className: "mosaic-name-error", role: "alert" }, error),
      h("div", { className: "mosaic-avatar-actions" },
        h(Button, { type: "button", variant: "ghost", disabled: saving || key === DEFAULT_PERSONAL_AVATAR, "data-action": "reset-avatar", onClick: () => { setKey(DEFAULT_PERSONAL_AVATAR); setError(""); } }, label("Reset", "恢复默认")),
        h(Button, { type: "button", disabled: saving, "data-action": "cancel-avatar", onClick: onClose }, label("Cancel", "取消")),
        h(Button, { type: "submit", variant: "primary", disabled: saving || !changed, "data-action": "save-avatar" }, saving ? label("Saving…", "保存中…") : label("Save", "保存")))));
  };
}
