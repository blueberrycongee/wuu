// Real-browser verification of the shipped offline renderer, without an account or host.
const { app, BrowserWindow } = require("electron");
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const output = path.resolve(process.argv[2] || "/tmp/wuu-mermaid-evidence");
app.whenReady().then(async () => {
  await fs.mkdir(output, { recursive: true });
  const win = new BrowserWindow({ width: 390, height: 720, show: false, webPreferences: { nodeIntegration: false, contextIsolation: true } });
  const failures = [];
  win.webContents.on("console-message", (_event, _level, message) => {
    if (message.includes("TypeError") || message.includes("ReferenceError")) failures.push(message);
  });
  const evaluate = code => win.webContents.executeJavaScript(code);
  const update = input => evaluate(`window.renderWuuMermaid(${JSON.stringify({ dark: false, fontSize: 15, ...input })})`);
  const wait = async (expression) => {
    const end = Date.now() + 15000;
    while (Date.now() < end) {
      if (await evaluate(expression)) return;
      await new Promise(resolve => setTimeout(resolve, 25));
    }
    throw new Error(`Timed out: ${expression}`);
  };
  const screenshot = async name => fs.writeFile(path.join(output, name), (await win.webContents.capturePage()).toPNG());
  await win.loadFile(path.join(__dirname, "NativeUI/mermaid.html"));
  await evaluate(`window.heights=[];window.WuuDiagram={height:h=>heights.push(h)};window.frames=[];
    new MutationObserver(()=>{if(document.querySelector('#diagram svg'))frames.push(document.querySelector('#diagram').textContent)}).observe(document.querySelector('#diagram'),{childList:true});`);
  let code = "flowchart TD\n  A[收到消息] --> B[绘制图表]\n";
  await update({ code, live: true });
  await wait("document.querySelector('#diagram svg')");
  // Deltas never pause long enough for a 300ms debounce. Frames must still advance.
  const stream = "  B --> C{语法完整？}\n  C -->|是| D[更新图表]\n  C -->|否| E[保留上一帧]\n";
  for (let index = 0; index < stream.length; index += 2) {
    code += stream.slice(index, index + 2);
    await update({ code, live: true });
    await new Promise(resolve => setTimeout(resolve, 40));
  }
  await wait("document.querySelector('#diagram').textContent.includes('保留上一帧')");
  assert.ok(await evaluate("frames.length >= 3"), "continuous streaming must produce intermediate diagrams");
  // Native parsers normalize newlines. A completed new line must appear even
  // when every snapshot also contains the next, unfinished statement.
  code += "  E --> F[完成行]\n";
  await update({ code: code + "  F --> G[", live: true });
  await wait("document.querySelector('#diagram').textContent.includes('完成行')");
  const before = await evaluate("document.querySelector('#diagram svg').outerHTML");
  await update({ code: code + "  F --> G[still", live: true });
  await new Promise(resolve => setTimeout(resolve, 650));
  assert.equal(await evaluate("document.querySelector('#diagram svg').outerHTML"), before, "partial syntax keeps last valid SVG");
  await update({ code: code + "  F --> G[still", live: false });
  await wait("document.querySelector('#diagram').dataset.state === 'error'");
  assert.ok(await evaluate("document.querySelector('#diagram pre').textContent.endsWith('G[still')"));
  await update({ code, live: false });
  await wait("document.querySelector('#diagram svg')");
  await screenshot("renderer-light.png");
  const light = await evaluate("document.querySelector('#diagram svg').outerHTML");
  win.setSize(320, 720);
  await update({ code, live: false, dark: true, fontSize: 23 });
  await wait(`document.querySelector('#diagram svg').outerHTML !== ${JSON.stringify(light)}`);
  await screenshot("renderer-dark-large.png");
  assert.ok(await evaluate("heights.length > 0 && heights.every(h=>h>=64 && h<=600)"));
  await update({ code: "sequenceDiagram\nAlice->>Bob: Hello\nBob-->>Alice: Done", live: false });
  await wait("document.querySelector('#diagram').textContent.includes('Done')");
  await update({ code: "pie title Budget\n\"Used\" : 30\n\"Free\" : 70", live: false });
  await wait("document.querySelector('#diagram').textContent.includes('Budget')");
  await update({ code: "<script>window.compromised=true</script>", live: false });
  await wait("document.querySelector('#diagram').dataset.state === 'error'");
  assert.ok(await evaluate("!window.compromised && !document.querySelector('#diagram script')"));
  assert.equal(await evaluate("fetch('https://example.com/').then(()=>false,()=>true)"), true, "CSP must block network access");
  assert.deepEqual(failures, []);
  const report = { result: "passed", checks: ["continuous streaming", "partial syntax", "final invalid source", "replacement", "dark/large/narrow", "height bridge", "sequence and pie diagrams", "inert source", "offline CSP"], frames: await evaluate("frames.length") };
  await fs.writeFile(path.join(output, "renderer-result.json"), JSON.stringify(report, null, 2) + "\n");
  console.log(JSON.stringify(report));
  win.destroy(); app.quit();
}).catch(error => { console.error(error); app.exit(1); });
