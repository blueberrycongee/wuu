import { useState } from "react";
import { createRoot } from "react-dom/client";
import { flushSync } from "react-dom";
import { MarkdownContent } from "../../src/renderer/RichContent";
import { ImagePreviewProvider } from "../../src/renderer/ImagePreview";
import "../../src/renderer/styles.css";
import cases from "./cases.json";

const fixtureCases = [...cases, {
  name: "flowchart-100-nodes",
  code: "flowchart TB\n" + Array.from({ length: 99 }, (_, i) => `N${i}[步骤 ${i}] --> N${i + 1}[步骤 ${i + 1}]`).join("\n"),
}];

function Fixture(): JSX.Element {
  const [source, setSource] = useState(fixtureCases[0].code);
  const [live, setLive] = useState(false);
  Object.assign(window, {
    diagramFixture: {
      cases: fixtureCases,
      show(code: string, streaming = false) {
        flushSync(() => { setSource(code); setLive(streaming); });
      },
      async benchmark() {
        // Both engines run in the same Chromium document with warm fonts and
        // identical sources. Include SVG insertion and forced layout, not paint.
        const { render } = await import("pele");
        const { default: mermaid } = await import("mermaid");
        await document.fonts.ready;
        const stage = document.createElement("div");
        stage.style.cssText = "position:absolute;left:-10000px;width:1000px;font:15px system-ui";
        document.body.append(stage);
        const results = [];
        let sequence = 0;
        for (const sample of fixtureCases.filter(sample => sample.name !== "inert-source")) {
          const timings: Record<string, number[]> = { pele: [], mermaid: [] };
          const errors: Record<string, string> = {};
          // Alternate engine order so neither always benefits from running first.
          for (let iteration = 0; iteration < 13; iteration++) {
            for (const engine of iteration % 2 ? ["pele", "mermaid"] : ["mermaid", "pele"]) {
              if (errors[engine]) continue;
              try {
                const start = performance.now();
                let svg: string;
                if (engine === "pele") {
                  svg = render(sample.code, { fontFamily: "system-ui", fontSize: 15, links: false, images: false, autoDirection: false }).svg;
                } else {
                  mermaid.initialize({ startOnLoad: false, securityLevel: "strict", theme: "base", themeVariables: { fontFamily: "system-ui", fontSize: "15px" } });
                  svg = (await mermaid.render(`baseline-${++sequence}`, sample.code)).svg;
                }
                stage.innerHTML = svg;
                stage.querySelector("svg")!.getBoundingClientRect();
                if (iteration >= 3) timings[engine].push(performance.now() - start);
              } catch (error) { errors[engine] = String(error); }
              stage.replaceChildren();
            }
          }
          results.push({ name: sample.name, timings, errors });
        }
        stage.remove();
        return results;
      },
    },
  });
  return <ImagePreviewProvider>
    <main style={{ width: "100%", maxWidth: 960, padding: 24, margin: "auto", boxSizing: "border-box" }}>
      <label>Diagram <select value={source} onChange={event => setSource(event.target.value)}>
        {fixtureCases.map(sample => <option key={sample.name} value={sample.code}>{sample.name}</option>)}
      </select></label>
      <div style={{ marginTop: 24 }}><MarkdownContent text={`\`\`\`mermaid\n${source}${live ? "" : "\n```"}`} mermaidStreaming={live} /></div>
    </main>
  </ImagePreviewProvider>;
}

createRoot(document.getElementById("root")!).render(<Fixture />);
