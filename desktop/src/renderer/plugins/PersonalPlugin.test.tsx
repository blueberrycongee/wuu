import { webcrypto } from "node:crypto";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import * as React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { PluginHost, type PluginGenerationApi, type PluginHostOptions } from "./PluginHost";
import { PluginViewContent, WorkbenchController } from "./Workbench";
const source = readFileSync(resolve(process.cwd(), "../internal/plugin/bundled/personal/desktop.js"), "utf8");
const activate = Function(source.replace("export async function activate(api)", "return async function activate(api)"))() as (api: PluginGenerationApi) => Promise<void>;
const main = { agent_id: "a-main", user_space_id: "local", name: "Mosaic", kind: "main", main_session_id: "s-private", status: "active", revision: 2 };
const specialist = { ...main, agent_id: "a-travel", name: "Lin", kind: "specialist", responsibility: "Travel", main_session_id: "s-travel" };
const cleanups: Array<() => void> = [];
afterEach(() => { cleanups.splice(0).forEach(fn => fn()); });
async function mount(mutate?: (method: string, input: any) => Promise<unknown>, mainAgent: typeof main & Record<string, unknown> = main, historyRecords?: unknown[], fixtures: { tasks?: unknown[]; memories?: unknown[] } = {}) {
  const invoke = vi.fn<NonNullable<PluginHostOptions["invokeRuntime"]>>(async ({ method, input }) => {
    if (method === "agents.list") return { agents: [mainAgent, specialist], default_agent_id: mainAgent.agent_id };
    if (method === "agents.open") return { agent: (input as any).agent_id === specialist.agent_id ? specialist : mainAgent, session: { state: "completed" } };
    if (method === "agents.history") return { records: historyRecords ?? [{ seq: 1, role: "assistant", content: `Hello ${(input as any).agent_id}` }] };
    if (method === "agents.proposals") return { proposals: [] };
    if (method === "tasks.list") return { tasks: fixtures.tasks ?? [{ task_id: "t1", agent_id: "a-travel", title: "Plan a trip", goal: "Find a route", state: "running", revision: 3, requirements_revision: 1 }] };
    if (method === "memory.query") return { records: fixtures.memories ?? [] };
    if (method === "settings.get") return { settings: { revision: 1, notifications: true, quiet_hours: { enabled: false, start: "22:00", end: "08:00", timezone: "UTC" } } };
    return mutate?.(method, input) ?? {};
  });
  const host = new PluginHost({ react: React, invokeRuntime: invoke });
  await host.activateGeneration({ pluginId: "personal", generation: "one", register: activate });
  const controller = new WorkbenchController(host);
  const container = document.createElement("div"); document.body.append(container);
  const root = createRoot(container);
  cleanups.push(() => { act(() => root.unmount()); controller.dispose(); host.unload("personal"); container.remove(); });
  await act(async () => root.render(<PluginViewContent controller={controller} pluginId="personal" viewTypeId="personal.home" />));
  const click = async (selector: string) => { const button = container.querySelector<HTMLButtonElement>(selector); expect(button).not.toBeNull(); await act(async () => button!.click()); };
  const edit = async (value: string) => { const node = container.querySelector("textarea")!; await act(async () => { Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(node, value); node.dispatchEvent(new Event("input", { bubbles: true })); }); };
  return { container, invoke, click, edit, publish: () => host.publishHostEvent({ type: "routine-state-changed" }) };
}
it("opens by stable agent identity without exposing session management or a tag on the main agent", async () => {
  const ui = await mount();
  expect(ui.container.querySelector('[data-agent-id="a-main"] [data-responsibility]')).toBeNull();
  expect(ui.container.querySelector('[data-agent-id="a-travel"] [data-responsibility]')?.textContent).toBe("Travel");
  await ui.click('[data-agent-id="a-travel"]');
  expect(ui.container.textContent).toContain("Hello a-travel");
  expect(ui.invoke).toHaveBeenCalledWith(expect.objectContaining({ method: "agents.open", input: { agent_id: "a-travel" }, userSpaceId: "local", productDomain: "personal" }));
  expect(ui.invoke.mock.calls.every(([request]) => !request.workspaceId && !request.workspaceRoot)).toBe(true);
  expect(ui.container.textContent).not.toContain("s-private");
});
it("preserves unsent text when sending fails and never substitutes a session ID for an agent", async () => {
  const ui = await mount(async method => { if (method === "agents.send") throw new Error("Host offline"); return {}; });
  await ui.edit("Remember this"); await ui.click('[data-action="send"]');
  expect(ui.container.querySelector("textarea")!.value).toBe("Remember this");
  expect(ui.container.querySelector('[role="alert"]')?.textContent).toContain("Host offline");
  expect(ui.invoke).toHaveBeenCalledWith(expect.objectContaining({ method: "agents.send", input: expect.objectContaining({ agent_id: "a-main", prompt: "Remember this", request_id: expect.any(String) }) }));
});
it("stops a task with its reviewed revision without stopping the agent's current reply", async () => {
  const ui = await mount(); await ui.click('[data-section="tasks"]'); await ui.click('[data-task-id="t1"]'); await ui.click('[data-action="stop-task"]');
  expect(ui.invoke).toHaveBeenCalledWith(expect.objectContaining({ method: "tasks.stop", input: { task_id: "t1", expected_revision: 3 } }));
  expect(ui.invoke.mock.calls.some(([request]) => request.method === "agents.stop_reply")).toBe(false);
});
it("keeps an uncertain agent cancellation visible and blocks new work until the host settles it", async () => {
  const ui = await mount(undefined, { ...main, status: "stopping", stop_target: "paused", cancellation_pending: true, cancellation_error: "Cancellation acknowledgement lost" });
  expect(ui.container.querySelector<HTMLTextAreaElement>("textarea")?.disabled).toBe(true);
  expect(ui.container.querySelector('[data-agent-state="stopping"]')).not.toBeNull();
  expect(ui.container.querySelector('[role="alert"]')?.textContent).toContain("Cancellation acknowledgement lost");
  await ui.click('[data-section="settings"]');
  const resume = ui.container.querySelector<HTMLButtonElement>('[data-action="resume-agent"]');
  expect(resume === null || resume.disabled).toBe(true);
  expect(ui.container.querySelector('[data-agent-id="a-main"]')).not.toBeNull();
});

const fileDigest = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824";
async function chooseFiles(container: HTMLElement, files: File[]) {
  const input = container.querySelector<HTMLInputElement>('input[type="file"]'); expect(input).not.toBeNull();
  await act(async () => { Object.defineProperty(input!, "files", { configurable: true, value: files }); input!.dispatchEvent(new Event("change", { bubbles: true })); });
}
function attachmentBrowser() {
  Object.defineProperty(crypto, "subtle", { configurable: true, value: webcrypto.subtle });
  const create = vi.fn(() => "blob:verified-scoped-file"), revoke = vi.fn();
  Object.defineProperty(URL, "createObjectURL", { configurable: true, value: create });
  Object.defineProperty(URL, "revokeObjectURL", { configurable: true, value: revoke }); return { create, revoke };
}
it("keeps attachment-only failed sends retryable with exact proof bytes and references only in plugin input", async () => {
  attachmentBrowser(); const ui = await mount(async method => { if (method === "agents.send") throw new Error("Offline after admission"); });
  await chooseFiles(ui.container, [new File(["hello"], "notes.txt", { type: "text/plain" })]);
  await vi.waitFor(async () => { await act(async () => {}); expect(ui.container.querySelector('[data-draft-attachment]')).not.toBeNull(); });
  await ui.click('[data-action="send"]'); await ui.click('[data-agent-id="a-travel"]'); await ui.edit("Another request"); await ui.click('[data-action="send"]'); await ui.click('[data-agent-id="a-main"]'); await ui.click('[data-action="send"]');
  const sends = ui.invoke.mock.calls.map(([request]) => request).filter(request => request.method === "agents.send" && (request.input as any).agent_id === "a-main"); expect(sends).toHaveLength(2);
  expect(sends[0].input).toEqual({ agent_id: "a-main", request_id: expect.any(String), prompt: "", attachments: [{ sha256: fileDigest, filename: "notes.txt", media_type: "text/plain" }] });
  expect(sends[0].userInput).toEqual({ requestId: (sends[0].input as any).request_id, prompt: "", attachments: [{ sha256: fileDigest, filename: "notes.txt", media_type: "text/plain", data: "aGVsbG8=" }] });
  expect(sends[1].input).toEqual(sends[0].input); expect(sends[1].userInput).toEqual(sends[0].userInput);
  expect(ui.container.querySelector('[data-draft-attachment]')?.textContent).toContain("notes.txt");
});
it("rejects oversized or too-many file selections before admitting attachment bytes", async () => {
  attachmentBrowser(); const ui = await mount();
  await chooseFiles(ui.container, [new File([new Uint8Array(20 * 1024 * 1024 + 1)], "oversized.bin")]);
  expect(ui.container.querySelector('[role="alert"]')).not.toBeNull(); expect(ui.container.querySelector('[data-draft-attachment]')).toBeNull();
  await chooseFiles(ui.container, Array.from({ length: 17 }, (_, i) => new File(["x"], `file${i}.txt`)));
  expect(ui.container.querySelector('[data-draft-attachment]')).toBeNull(); expect(ui.invoke.mock.calls.some(([request]) => request.method === "agents.send")).toBe(false);
});
it("downloads artifacts only through agent-scoped verified chunks and revokes the Blob on navigation", async () => {
  const browser = attachmentBrowser();
  const ui = await mount(async (method, input) => method === "agents.attachments.read" ? { data: input.offset === 0 ? "aGVs" : "bG8=", offset: input.offset, total: 8, content_type: "image/svg+xml", name: "diagram.svg", sha256: fileDigest } : {}, main,
    [{ seq: 7, role: "assistant", content: "Result", attachments: [{ kind: "artifact", index: 0, name: "diagram.svg", mime_type: "image/svg+xml", size: 5, sha256: fileDigest }] }]);
  await ui.click('[data-action="load-attachment"]');
  expect(ui.invoke).toHaveBeenCalledWith(expect.objectContaining({ method: "agents.attachments.read", input: { agent_id: "a-main", seq: 7, kind: "artifact", index: 0, sha256: fileDigest, offset: 4 } }));
  await vi.waitFor(async () => { await act(async () => {}); expect(ui.container.querySelector<HTMLAnchorElement>('a[download]')?.getAttribute("href")).toBe("blob:verified-scoped-file"); }); expect(ui.container.querySelector("img")).toBeNull();
  await ui.click('[data-agent-id="a-travel"]'); expect(browser.revoke).toHaveBeenCalledWith("blob:verified-scoped-file");
});
it("does not expose a Blob URL for corrupt attachment chunks", async () => {
  const browser = attachmentBrowser();
  const ui = await mount(async method => method === "agents.attachments.read" ? { data: "YmFk", offset: 0, total: 4, content_type: "text/plain", name: "notes.txt", sha256: fileDigest } : {}, main,
    [{ seq: 7, role: "user", attachments: [{ kind: "file", index: 0, name: "notes.txt", mime_type: "text/plain", size: 3, sha256: fileDigest }] }]);
  await ui.click('[data-action="load-attachment"]'); await vi.waitFor(async () => { await act(async () => {}); expect(ui.container.querySelector('[role="alert"]')).not.toBeNull(); }); expect(browser.create).not.toHaveBeenCalled(); expect(ui.container.querySelector('a[download]')).toBeNull(); expect(ui.container.querySelector('[role="alert"]')).not.toBeNull();
});

it("reads task result artifacts through the reviewed task execution rather than an agent session", async () => {
  attachmentBrowser();
  const ui = await mount(async (method, input) => {
    if (method === "tasks.history") return { records: [{ seq: 9, role: "tool", attachments: [{ kind: "artifact", index: 0, name: "result.txt", mime_type: "text/plain", size: 5, sha256: fileDigest }] }] };
    if (method === "tasks.attachments.read") return { data: "aGVsbG8=", offset: input.offset, total: 8, content_type: "text/plain", name: "result.txt", sha256: fileDigest };
    return {};
  });
  await ui.click('[data-section="tasks"]'); await ui.click('[data-task-id="t1"]');
  const trace = ui.container.querySelector<HTMLDetailsElement>('[data-task-trace]'); expect(trace).not.toBeNull();
  await act(async () => { trace!.open = true; trace!.dispatchEvent(new Event("toggle")); });
  await ui.click('[data-action="load-attachment"]');
  await vi.waitFor(async () => { await act(async () => {}); expect(ui.container.querySelector('a[download]')).not.toBeNull(); });
  expect(ui.invoke).toHaveBeenCalledWith(expect.objectContaining({ method: "tasks.attachments.read", input: { task_id: "t1", requirements_revision: 1, seq: 9, kind: "artifact", index: 0, sha256: fileDigest, offset: 0 } }));
  expect(ui.invoke.mock.calls.some(([request]) => request.method === "agents.attachments.read")).toBe(false);
});

it("refreshes a stopping routine after host events so settled cancellation can be resumed", async () => {
  let settled = false;
  const ui = await mount(async method => method === "routines.list" ? { routines: [{ routine_id:"routine-one",agent_id:"a-main",title:"Synthetic routine",status:settled?"paused":"stopping",cancellation_pending:!settled,revision:settled?3:2,binding:{} }] } : {});
  await ui.click('[data-section="today"]');
  const routine = () => Array.from(ui.container.querySelectorAll("article")).find(article => article.textContent?.includes("Synthetic routine"))!;
  expect(routine().querySelector("button")).toBeNull();
  settled = true; await act(async () => ui.publish());
  expect(routine().querySelector<HTMLButtonElement>("button")?.disabled).toBe(false);
});

async function changeField(container: HTMLElement, selector: string, value: string) {
  const node = container.querySelector<HTMLInputElement | HTMLSelectElement>(selector); expect(node).not.toBeNull();
  await act(async () => { Object.getOwnPropertyDescriptor(node!.tagName === "SELECT" ? HTMLSelectElement.prototype : HTMLInputElement.prototype,"value")!.set!.call(node,value); node!.dispatchEvent(new Event("input",{bubbles:true})); node!.dispatchEvent(new Event("change",{bubbles:true})); });
}
it("shows cached Today agenda with its stale state, source and read error instead of implying availability", async () => {
  const status = {state:"error",freshness:"stale",read_only:true,error_code:"cache_refresh_failed",account_id:"calendar@example.test",last_success_at:"2026-10-17T10:00:00Z",source:{calendar_id:"team-calendar",account_id:"calendar@example.test",fetched_at:"2026-10-17T10:00:00Z"}};
  const ui = await mount(async method => method === "calendar.status" ? status : method === "calendar.agenda" ? {...status,events:[{id:"event-one",summary:"Cached appointment",start:{date_time:new Date().toISOString(),timezone:"UTC"},end:{date_time:new Date(Date.now()+3600000).toISOString(),timezone:"UTC"}}]} : {});
  await ui.click('[data-section="today"]');
  expect(ui.invoke).toHaveBeenCalledWith(expect.objectContaining({method:"calendar.status",input:{agent_id:"a-main"},userSpaceId:"local",productDomain:"personal"}));
  expect(ui.invoke).toHaveBeenCalledWith(expect.objectContaining({method:"calendar.agenda",input:expect.objectContaining({agent_id:"a-main",calendar_id:"team-calendar",account_id:"calendar@example.test",time_min:expect.any(String),time_max:expect.any(String),timezone:expect.any(String)})}));
  expect(ui.container.querySelector('[data-calendar-freshness="stale"]')).not.toBeNull();
  expect(ui.container.querySelector('[data-calendar-event="event-one"]')?.textContent).toContain("Cached appointment");
  expect(ui.container.textContent).toContain("cache_refresh_failed"); expect(ui.container.textContent).toContain("calendar@example.test");
});
it("keeps Today tasks available when Calendar is unconnected or its service fails", async () => {
  let failed = false;
  const ui = await mount(async method => { if(method.startsWith("calendar.")){if(failed)throw new Error("calendar_service_unavailable");return {state:"unconnected",freshness:"empty",read_only:true,events:[]};}return {}; });
  await ui.click('[data-section="today"]'); expect(ui.container.querySelector('[data-calendar-state="unconnected"]')).not.toBeNull();
  expect(ui.container.querySelector('[data-calendar-freshness="empty"]')).not.toBeNull();
  failed=true; await ui.click('[data-action="refresh-agenda"]');
  expect(ui.container.textContent).toContain("calendar_service_unavailable"); expect(ui.container.querySelector('[data-task-id="t1"]')).not.toBeNull();
});
it("combines owner and inclusive updated-date filters over the explicitly bounded loaded task list", async () => {
  const base={goal:"Task",state:"running",revision:1,requirements_revision:1};
  const ui=await mount(undefined,main,undefined,{tasks:[
    {...base,task_id:"travel-new",agent_id:"a-travel",title:"Travel recent",updated_at:"2026-10-18T12:00:00Z"},
    {...base,task_id:"travel-old",agent_id:"a-travel",title:"Travel old",updated_at:"2026-09-01T12:00:00Z"},
    {...base,task_id:"main-new",agent_id:"a-main",title:"Main recent",updated_at:"2026-10-18T12:00:00Z"},
    {...base,task_id:"undated",agent_id:"a-travel",title:"No date"},
  ]});
  await ui.click('[data-section="tasks"]'); await changeField(ui.container,'[data-task-filter="owner"]',"a-travel");
  await changeField(ui.container,'[data-task-filter="from"]',"2026-10-18"); await changeField(ui.container,'[data-task-filter="until"]',"2026-10-18");
  expect(Array.from(ui.container.querySelectorAll('[data-task-id]')).map(row=>row.getAttribute("data-task-id"))).toEqual(["travel-new"]);
  expect(ui.container.querySelector('[data-task-filter-scope="loaded"]')).not.toBeNull();
});
it("searches only the selected named agent and marks partial history results without exposing session controls", async () => {
  const ui=await mount(async method=>method==="agents.search"?{session_id:"private-search-session",matches:[{seq:20,role:"assistant",excerpt:"Remembered Kyoto"},{seq:21,role:"tool",excerpt:"Hidden process data"}],has_more:true,budget_exhausted:true,next_cursor:"cursor"}:{});
  await ui.click('[data-agent-id="a-travel"]'); await changeField(ui.container,'[data-agent-history-query]',"Kyoto"); await ui.click('[data-action="search-agent-history"]');
  expect(ui.invoke).toHaveBeenCalledWith(expect.objectContaining({method:"agents.search",input:expect.objectContaining({agent_id:"a-travel",query:"Kyoto"})}));
  expect(ui.container.querySelector('[data-history-search-results]')?.textContent).toContain("Remembered Kyoto");
  expect(ui.container.querySelector('[data-history-search-partial]')).not.toBeNull();
  expect(ui.container.textContent).not.toContain("Hidden process data"); expect(ui.container.textContent).not.toContain("private-search-session");
  await ui.click('[data-agent-id="a-main"]'); expect(ui.container.textContent).not.toContain("Remembered Kyoto");
});
it("renders memory provenance and conflicting candidate facts as references rather than unresolved message links", async () => {
  const ui=await mount(undefined,main,undefined,{memories:[{id:"memory-one",revision:3,status:"conflict",content:"Accepted preference",evidence:"explicit",contributor_agent_id:"a-main",updated_by_agent_id:"a-travel",source_at:"2026-10-10T10:00:00Z",effective_at:"2026-10-11T00:00:00Z",expires_at:"2027-01-01T00:00:00Z",sources:[{kind:"message",ref:"message:source-one",occurred_at:"2026-10-10T10:00:00Z"}],contributions:[{revision:3,agent_id:"a-travel",executor_session_id:"executor-one",operation:"revise",source_at:"2026-10-12T10:00:00Z",effective_at:"2026-10-12T10:00:00Z",written_at:"2026-10-12T11:00:00Z"}],conflict:{content:"Candidate preference",agent_id:"a-travel",executor_session_id:"executor-two",evidence:"inferred",source_at:"2026-10-12T10:00:00Z",effective_at:"2026-10-12T10:00:00Z",sources:[{kind:"message",ref:"message:candidate",occurred_at:"2026-10-12T10:00:00Z"}]}}]});
  await ui.click('[data-section="memory"]'); const provenance=ui.container.querySelector('[data-memory-provenance="memory-one"]'); expect(provenance).not.toBeNull();
  for(const value of ["message:source-one","executor-one","2026-10-11T00:00:00Z","2027-01-01T00:00:00Z","Candidate preference","executor-two","message:candidate"])expect(provenance!.textContent).toContain(value);
  expect(provenance!.querySelector("a")).toBeNull();
});
it("saves reversible notification grouping without altering reply or quiet-hours settings", async () => {
  const ui=await mount(async method=>method==="settings.update"?{settings:{revision:2,notifications:true,notification_batch_seconds:300,quiet_hours:{enabled:false,start:"22:00",end:"08:00",timezone:"UTC"}}}:{});
  await ui.click('[data-section="settings"]'); await changeField(ui.container,'[data-notification-batch]',"300"); await ui.click('[data-action="save-notifications"]');
  expect(ui.invoke).toHaveBeenCalledWith(expect.objectContaining({method:"settings.update",input:{expected_revision:1,notifications:true,notification_batch_seconds:300,quiet_hours:{enabled:false,start:"22:00",end:"08:00",timezone:"UTC"}}}));
});
it("pages timestamp-bounded agent history and reads the original hit by exact owned sequence", async () => {
  const ui = await mount(async (method, input) => method === "agents.search" ? input.cursor ? { matches: [{ seq: 21, role: "user", excerpt: "Kyoto legacy" }], has_more: false } : { matches: [{ seq: 20, role: "assistant", excerpt: "Kyoto excerpt", at: "2026-10-18T09:00:00Z" }], has_more: true, next_cursor: "page-two" } : {}, main,
    [{ seq: 20, role: "assistant", content: "The original Kyoto message", at: "2026-10-18T09:00:00Z" }]);
  await changeField(ui.container, '[data-agent-history-query]', "Kyoto");
  await changeField(ui.container, '[data-history-date="from"]', "2026-10-18");
  await changeField(ui.container, '[data-history-date="until"]', "2026-10-18");
  await ui.click('[data-action="search-agent-history"]');
  const bounds = { agent_id: "a-main", query: "Kyoto", limit: 25, time_min: new Date(2026, 9, 18).toISOString(), time_max: new Date(2026, 9, 19).toISOString() };
  expect(ui.invoke).toHaveBeenCalledWith(expect.objectContaining({ method: "agents.search", input: bounds }));
  await ui.click('[data-action="more-agent-history"]');
  expect(ui.invoke).toHaveBeenCalledWith(expect.objectContaining({ method: "agents.search", input: { ...bounds, cursor: "page-two" } }));
  expect(ui.container.querySelector('[data-history-search-results]')?.textContent).toContain("Kyoto excerpt");
  expect(ui.container.querySelector('[data-history-timestamp="unknown"]')).not.toBeNull();
  await ui.click('[data-history-hit="20"]');
  expect(ui.invoke).toHaveBeenCalledWith(expect.objectContaining({ method: "agents.history", input: { agent_id: "a-main", start_seq: 20, end_seq: 20 } }));
  expect(ui.container.querySelector('[data-history-original="20"]')?.textContent).toContain("The original Kyoto message");
  await changeField(ui.container, '[data-history-date="from"]', "2026-10-19");
  expect(ui.container.querySelector('[data-history-original]')).toBeNull();
  expect(ui.container.querySelector<HTMLButtonElement>('[data-action="search-agent-history"]')!.disabled).toBe(true);
});
it("does not pass off a different history record as the searched original", async () => {
  const ui = await mount(async method => method === "agents.search" ? { matches: [{ seq: 80, role: "assistant", excerpt: "Old match" }] } : {});
  await changeField(ui.container, '[data-agent-history-query]', "Old"); await ui.click('[data-action="search-agent-history"]'); await ui.click('[data-history-hit="80"]');
  expect(ui.container.querySelector('[data-history-original]')).toBeNull();
  expect(ui.container.querySelector('[data-history-original-error]')).not.toBeNull();
});
it("discards an outstanding search when its named agent changes", async () => {
  let resolveSearch!: (result: unknown) => void;
  const ui = await mount(async method => method === "agents.search" ? new Promise(resolve => { resolveSearch = resolve; }) : {});
  await changeField(ui.container, '[data-agent-history-query]', "Old"); await ui.click('[data-action="search-agent-history"]');
  await ui.click('[data-agent-id="a-travel"]');
  await act(async () => resolveSearch({ matches: [{ seq: 80, role: "assistant", excerpt: "Late private result" }] }));
  expect(ui.container.textContent).not.toContain("Late private result");
  expect(ui.container.querySelector('[data-history-search-results]')).toBeNull();
});
it("clears an earlier fresh agenda claim when a subsequent cache read fails", async () => {
  let failed = false;
  const ui = await mount(async method => { if(method.startsWith("calendar.")){ if(failed) throw new Error("read_failed"); return { state: "ready", freshness: "fresh", events: [] }; } return {}; });
  await ui.click('[data-section="today"]'); expect(ui.container.querySelector('[data-calendar-freshness="fresh"]')).not.toBeNull();
  failed = true; await ui.click('[data-action="refresh-agenda"]');
  expect(ui.container.querySelector('[data-calendar-state="error"]')).not.toBeNull();
  expect(ui.container.querySelector('[data-calendar-freshness]')).toBeNull();
  expect(ui.container.querySelector('[data-calendar-cache-freshness="fresh"]')).toBeNull();
});
it("does not display a different cached calendar window as Today's agenda when the exact-day read fails", async () => {
  const ui = await mount(async method => {
    if (method === "calendar.status") return { state: "ready", freshness: "fresh", source: { calendar_id: "primary", time_min: "2026-10-01T00:00:00Z", time_max: "2026-11-01T00:00:00Z" }, events: [{ id: "outside-day", summary: "Different cached window", start: { date: "2026-10-01" }, end: { date: "2026-10-02" } }] };
    if (method === "calendar.agenda") throw new Error("exact_window_unavailable");
    return {};
  });
  await ui.click('[data-section="today"]');
  expect(ui.container.querySelector('[data-calendar-state="error"]')).not.toBeNull();
  expect(ui.container.querySelector('[data-calendar-event="outside-day"]')).toBeNull();
});
