import { DEFAULT_PERSONAL_AVATAR, PERSONAL_AVATAR_SHAPES, PERSONAL_AVATAR_ACCESSORIES, PERSONAL_AVATAR_HUES } from "../src/avatar.js";

export const avatarDialogStyles = `
.mosaic-avatar-dialog { position:relative;box-sizing:border-box;width:min(800px,100%);max-height:100%;overflow:auto;display:grid;grid-template-columns:minmax(0,1.2fr) minmax(0,1fr);gap:32px;padding:32px;border-radius:24px;background:var(--paper);box-shadow:0 12px 48px #00000026; }
.mosaic-avatar-controls { display:flex;flex-direction:column;gap:24px;min-width:0; }
.mosaic-avatar-summary { display:flex;flex-direction:column;gap:24px;min-width:0; }
.mosaic-name-input { box-sizing:border-box;width:100%;min-width:0;border:1px solid transparent;border-radius:var(--radius-md,12px);background:var(--surface-1);color:var(--ink);padding:12px;font:inherit;font-size:calc(var(--font-ui) * 2); }
.mosaic-avatar-dialog h2 { grid-column:1/-1;margin:0;padding-right:32px;font-size:var(--font-ui);font-weight:600; }
.mosaic-avatar-preview { display:flex;align-items:center;justify-content:center;flex:1;min-height:180px; }
.mosaic-avatar-dialog fieldset { border:0;margin:0;padding:0;min-width:0; }
.mosaic-avatar-dialog legend { padding:0;margin-bottom:var(--space-2);color:var(--ink-muted); }
.mosaic-avatar-options { display:grid;grid-template-columns:repeat(5,minmax(0,1fr));gap:var(--space-2); }
.mosaic-avatar-options button { position:relative;min-width:0;min-height:48px;display:grid;place-items:center;padding:4px;border:0;border-radius:var(--radius-md,10px);background:transparent;color:var(--ink);cursor:pointer; }
.mosaic-avatar-options button:hover { background:var(--field-hover-bg); }
.mosaic-avatar-options button[aria-pressed="true"] { background:color-mix(in srgb,var(--avatar-selection) 18%,var(--paper)); }
.mosaic-avatar-selected { position:absolute;top:3px;right:3px;display:grid;place-items:center;width:16px;height:16px;border-radius:50%;background:var(--paper);color:var(--ink);font-size:12px;line-height:1; }
.mosaic-avatar-dialog button:focus-visible,.mosaic-avatar-dialog input:focus-visible { outline:2px solid var(--accent);outline-offset:2px; }
.mosaic-avatar-colors { grid-template-columns:repeat(6,minmax(0,1fr)); }
.mosaic-avatar-colors button { min-height:36px; }
.mosaic-avatar-swatch { width:24px;height:24px;border-radius:50%;background:var(--swatch);box-shadow:inset 0 0 0 1px #00000020; }
.mosaic-avatar-actions { display:flex;gap:var(--space-2);margin-top:auto; }
.mosaic-avatar-actions > button { flex:1;border-radius:999px;min-height:42px; }
.mosaic-avatar-reset { align-self:flex-start; }
@media(max-width:720px) { .mosaic-avatar-dialog { grid-template-columns:minmax(0,1fr);gap:24px;padding:24px; } .mosaic-avatar-summary { grid-row:2;display:grid;grid-template-columns:minmax(0,1fr) 144px;align-items:center;gap:16px; } .mosaic-avatar-preview { grid-column:2;grid-row:1/3;min-height:144px; } .mosaic-avatar-actions { grid-column:1; } .mosaic-avatar-summary .mosaic-name-error { grid-column:1/-1; } .mosaic-avatar-controls { gap:20px; } }
@media(max-width:480px) { .mosaic-avatar-summary { display:flex;align-items:stretch; } }
`;

export function createIdentityDialog({ react: React, ui: { Avatar, Button } }) {
  const h = React.createElement;
  return function IdentityDialog({ agent, locale, onSave, onClose }) {
    const zh = locale.startsWith("zh"), label = (en, cn) => zh ? cn : en;
    const [draftKey, setKey] = React.useState(null), [draftName, setName] = React.useState(null);
    const key = draftKey ?? agent.avatar_key ?? DEFAULT_PERSONAL_AVATAR, name = draftName ?? agent.name;
    const trimmed = name.trim(), valid = trimmed.length > 0 && Array.from(trimmed).length <= 64;
    const [saving, setSaving] = React.useState(false), [error, setError] = React.useState("");
    const dialog = React.useRef(null), pending = React.useRef(false), alive = React.useRef(true);
    const [, shape, accessory, rawHue] = key.split(":"), hue = Number(rawHue);
    const nameChanged = trimmed !== agent.name, avatarChanged = key !== (agent.avatar_key || DEFAULT_PERSONAL_AVATAR);
    const changed = nameChanged || avatarChanged;
    React.useEffect(() => { alive.current = true; dialog.current?.focus(); return () => { alive.current = false; }; }, []);
    const choose = (nextShape, nextAccessory, nextHue) => { setKey(`mascot-v1:${nextShape}:${nextAccessory}:${nextHue}`); setError(""); };
    const save = async event => {
      event.preventDefault();
      if (pending.current || !valid || !changed) return;
      pending.current = true; setSaving(true); setError(""); dialog.current?.focus();
      try { await onSave({ ...(nameChanged ? { name: trimmed } : {}), ...(avatarChanged ? { avatar_key: key } : {}) }); }
      catch (cause) { if (alive.current) setError(`${label("Could not confirm your changes were saved. Try again.", "未能确认修改已保存，请重试。")} ${cause instanceof Error ? cause.message : String(cause)}`); }
      finally { pending.current = false; if (alive.current) setSaving(false); }
    };
    const selectedMark = h("span", { className: "mosaic-avatar-selected", "aria-hidden": true }, "✓");
    const shapes = { round: label("Round", "圆形"), "rounded-square": label("Rounded square", "圆角方形"), capsule: label("Capsule", "胶囊"), triangle: label("Triangle", "三角"), diamond: label("Diamond", "菱形") };
    const accessories = { none: label("None", "无配饰"), beanie: label("Beanie", "毛线帽"), "hard-hat": label("Hard hat", "安全帽"), headset: label("Headset", "耳机"), leaf: label("Leaf", "叶子") };
    return h("div", { className: "mosaic-name-backdrop", onPointerDown: event => { if (!pending.current && event.target === event.currentTarget) onClose(); } },
      h("form", { ref: dialog, className: "mosaic-avatar-dialog", style: { "--avatar-selection": `hsl(${hue} 65% 65%)` }, tabIndex: -1, role: "dialog", "aria-modal": true, "aria-label": label("Customize your twin", "自定义你的 twin"), "aria-busy": saving, onSubmit: save, onKeyDown: event => {
        if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); if (!pending.current) onClose(); }
        if (event.key === "Tab") {
          const controls = [...dialog.current.querySelectorAll("button:not(:disabled),input:not(:disabled)")], first = controls[0], last = controls.at(-1);
          if (!first) { event.preventDefault(); dialog.current.focus(); }
          else if (event.shiftKey && (document.activeElement === first || document.activeElement === dialog.current)) { event.preventDefault(); last.focus(); }
          else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
        }
      } },
      h("button", { type: "button", className: "mosaic-name-close", disabled: saving, "aria-label": label("Close", "关闭"), onClick: onClose }, "×"),
      h("h2", null, label("Customize your twin", "自定义你的 twin")),
      h("div", { className: "mosaic-avatar-controls" },
      h("fieldset", { disabled: saving }, h("legend", null, label("Shape", "形状")), h("div", { className: "mosaic-avatar-options" }, PERSONAL_AVATAR_SHAPES.map(value => h("button", { key: value, type: "button", "data-avatar-shape": value, "aria-label": shapes[value], title: shapes[value], "aria-pressed": shape === value, onClick: () => choose(value, accessory, hue) }, h(Avatar, { size: 40, avatarKey: `mascot-v1:${value}:${accessory}:${hue}` }), shape === value && selectedMark)))),
      h("fieldset", { disabled: saving }, h("legend", null, label("Accessory", "配饰")), h("div", { className: "mosaic-avatar-options" }, PERSONAL_AVATAR_ACCESSORIES.map(value => h("button", { key: value, type: "button", "data-avatar-accessory": value, "aria-label": accessories[value], title: accessories[value], "aria-pressed": accessory === value, onClick: () => choose(shape, value, hue) }, h(Avatar, { size: 40, avatarKey: `mascot-v1:${shape}:${value}:${hue}` }), accessory === value && selectedMark)))),
      h("fieldset", { disabled: saving }, h("legend", null, label("Color", "颜色")), h("div", { className: "mosaic-avatar-options mosaic-avatar-colors" }, PERSONAL_AVATAR_HUES.map(value => h("button", { key: value, type: "button", "data-avatar-hue": value, "aria-label": `${label("Hue", "色相")} ${value}°`, "aria-pressed": hue === value, onClick: () => choose(shape, accessory, value) }, h("span", { className: "mosaic-avatar-swatch", style: { "--swatch": `hsl(${value} 65% 65%)` } }), hue === value && selectedMark)))),
      h(Button, { type: "button", variant: "ghost", className: "mosaic-avatar-reset", disabled: saving || key === DEFAULT_PERSONAL_AVATAR, "data-action": "reset-avatar", onClick: () => { setKey(DEFAULT_PERSONAL_AVATAR); setError(""); } }, label("Reset appearance", "恢复默认外观"))),
      h("div", { className: "mosaic-avatar-summary" },
        h("input", { className: "mosaic-name-input", "aria-label": label("Name", "名字"), value: name, disabled: saving, "aria-invalid": !valid, onChange: event => { setName(event.target.value); setError(""); } }),
        !valid && h("p", { className: "mosaic-name-error", role: "status" }, label("Use 1–64 characters.", "请输入 1–64 个字符。")),
        h("div", { className: "mosaic-avatar-preview" }, h(Avatar, { size: 144, avatarKey: key, motion: "ambient" })),
        error && h("p", { className: "mosaic-name-error", role: "alert" }, error),
        h("div", { className: "mosaic-avatar-actions" },
          h(Button, { type: "submit", variant: "primary", disabled: saving || !valid || !changed, "data-action": "save-avatar" }, saving ? label("Saving…", "保存中…") : label("Save", "保存"))))));
  };
}
