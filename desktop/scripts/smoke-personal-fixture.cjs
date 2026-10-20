// DOM execution evidence only. jsdom has no layout or pixel renderer.
const fs = require("node:fs");
const path = require("node:path");
const { webcrypto } = require("node:crypto");
const { JSDOM, VirtualConsole } = require("jsdom");
const target = path.resolve(__dirname, "../out/personal-fixture/index.html");
const errors = [], revoked = [], rendererLimitations = [];
const console = new VirtualConsole();
console.on("jsdomError", error => { if (String(error).includes("HTMLCanvasElement.prototype.getContext")) rendererLimitations.push(String(error)); else if (error.type !== "css parsing") errors.push(String(error)); });
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
  await waitFor(() => [...document.querySelectorAll(".primary-nav button")].some(button => button.textContent.trim() === "twin"));
  const navigation = [...document.querySelectorAll(".primary-nav button")].map(button => button.textContent.trim());
  const twin = [...document.querySelectorAll(".primary-nav button")].find(button => button.textContent.trim() === "twin");
  document.querySelector('[data-wuu-plugin="personal"]').click();
  await waitFor(() => document.querySelector('[data-action="load-attachment"]'));
  if (document.body.textContent.includes("合成日程：周五午餐")) throw new Error("Default preview must not display a synthetic calendar confirmation notice");
  if (document.querySelector(".plugin-conversation-questions [role=alert]")) throw new Error("Conversation question fixture returned an invalid response");
  const mainName = document.querySelector(".plugin-conversation-titlebar h1").textContent;
  document.querySelector('[data-action="load-attachment"]').click();
  await waitFor(() => document.querySelector('a[download]'));
  const fileDownload = document.querySelector('a[download]').getAttribute("href");
  const conversation = document.querySelector('.plugin-conversation');
  const conversationId = conversation.dataset.conversationId;
  const textarea = document.querySelector('.mosaic textarea');
  Object.getOwnPropertyDescriptor(dom.window.HTMLTextAreaElement.prototype, "value").set.call(textarea, "保留这条草稿");
  textarea.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
  await waitFor(() => document.querySelector('.mosaic .composer-send-button'));
  document.querySelector('[data-task-id="trip"]').click();
  await waitFor(() => document.querySelector('[data-task-detail="trip"]'));
  if(document.querySelector('.plugin-conversation') !== conversation) throw new Error("Task detail replaced the conversation");
  document.querySelector('[data-action="close-detail"]').click();
  await waitFor(() => document.querySelector('[data-output-task-id="reading"]'));
  document.querySelector('[data-output-task-id="reading"]').click();
  await waitFor(() => document.querySelector('[data-task-detail="reading"]'));
  if(document.querySelector('.plugin-conversation') !== conversation) throw new Error("Output detail replaced the conversation");
  document.querySelector('[data-action="close-detail"]').click();
  document.querySelector('[data-wuu-plugin="automations"]').click();
  await waitFor(() => !document.querySelector('.mosaic textarea'));
  document.querySelector('[data-wuu-plugin="personal"]').click();
  await waitFor(() => document.querySelector('.mosaic textarea')?.value === "保留这条草稿");
  if(document.querySelector('.plugin-conversation').dataset.conversationId !== conversationId) throw new Error("Route changed main identity");
  const sections = [...document.querySelectorAll('[data-panel-section]')].map(element => element.dataset.panelSection);
  if(JSON.stringify(sections) !== JSON.stringify(['ongoing','recent','outputs'])) throw new Error("Unexpected continuous panel order");
  const result = { kind:"synthetic DOM execution only", visualAcceptance:false, realHost:false, realCalendar:false, navigation, mainName, directEntry:true, restoredMainDraft:true, sameMainSession:true, continuousSections:sections, nativeComposer:!!document.querySelector('.mosaic .split-composer'), fileDownload, scopedBlobRevoked:revoked.includes(fileDownload), runtimeErrors:errors, rendererLimitations };
  fs.writeFileSync(path.join(path.dirname(target), "dom-smoke.json"), JSON.stringify(result,null,2)+"\n");
  if(errors.length || navigation.indexOf("twin") !== navigation.indexOf("Automations") + 1 || !result.scopedBlobRevoked) throw new Error(JSON.stringify(result));
  process.stdout.write(JSON.stringify(result,null,2)+"\n");
})().catch(error => { fs.writeFileSync(path.join(path.dirname(target), "dom-smoke.json"), JSON.stringify({kind:"synthetic DOM execution only", visualAcceptance:false, passed:false, error:String(error), runtimeErrors:errors},null,2)+"\n"); process.stderr.write(String(error.stack || error)+"\n");process.exitCode=1; }).finally(() => dom.window.close());
