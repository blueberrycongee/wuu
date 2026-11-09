import * as React from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createPluginUIKit } from "../../shared/workbench";
import { createPersonalReviewPanel, type ReviewService, type ReviewSnapshot } from "../../../../plugins/personal/ui/action-review-panel";

// UI boundary failures: duplicate submit, failed persistence shown as success,
// expired/stale cards still actionable, unconnected capability pretending enabled.
const ready = (): ReviewSnapshot => ({ schema:"personal-review.v1", kind:"ready", requests:[{
  revision:2,status:"awaiting_confirmation",outcome:"ask",reason:"Confirm the destination.",
  action:{id:"synthetic-1",digest:"a".repeat(64),operation:"synthetic.send",scope:{space_id:"synthetic",session_id:"synthetic-session",task_id:"synthetic-task",requirements_revision:3},effect:"external_write",summary:{title:"Send synthetic note",account:"demo@example.invalid",target:"reader@example.invalid",consequence:"Sends this one message."},arguments_json:'{"body":"Synthetic message"}',expires_at:"2099-01-01T00:00:00Z"},
}] });
let root:Root | undefined;
let container:HTMLDivElement;
afterEach(()=>{if(root)React.act(()=>root?.unmount());container?.remove();root=undefined;vi.useRealTimers()});
async function render(service:ReviewService, variant: "settings" | "conversation" = "settings"){
  container=document.createElement("div");document.body.append(container);root=createRoot(container);
  const Panel=createPersonalReviewPanel(React,createPluginUIKit(React));
  await React.act(async()=>{root?.render(<Panel service={service} locale="en" variant={variant}/>)});
}
function button(selector:string){const value=container.querySelector<HTMLButtonElement>(selector);if(!value)throw new Error(`missing ${selector}`);return value}
describe("Personal action approval component",()=>{
  // Lasting authority failures: preview must not save; a failed save must not
  // silently approve once; an acknowledgement must precede the current decision.
  it("previews a rule without granting permission, then saves before approving this action",async()=>{
    const events:string[]=[];
    const rules={schema:"personal-review-rules.v1",revision:1,rules:[]};
    const preview=vi.fn(async()=>({...rules,preview:{description:"Allow future messages to this exact recipient",operation:"synthetic.send",target:"reader@example.invalid",decision:"allow"}}));
    const saveRules=vi.fn(async()=>{events.push("saved");return {...rules,revision:2}});
    const resolve=vi.fn(async()=>{events.push("approved");return ready()});
    await render({list:async()=>ready(),resolve,revoke:async()=>ready(),rules:saveRules,previewRule:preview},"conversation");
    await React.act(async()=>button('[data-review-choice="remember"]').click());
    expect(preview).toHaveBeenCalledWith({space_id:"synthetic",id:"synthetic-1",digest:"a".repeat(64),expected_revision:2});
    expect(saveRules).not.toHaveBeenCalled();expect(resolve).not.toHaveBeenCalled();
    expect(container.querySelector('[data-review-rule-preview]')?.textContent).toContain("exact recipient");
    await React.act(async()=>button('[data-review-choice="save_rule"]').click());
    expect(saveRules).toHaveBeenCalledWith({expected_revision:1,action:"save",source:{space_id:"synthetic",id:"synthetic-1",digest:"a".repeat(64),expected_revision:2},decision:"allow"});
    expect(events).toEqual(["saved","approved"]);
    expect(resolve).toHaveBeenCalledWith({space_id:"synthetic",id:"synthetic-1",digest:"a".repeat(64),expected_revision:2,choice:"approve_once"});
  });
  it("does not approve after a failed rule save and permits an explicit once-only decision",async()=>{
    const resolve=vi.fn(async()=>ready());
    const rules={schema:"personal-review-rules.v1",revision:1,rules:[]};
    await render({list:async()=>ready(),resolve,revoke:async()=>ready(),previewRule:async()=>({...rules,preview:{description:"Only this destination",operation:"synthetic.send",target:"reader@example.invalid",decision:"allow"}}),rules:async command=>{if(command)throw new Error("Save unavailable");return rules}});
    await React.act(async()=>button('[data-review-choice="remember"]').click());
    await React.act(async()=>button('[data-review-choice="save_rule"]').click());
    expect(resolve).not.toHaveBeenCalled();expect(container.querySelector('[role="alert"]')).not.toBeNull();
    await React.act(async()=>button('[data-review-refresh]').click());
    await React.act(async()=>button('[data-review-choice="approve_once"]').click());
    expect(resolve).toHaveBeenCalledTimes(1);
  });
  it("updates and removes saved rules using the displayed rules revision",async()=>{
    const rule={id:"rule-1",description:"Allow this destination",operation:"synthetic.send",target:"reader@example.invalid",decision:"allow",enabled:true,created_at:"2026-11-01T00:00:00Z",updated_at:"2026-11-01T00:00:00Z"};
    const rules=vi.fn(async command=>command?.action==="delete"?{schema:"personal-review-rules.v1",revision:4,rules:[]}:{schema:"personal-review-rules.v1",revision:command?3:2,rules:[{...rule,decision:command?"ask":"allow"}]});
    await render({list:async()=>ready(),resolve:async()=>ready(),revoke:async()=>ready(),rules});
    await React.act(async()=>{const select=container.querySelector<HTMLSelectElement>('[data-review-rule-choice="rule-1"]')!;select.value="ask";select.dispatchEvent(new Event("change",{bubbles:true}));});
    expect(rules).toHaveBeenLastCalledWith({action:"update",expected_revision:2,id:"rule-1",decision:"ask",enabled:true});
    await React.act(async()=>button('[data-review-rule-delete="rule-1"]').click());
    expect(rules).toHaveBeenLastCalledWith({action:"delete",expected_revision:3,id:"rule-1"});
    expect(container.querySelector('[data-review-rule="rule-1"]')).toBeNull();
  });

  it("can turn AutoReview off after the last registered handler disappears",async()=>{
    const snapshot=ready();if(snapshot.kind!=="ready")throw new Error("ready fixture");
    snapshot.settings={auto_review_enabled:true,revision:4,policy_version:"personal.v1",scope:[]};
    const settings=vi.fn(async()=>snapshot);await render({list:async()=>snapshot,resolve:async()=>snapshot,revoke:async()=>snapshot,settings});
    expect(button('[role="switch"]').disabled).toBe(false);
    await React.act(async()=>button('[role="switch"]').click());expect(settings).toHaveBeenCalledWith({enabled:false,expected_revision:4});
  });

  it("keeps an unavailable initial review service from interrupting ordinary conversation",async()=>{
    await render({list:async()=>{throw new Error("review service unavailable")},resolve:async()=>ready(),revoke:async()=>ready()},"conversation");
    expect(container.querySelector('[data-personal-action-review]')).toBeNull();
    expect(container.querySelector('[role="alert"]')).toBeNull();
  });

  it("accepts the host context digest without permitting a client context override",async()=>{
    const snapshot=ready();if(snapshot.kind!=="ready")throw new Error("ready fixture");
    snapshot.requests[0].action.scope.context_digest="c".repeat(64);
    const resolve=vi.fn(async()=>snapshot);await render({list:async()=>snapshot,resolve,revoke:async()=>snapshot});
    await React.act(async()=>button('[data-review-choice="approve_once"]').click());
    expect(resolve).toHaveBeenCalledWith({space_id:"synthetic",id:"synthetic-1",digest:"a".repeat(64),expected_revision:2,choice:"approve_once"});
  });
  it("displays a bounded host receipt without changing pending authorization",async()=>{
    const snapshot=ready();if(snapshot.kind!=="ready")throw new Error("ready fixture");
    snapshot.requests.push({...structuredClone(snapshot.requests[0]),status:"succeeded",receipt_json:JSON.stringify({summary:"x".repeat(60_000),target:"synthetic-file",sha256:"e".repeat(64)}),action:{...structuredClone(snapshot.requests[0].action),id:"completed"}});
    await render({list:async()=>snapshot,resolve:async()=>snapshot,revoke:async()=>snapshot});
    expect(container.querySelector('[data-review-receipt] pre')?.textContent).toContain("synthetic-file");
    expect(button('[data-review-choice="approve_once"]').disabled).toBe(false);
  });

  it("keeps ordinary conversational questions and unconnected state out of approval cards",async()=>{
    await render({list:async()=>({schema:"personal-review.v1",kind:"ready",requests:[]}),resolve:async()=>ready(),revoke:async()=>ready()},"conversation");
    expect(container.querySelector('[data-personal-action-review]')).toBeNull();
  });
  it("shows consequential action cards in chat, never a read-only approval or the settings toggle",async()=>{
    const snapshot=ready();if(snapshot.kind!=="ready")throw new Error("ready fixture");
    snapshot.settings={auto_review_enabled:false,revision:1,policy_version:"personal.v1",scope:["synthetic.send"]};
    snapshot.requests.push({...structuredClone(snapshot.requests[0]),action:{...structuredClone(snapshot.requests[0].action),id:"read-only",effect:"read_only"}});
    await render({list:async()=>snapshot,resolve:async()=>ready(),revoke:async()=>ready(),settings:async()=>ready()},"conversation");
    expect(container.querySelector('[data-review-id="synthetic-1"]')).not.toBeNull();
    expect(container.querySelector('[data-review-id="read-only"]')).toBeNull();
    expect(container.querySelector('[role="switch"]')).toBeNull();
  });
  it("removes completed actions from chat without replaying the approval and stops after unmount",async()=>{
    vi.useFakeTimers();
    let snapshot=ready();const list=vi.fn(async()=>snapshot),resolve=vi.fn(async()=>ready());
    await render({list,resolve,revoke:async()=>ready()},"conversation");
    expect(container.querySelector('[data-personal-action-review]')).not.toBeNull();
    snapshot=ready();if(snapshot.kind==="ready"){snapshot.requests[0].status="succeeded";snapshot.requests[0].receipt_json='{ "receipt_id": "synthetic-only" }';}
    await React.act(async()=>vi.advanceTimersByTimeAsync(5000));
    expect(list).toHaveBeenCalledTimes(2);expect(resolve).not.toHaveBeenCalled();
    expect(container.querySelector('[data-personal-action-review]')).toBeNull();
    expect(container.textContent).not.toContain("synthetic-only");
    React.act(()=>root?.unmount());root=undefined;
    await React.act(async()=>vi.advanceTimersByTimeAsync(5000));expect(list).toHaveBeenCalledTimes(2);
  });
  it("opts in only after a saved settings revision and never double submits",async()=>{
    const snapshot=ready();if(snapshot.kind!=="ready")throw new Error("ready fixture");
    snapshot.settings={auto_review_enabled:false,revision:4,policy_version:"personal.v1",scope:["synthetic.send"]};
    let save:((value:ReviewSnapshot)=>void)|undefined;const settings=vi.fn(()=>new Promise<ReviewSnapshot>(done=>{save=done}));
    await render({list:async()=>snapshot,resolve:async()=>ready(),revoke:async()=>ready(),settings});
    const toggle=button('[role="switch"]');expect(toggle.getAttribute("aria-checked")).toBe("false");
    React.act(()=>{toggle.click();toggle.click()});
    expect(settings).toHaveBeenCalledTimes(1);expect(settings).toHaveBeenCalledWith({enabled:true,expected_revision:4});
    expect(toggle.getAttribute("aria-checked")).toBe("false");expect(toggle.disabled).toBe(true);
    const saved=structuredClone(snapshot);saved.settings!.auto_review_enabled=true;saved.settings!.revision++;
    await React.act(async()=>save?.(saved));expect(button('[role="switch"]').getAttribute("aria-checked")).toBe("true");
  });
  it("locks all mutations after a lost acknowledgement until a read verifies the state",async()=>{
    const snapshot=ready();if(snapshot.kind!=="ready")throw new Error("ready fixture");
    snapshot.settings={auto_review_enabled:false,revision:4,policy_version:"personal.v1",scope:["synthetic.send"]};
    const resolve=vi.fn(async()=>{throw new Error("Response lost; check saved state")}),settings=vi.fn(async()=>snapshot);
    await render({list:async()=>snapshot,resolve,revoke:async()=>ready(),settings});
    await React.act(async()=>button('[data-review-choice="approve_once"]').click());
    React.act(()=>{button('[data-review-choice="approve_once"]').click();button('[role="switch"]').click()});
    expect(resolve).toHaveBeenCalledTimes(1);expect(settings).not.toHaveBeenCalled();
    await React.act(async()=>button('[data-review-refresh]').click());
    expect(button('[role="switch"]').disabled).toBe(false);
  });

  it("displays canonical parameter text without rounding integers or overflowing exponents",async()=>{
    const snapshot=ready();
    if(snapshot.kind!=="ready")throw new Error("fixture must be ready");
    const exact='{"integer":9007199254740993,"overflow":1e400,"fraction":0.123456789012345678901}';
    const raw={...snapshot,schema:"personal-review.v1",requests:snapshot.requests.map(record=>({...record,action:{...record.action,arguments_json:exact}}))};
    const resolve=vi.fn(async()=>ready());
    await render({list:vi.fn().mockResolvedValue(raw),resolve,revoke:async()=>ready()});
    expect(container.querySelector("pre")?.textContent).toBe(exact);
    await React.act(async()=>button('[data-review-choice="approve_once"]').click());
    expect(resolve).toHaveBeenCalledWith({space_id:"synthetic",id:"synthetic-1",digest:"a".repeat(64),expected_revision:2,choice:"approve_once"});
  });
  it.each(["schema", "object-parameters", "invalid-json", "array-parameters", "unknown-effect", "unknown-status", "unsafe-revision"])("rejects an invalid response contract: %s",async(invalid)=>{
    const snapshot=ready();
    if(snapshot.kind!=="ready")throw new Error("fixture must be ready");
    const record=snapshot.requests[0];
    let raw:unknown={...snapshot,schema:"personal-review.v2"};
    if(invalid!=="schema"){
      const action={...record.action,arguments_json:invalid==="object-parameters"?{body:"rounded"}:invalid==="invalid-json"?"{broken":invalid==="array-parameters"?"[]":'{"body":"Synthetic message"}',effect:invalid==="unknown-effect"?"unclassified":record.action.effect};
      raw={...snapshot,schema:"personal-review.v1",requests:[{...record,action,status:invalid==="unknown-status"?"autoapproved":record.status,revision:invalid==="unsafe-revision"?9007199254740992:record.revision}]};
    }
    const resolve=vi.fn(async()=>ready());
    await render({list:vi.fn().mockResolvedValue(raw),resolve,revoke:async()=>ready()});
    expect(container.querySelector('[data-review-choice]')).toBeNull();
    expect(container.querySelector('[role="alert"]')).not.toBeNull();
    expect(resolve).not.toHaveBeenCalled();
  });
  it("submits the displayed digest and revision once, and waits for a saved response",async()=>{
    let finish:((snapshot:ReviewSnapshot)=>void)|undefined;
    const resolve=vi.fn(()=>new Promise<ReviewSnapshot>(done=>{finish=done}));
    await render({list:async()=>ready(),resolve,revoke:async()=>ready()});
    const approve=button('[data-review-choice="approve_once"]');
    React.act(()=>{approve.click();approve.click()});
    expect(resolve).toHaveBeenCalledTimes(1);
    expect(resolve).toHaveBeenCalledWith({space_id:"synthetic",id:"synthetic-1",digest:"a".repeat(64),expected_revision:2,choice:"approve_once"});
    expect(approve.disabled).toBe(true);
    expect(container.querySelector('[data-review-state="approved"]')).toBeNull();
    const saved=ready();if(saved.kind==="ready")saved.requests[0].status="approved";
    await React.act(async()=>{finish?.(saved)});
    expect(container.querySelector('[data-review-state="approved"]')).not.toBeNull();
    expect(container.querySelector('[data-review-choice="approve_once"]')).toBeNull();
  });
  it("binds the decision to the displayed snapshot when the service reuses its response object",async()=>{
    const snapshot=ready();
    if(snapshot.kind!=="ready")throw new Error("fixture must be ready");
    const resolve=vi.fn(async()=>ready());
    await render({list:async()=>snapshot,resolve,revoke:async()=>ready()});
    snapshot.requests[0].action.digest="b".repeat(64);
    snapshot.requests[0].action.arguments_json='{"body":"changed after display"}';
    expect(container.querySelector("pre")?.textContent).toBe('{"body":"Synthetic message"}');
    await React.act(async()=>button('[data-review-choice="approve_once"]').click());
    expect(resolve).toHaveBeenCalledWith({space_id:"synthetic",id:"synthetic-1",digest:"a".repeat(64),expected_revision:2,choice:"approve_once"});
  });
  it("keeps a failed or conflicting confirmation visible without claiming execution",async()=>{
    await render({list:async()=>ready(),resolve:async()=>{throw new Error("Revision changed; refresh this action.")},revoke:async()=>ready()});
    await React.act(async()=>button('[data-review-choice="approve_once"]').click());
    expect(container.querySelector('[role="alert"]')?.textContent).toContain("Revision changed");
    expect(container.querySelector('[data-review-state="awaiting_confirmation"]')).not.toBeNull();
    expect(button('[data-review-choice="approve_once"]').disabled).toBe(true);
    await React.act(async()=>button('[data-review-refresh]').click());
    expect(button('[data-review-choice="approve_once"]').disabled).toBe(false);
  });
  it("shows the captured original when a file will be replaced",async()=>{
    const snapshot=ready();if(snapshot.kind!=="ready")throw new Error("ready fixture");
    snapshot.requests[0].action.operation="write_file";
    snapshot.requests[0].action.arguments_json=JSON.stringify({operation:"write_file",exists:true,before_content:"Original contents",arguments:{path:"note.txt",content:"Replacement contents"}});
    await render({list:async()=>snapshot,resolve:async()=>ready(),revoke:async()=>ready()});
    const diff=container.querySelector('[data-review-changes]');
    expect(diff?.querySelectorAll('pre')).toHaveLength(2);expect(diff?.textContent).toContain("Original contents");expect(diff?.textContent).toContain("Replacement contents");
  });
  it("shows every frozen file edit before the owner allows it",async()=>{
    const snapshot=ready();if(snapshot.kind!=="ready")throw new Error("ready fixture");
    snapshot.requests[0].action.operation="edit_file";
    snapshot.requests[0].action.arguments_json=JSON.stringify({operation:"edit_file",arguments:{path:"note.txt",edits:[{old_text:"First old",new_text:"First new",replace_all:true},{old_text:"Second old",new_text:"Second new"}]}});
    await render({list:async()=>snapshot,resolve:async()=>ready(),revoke:async()=>ready()});
    const diff=container.querySelector('[data-review-changes]');
    expect(diff?.querySelectorAll('pre')).toHaveLength(4);
    expect(diff?.textContent).toContain("First old");expect(diff?.textContent).toContain("Second new");
  });
  it("accepts a host-bound original request caption and rejects malformed source data",async()=>{
    const snapshot=ready();if(snapshot.kind!=="ready")throw new Error("ready fixture");
    const withSource={...snapshot,requests:snapshot.requests.map(record=>({...record,action:{...record.action,summary:{...record.action.summary,source:"Prepare my weekly report"}}}))};
    await render({list:async()=>withSource,resolve:async()=>ready(),revoke:async()=>ready()});
    expect(container.querySelector('[data-review-source]')?.textContent).toContain("Prepare my weekly report");
    expect(button('[data-review-choice="approve_once"]').disabled).toBe(false);
    await React.act(async()=>root?.unmount());root=undefined;container.remove();
    const malformed={...withSource,requests:withSource.requests.map(record=>({...record,action:{...record.action,summary:{...record.action.summary,source:42}}}))};
    await render({list:async()=>malformed,resolve:async()=>ready(),revoke:async()=>ready()});
    expect(container.querySelector('[data-review-choice]')).toBeNull();
  });
  it("keeps a main-chat action without a time limit confirmable",async()=>{
    const snapshot=ready();if(snapshot.kind!=="ready")throw new Error("ready fixture");
    const raw={...snapshot,requests:snapshot.requests.map(record=>({...record,action:{...record.action,expires_at:null}}))};
    const resolve=vi.fn(async()=>ready());await render({list:async()=>raw,resolve,revoke:async()=>ready()});
    expect(button('[data-review-choice="approve_once"]').disabled).toBe(false);
    await React.act(async()=>button('[data-review-choice="approve_once"]').click());expect(resolve).toHaveBeenCalledTimes(1);
  });
  it("disables expired cards without sending a decision",async()=>{
    const snapshot=ready();if(snapshot.kind==="ready")snapshot.requests[0].action.expires_at="2000-01-01T00:00:00Z";
    const resolve=vi.fn(async()=>snapshot);await render({list:async()=>snapshot,resolve,revoke:async()=>snapshot});
    expect(button('[data-review-choice="approve_once"]').disabled).toBe(true);
    React.act(()=>button('[data-review-choice="approve_once"]').click());expect(resolve).not.toHaveBeenCalled();
  });
  it("lets the owner withdraw a blocked action whose review can later recover",async()=>{
    const snapshot=ready();if(snapshot.kind==="ready")snapshot.requests[0].status="blocked";
    const revoke=vi.fn(async()=>{const saved=ready();if(saved.kind==="ready")saved.requests[0].status="revoked";return saved});
    await render({list:async()=>snapshot,resolve:async()=>ready(),revoke});
    await React.act(async()=>button('[data-review-choice="revoke"]').click());
    expect(revoke).toHaveBeenCalledWith({space_id:"synthetic",id:"synthetic-1",digest:"a".repeat(64),expected_revision:2});
    expect(container.querySelector('[data-review-state="revoked"]')).not.toBeNull();
  });
  it("does not expose approval or enable controls for an unconnected host",async()=>{
    await render({list:async()=>({schema:"personal-review.v1",kind:"unavailable",reason:"Host action gate is not connected."}),resolve:async()=>ready(),revoke:async()=>ready()});
    expect(container.querySelector('[data-review-choice]')).toBeNull();
    expect(container.querySelector('[role="switch"]')).toBeNull();
    expect(container.textContent).toContain("not connected");
  });
  it("drops an old space snapshot and ignores its late response when the service changes",async()=>{
    container=document.createElement("div");document.body.append(container);root=createRoot(container);
    const Panel=createPersonalReviewPanel(React,createPluginUIKit(React));
    let finishOld:((snapshot:ReviewSnapshot)=>void)|undefined;
    const oldService:ReviewService={list:async()=>ready(),resolve:()=>new Promise(done=>{finishOld=done}),revoke:async()=>ready()};
    const newService:ReviewService={list:async()=>({schema:"personal-review.v1",kind:"unavailable",reason:"Different personal space"}),resolve:async()=>ready(),revoke:async()=>ready()};
    await React.act(async()=>root?.render(<Panel service={oldService}/>));
    React.act(()=>button('[data-review-choice="approve_once"]').click());
    await React.act(async()=>root?.render(<Panel service={newService}/>));
    expect(container.textContent).not.toContain("reader@example.invalid");
    expect(container.textContent).toContain("Different personal space");
    await React.act(async()=>finishOld?.(ready()));
    expect(container.textContent).not.toContain("reader@example.invalid");
  });
});
