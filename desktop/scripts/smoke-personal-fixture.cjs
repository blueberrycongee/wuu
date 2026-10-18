// DOM execution evidence only. jsdom has no layout or pixel renderer.
const fs = require("node:fs");
const path = require("node:path");
const { webcrypto } = require("node:crypto");
const { JSDOM, VirtualConsole } = require("jsdom");
const target = path.resolve(__dirname, "../out/personal-fixture/index.html");
const errors = [], revoked = [];
const console = new VirtualConsole();
console.on("jsdomError", error => { if (error.type !== "css parsing") errors.push(String(error)); });
console.on("error", error => errors.push(String(error)));
const dom = new JSDOM(fs.readFileSync(target, "utf8"), {
  url: "https://synthetic-fixture.invalid/", runScripts: "dangerously", pretendToBeVisual: true, virtualConsole: console,
  beforeParse(window) {
    window.matchMedia = query => ({ matches:false, media:query, addEventListener(){}, removeEventListener(){} });
    window.ResizeObserver = class { observe(){} unobserve(){} disconnect(){} };
    window.structuredClone = structuredClone;
    window.TextEncoder = TextEncoder; window.TextDecoder = TextDecoder;
    Object.defineProperty(window.crypto, "subtle", { value:{ digest:(algorithm, bytes) => webcrypto.subtle.digest(algorithm, new Uint8Array(bytes)) } });
    window.URL.createObjectURL = () => "blob:synthetic-dom-only";
    window.URL.revokeObjectURL = url => revoked.push(url);
  },
});
const document = dom.window.document;
function waitFor(predicate) {
  if (predicate()) return Promise.resolve();
  return new Promise((resolve,reject) => {
    const observer = new dom.window.MutationObserver(() => { if (predicate()) { clearTimeout(timer);observer.disconnect();resolve(); } });
    const timer = setTimeout(() => { observer.disconnect(); reject(new Error(`DOM fixture condition did not settle: ${predicate.toString()}; errors=${JSON.stringify(errors)}; text=${document.body.textContent.slice(0,700)}`)); }, 5000);
    observer.observe(document, { childList:true, subtree:true, attributes:true });
  });
}
(async () => {
  await waitFor(() => document.querySelector('[data-action="load-attachment"]'));
  document.querySelector('[data-action="load-attachment"]').click();
  await waitFor(() => document.querySelector('a[download]'));
  const fileDownload = document.querySelector('a[download]').getAttribute("href");
  [...document.querySelectorAll("button")].find(button => button.textContent === "Personal / 日历").click();
  await waitFor(() => document.querySelector('[data-operation-id]'));
  document.querySelector('[data-operation-id]').click();
  await waitFor(() => document.querySelector('[data-action="confirm"]'));
  const readOnlyGate = document.querySelector('[data-action="confirm"]').disabled;
  [...document.querySelectorAll("button")].find(button => button.textContent === "合成写入开关").click();
  document.querySelector('[data-action="refresh"]').click();
  await waitFor(() => !document.querySelector('[data-action="review-preview"]').disabled);
  await new Promise(resolve => dom.window.requestAnimationFrame(resolve));
  document.querySelector('[data-action="review-preview"]').click();
  await waitFor(() => !document.querySelector('[data-action="confirm"]').disabled);
  document.querySelector('[data-action="confirm"]').click();
  await waitFor(() => document.querySelector('[data-operation-status="uncertain"]'));
  const result = { kind:"synthetic DOM execution only", visualAcceptance:false, realHost:false, realCalendar:false, fileDownload, scopedBlobRevoked:revoked.includes(fileDownload), readOnlyGate, uncertainOutcomeVisible:true, runtimeErrors:errors };
  fs.writeFileSync(path.join(path.dirname(target), "dom-smoke.json"), JSON.stringify(result,null,2)+"\n");
  if(errors.length || !readOnlyGate || !result.scopedBlobRevoked) throw new Error(JSON.stringify(result));
  process.stdout.write(JSON.stringify(result,null,2)+"\n");
})().catch(error => { fs.writeFileSync(path.join(path.dirname(target), "dom-smoke.json"), JSON.stringify({kind:"synthetic DOM execution only", visualAcceptance:false, passed:false, error:String(error), runtimeErrors:errors},null,2)+"\n"); process.stderr.write(String(error)+"\n");process.exitCode=1; }).finally(() => dom.window.close());
