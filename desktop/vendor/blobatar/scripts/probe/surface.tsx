import { createRoot } from "react-dom/client";
import { useState } from "react";
import { SurfaceWorkbench } from "../../demo/SurfaceWorkbench";
import { WuuMascot } from "../../../../src/renderer/WuuMascot";
import "../../src/motion.css";

function App() {
  const [activity, setActivity] = useState<"thinking" | "edit">("thinking");
  return <main>
    <h1>Wuu · 曲面造型</h1>
    <SurfaceWorkbench />
    <section id="product">
      <button onClick={() => setActivity((value) => value === "thinking" ? "edit" : "thinking")}>切换活动</button>
      <WuuMascot activity={activity} accessory="cap" size={150} followPointer />
    </section>
  </main>;
}
createRoot(document.getElementById("root")!).render(<App />);
