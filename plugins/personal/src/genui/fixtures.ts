import type { Spec, Node } from "./schema";
const actions: Spec["actions"] = [{ id: "save", kind: "save", label: "保存选择" }, { id: "submit", kind: "submit", label: "解释这些选择" }];
const buttons: Node[] = [{ id: "footer", type: "row", children: [{ id: "save", type: "button", label: "保存选择", action: "save" }, { id: "submit", type: "button", label: "解释这些选择", action: "submit" }] }];
export const fixtures: Record<string, Spec> = {
  checklist: {
    schemaVersion: 1, title: "周末出发前", fallbackText: "示例清单：检查车票、整理行李。勾选仅记录此界面的选择，不完成任何真实任务。", actions,
    fields: { items: { type: "list", scope: "persistent", maxItems: 12, item: { text: { type: "string", initial: "新项目", maxLength: 500 }, done: { type: "boolean", initial: false } }, initial: [{ text: "确认周六上午的出发时间，并把车票放进随身文件夹", done: false }, { text: "整理行李：雨伞、充电线，还有路上想读的那本书", done: false }] } },
    tree: { id: "root", type: "stack", children: [
      { id: "hint", type: "text", value: "把出发前要记住的事放在一起。可以修改、添加或移除。" },
      { id: "list", type: "repeat", field: "items", children: [{ id: "itemrow", type: "row", children: [{ id: "done", type: "checkbox", label: "已准备", itemField: "done" }, { id: "task", type: "textarea", label: "清单内容", itemField: "text" }, { id: "remove", type: "button", label: "移除", local: "remove", field: "items" }] }] },
      { id: "add", type: "button", label: "添加一项", local: "append", field: "items" },
      { id: "note", type: "text", value: "勾选只记录准备情况，不会停止或完成 Twin 的执行任务。" }, ...buttons,
    ] },
  },
  calculator: {
    schemaVersion: 1, title: "周末活动预算", fallbackText: "示例计算器：人数 × 每人预算。可用文字告诉 Twin 您的数字。", actions,
    fields: { count: { type: "number", scope: "persistent", initial: 4, min: 1, max: 20 }, price: { type: "number", scope: "persistent", initial: 80, min: 10, max: 300 } },
    tree: { id: "root", type: "stack", children: [
      { id: "sliders", type: "row", children: [{ id: "people", type: "slider", label: "参加人数", field: "count" }, { id: "price", type: "slider", label: "每人预算（元）", field: "price" }] },
      { id: "total", type: "card", children: [{ id: "totalLabel", type: "text", value: "预计总额（元）" }, { id: "sum", type: "text", value: { op: "mul", args: [{ get: "count" }, { get: "price" }] } }] },
      { id: "large", type: "text", visible: { op: "gt", args: [{ op: "mul", args: [{ get: "count" }, { get: "price" }] }, 1000] }, value: "总额超过 1,000 元，可以调整人数或每人预算。" }, ...buttons,
    ] },
  },
  comparison: {
    schemaVersion: 1, title: "选一个适合的周末", fallbackText: "示例对比：室外散步或室内展览。如有无障碍需求，请补充说明。", actions,
    fields: { plan: { type: "string", scope: "persistent", initial: "室外散步", options: ["室外散步", "室内展览"] }, accessible: { type: "boolean", scope: "persistent", initial: false }, needs: { type: "string", scope: "persistent", initial: "", maxLength: 500 }, expanded: { type: "boolean", scope: "temporary", initial: false } },
    tree: { id: "root", type: "stack", children: [
      { id: "plans", type: "row", children: [
        { id: "outdoors", type: "card", children: [{ id: "aTitle", type: "text", value: "室外散步" }, { id: "aDesc", type: "text", value: "时间自由 · 预算较低 · 需要留意天气" }] },
        { id: "indoors", type: "card", children: [{ id: "bTitle", type: "text", value: "室内展览" }, { id: "bDesc", type: "text", value: "天气影响小 · 需要预留入场时间" }] },
      ] },
      { id: "choice", type: "select", label: "我的选择", field: "plan" },
      { id: "access", type: "checkbox", label: "需要考虑无障碍条件", field: "accessible" },
      { id: "needs", type: "input", label: "需要留意的条件", field: "needs", visible: { get: "accessible" } },
      { id: "expand", type: "checkbox", label: "展开说明（仅本次查看）", field: "expanded" },
      { id: "details", type: "text", visible: { get: "expanded" }, value: "这是测试替身中的示例。选择只在预览中记录；点击解释这些选择不会发送给 Twin，也不会调用模型、订票、发消息或创建任务。" }, ...buttons,
    ] },
  },
};
