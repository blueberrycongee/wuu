import { useState } from "react";
import { createRoot } from "react-dom/client";
import { WuuMascot, WuuMascotRuntimeProvider, WUU_MASCOT_ACCESSORIES, type WuuMascotActivity } from "../../src/renderer/WuuMascot";
import { WUU_MASCOT_ACTIVITY_PERSPECTIVES } from "../../src/renderer/wuu-mascot-spec";
import { WuuIconMascot } from "../../src/renderer/WuuIconMascot";
import { OnboardingMascotStage } from "../../src/renderer/OnboardingMascotStage";
import { ONBOARDING_PLUGIN_ORDER } from "../../src/renderer/onboardingCatalog";
import "../../src/renderer/styles/onboarding.css";
import { AgentAvatarMark, AGENT_AVATAR_SHAPES, serializeAgentAvatarConfig } from "../../src/renderer/AgentAvatarMark";
import "./preview.css";

function Preview(): JSX.Element {
  const [activity, setActivity] = useState<WuuMascotActivity>("idle");
  const [provider, setProvider] = useState("openai");
  const [model, setModel] = useState("gpt-5");
  const [visible, setVisible] = useState(true);
  const [dark, setDark] = useState(false);
  return <main data-theme={dark ? "dark" : "light"}>
    <header>
      <label>Provider <select value={provider} onChange={e => setProvider(e.target.value)}>{["openai", "anthropic", "google"].map(p => <option key={p}>{p}</option>)}</select></label>
      <label>Model <select value={model} onChange={e => setModel(e.target.value)}>{["gpt-5", "claude-sonnet-4", "gemini-2.5-pro"].map(m => <option key={m}>{m}</option>)}</select></label>
      <label>State <select value={activity} onChange={e => setActivity(e.target.value as WuuMascotActivity)}>{Object.keys(WUU_MASCOT_ACTIVITY_PERSPECTIVES).map(s => <option key={s}>{s}</option>)}</select></label>
      <label><input type="checkbox" checked={visible} onChange={e => setVisible(e.target.checked)} /> Visible</label>
      <label><input type="checkbox" checked={dark} onChange={e => setDark(e.target.checked)} /> Dark</label>
    </header>
    <WuuMascotRuntimeProvider provider={provider} providers={["openai", "anthropic", "google"]} model={model}>
      <section className="runtime-preview" aria-label="Runtime identity">
        <WuuMascot size={140} activity={activity} visible={visible} ambient followPointer />
        <WuuMascot size={64} activity={activity} visible={visible} />
        <WuuMascot size={28} activity={activity} visible={visible} />
        <WuuIconMascot className="icon-preview" />
      </section>
      <section className="accessories" aria-label="Accessories">
        {WUU_MASCOT_ACCESSORIES.map(accessory => <figure key={accessory}>
          <div className="samples"><WuuMascot size={80} accessory={accessory} activity={activity} /><WuuMascot size={28} accessory={accessory} activity={activity} /></div>
          <figcaption>{accessory}</figcaption>
        </figure>)}
      </section>
      <section className="shapes" aria-label="Agent shapes">
        {AGENT_AVATAR_SHAPES.map(shape => <figure key={shape.id}><AgentAvatarMark seed={shape.id} avatarKey={serializeAgentAvatarConfig({ shape: shape.id, accessory: "headphones", hue: 202 })} /><figcaption>{shape.id}</figcaption></figure>)}
      </section>
      <section aria-label="Onboarding equipment">
        <OnboardingMascotStage pluginIDs={ONBOARDING_PLUGIN_ORDER} engineID="claude" />
      </section>
    </WuuMascotRuntimeProvider>
  </main>;
}

createRoot(document.getElementById("root")!).render(<Preview />);
