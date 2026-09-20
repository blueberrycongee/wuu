export async function activate(api) {
  const React = api.react;
  const h = React.createElement;
  const { Page, Button, TextInput, TextArea, Checkbox, EmptyState } = api.ui;

  api.registerLocale({ id: "automation-en", locale: "en-US", entries: {
    "automation.save": "Save changes", "automation.saved": "Saved", "automation.unsaved": "Unsaved changes",
    "automation.custom": "Custom", "automation.once": "Run once", "automation.day": "Day",
    "automation.suggestions": "Suggestions", "automation.history": "Recent runs", "automation.more": "More actions",
    "automation.template.brief": "Morning briefing", "automation.template.brief.prompt": "Summarize recent changes in this project, pending work, and anything that needs my attention today.",
    "automation.template.review": "Weekly review", "automation.template.review.prompt": "Review this week's project progress. Summarize completed work, outstanding issues, and priorities for next week.",
    "automation.template.check": "Follow up", "automation.template.check.prompt": "Check the progress of pending work in this project. Report meaningful changes, blockers, and decisions that need my attention.",

    "automation.title": "Automations", "automation.subtitle": "Scheduled prompts and recurring work.",
    "automation.new": "New automation", "automation.empty": "No automations yet",
    "automation.emptyHelp": "Scheduled prompts will show up here.",
    "automation.search": "Search automations", "automation.filter.all": "All", "automation.filter.active": "Active",
    "automation.filter.empty": "No tasks match this filter.",
    "automation.name": "Name", "automation.prompt": "Instructions", "automation.schedule": "Cron schedule", "automation.timezone": "Timezone", "automation.workspace": "Workspace",
    "automation.recurring": "Repeat", "automation.workspaceHelp": "Tasks and runs shown below belong to this workspace.", "automation.workspaceNone": "No available project workspaces",
    "automation.isolation": "Run in an isolated git worktree", "automation.isolationHelp": "Each run executes in its own worktree instead of the live project directory.",
    "automation.create": "Create", "automation.cancel": "Cancel", "automation.pause": "Pause", "automation.resume": "Resume", "automation.remove": "Delete",
    "automation.paused": "Paused",
    "automation.mode.new": "New thread", "automation.mode.wake": "Wake session", "automation.mode.worktree": "worktree",
    "automation.next": "Next", "automation.run.completed": "Completed", "automation.run.failed": "Failed",
    "automation.run.running": "Running", "automation.run.queued": "Queued", "automation.run.interrupted": "Interrupted", "automation.run.never": "No runs yet",
    "automation.schedule.daily": "Daily", "automation.schedule.weekdays": "Weekdays", "automation.schedule.weekly": "Weekly",
    "automation.advanced": "More settings", "automation.resize": "Resize automation editor", "automation.close": "Close", "automation.group.details": "Execution", "automation.group.schedule": "Schedule",
    "automation.field.target": "Runs in", "automation.field.project": "Project", "automation.field.time": "Time", "automation.field.isolation": "Isolated run",
    "automation.target.new": "New chat each run", "automation.target.thread": "Existing chat",
    "automation.target.search": "Search chats", "automation.target.chats": "Chats", "automation.target.pinned": "Pinned", "automation.target.empty": "No matching chats",
    "automation.placeholder.name": "Task title", "automation.placeholder.prompt": "What should this automation do?", "automation.placeholder.schedule": "0 9 * * 1-5",
  }});
  api.registerLocale({ id: "automation-zh", locale: "zh-CN", entries: {
    "automation.save": "保存修改", "automation.saved": "已保存", "automation.unsaved": "修改未保存",
    "automation.custom": "自定义", "automation.once": "仅执行一次", "automation.day": "星期",
    "automation.suggestions": "建议", "automation.history": "最近运行", "automation.more": "更多操作",
    "automation.template.brief": "每日简报", "automation.template.brief.prompt": "汇总这个项目最近的变更、待办工作，以及今天需要我关注的事项。",
    "automation.template.review": "每周回顾", "automation.template.review.prompt": "回顾这个项目本周的进展，整理已完成的工作、尚未解决的问题和下周的重点。",
    "automation.template.check": "跟进监控", "automation.template.check.prompt": "检查这个项目待办工作的进展，报告有意义的变化、阻碍和需要我决定的事项。",

    "automation.title": "自动化", "automation.subtitle": "按计划运行的提示词与周期性任务。",
    "automation.new": "新建自动化", "automation.empty": "还没有自动化任务",
    "automation.emptyHelp": "按时间执行的提示词会显示在这里。",
    "automation.search": "搜索自动化", "automation.filter.all": "全部", "automation.filter.active": "已开启",
    "automation.filter.empty": "没有符合筛选的任务。",
    "automation.name": "名称", "automation.prompt": "任务内容", "automation.schedule": "Cron 时间", "automation.timezone": "时区", "automation.workspace": "工作区",
    "automation.recurring": "重复", "automation.workspaceHelp": "下方任务和运行记录都属于这个工作区。", "automation.workspaceNone": "没有可用的项目工作区",
    "automation.isolation": "在独立 git worktree 中运行", "automation.isolationHelp": "每次运行都在自己的 worktree 中执行，不直接改动当前项目目录。",
    "automation.create": "创建", "automation.cancel": "取消", "automation.pause": "暂停", "automation.resume": "继续", "automation.remove": "删除",
    "automation.paused": "已暂停",
    "automation.mode.new": "新会话", "automation.mode.wake": "唤醒会话", "automation.mode.worktree": "worktree",
    "automation.next": "下次", "automation.run.completed": "已完成", "automation.run.failed": "失败",
    "automation.run.running": "运行中", "automation.run.queued": "排队中", "automation.run.interrupted": "已中断", "automation.run.never": "暂无运行",
    "automation.schedule.daily": "每天", "automation.schedule.weekdays": "工作日", "automation.schedule.weekly": "每周",
    "automation.advanced": "更多设置", "automation.resize": "调整自动化编辑区宽度", "automation.close": "关闭", "automation.group.details": "执行设置", "automation.group.schedule": "时间安排",
    "automation.field.target": "运行于", "automation.field.project": "项目", "automation.field.time": "时间", "automation.field.isolation": "隔离运行",
    "automation.target.new": "每次新会话", "automation.target.thread": "指定会话",
    "automation.target.search": "搜索会话", "automation.target.chats": "会话", "automation.target.pinned": "已置顶", "automation.target.empty": "没有匹配的会话",
    "automation.placeholder.name": "任务标题", "automation.placeholder.prompt": "描述这次自动化要做什么", "automation.placeholder.schedule": "0 9 * * 1-5",
  }});
  api.registerStyle({ id: "automation-catalog", css: `
    .plugin-automation {
      --automation-unit: var(--wuu-space-unit, 4px);
      --automation-gap: calc(var(--automation-unit) * 4);
      --automation-inset: calc(var(--automation-unit) * 6);
      height:100%; min-height:0; container-type:inline-size;
      color:var(--wuu-color-text, var(--ink));
      font-size:var(--wuu-font-size-ui, var(--font-ui));
    }
    .plugin-automation *, .plugin-automation *::before { box-sizing:border-box; }
    .plugin-automation .plugin-ui-page { width:100%; height:100%; max-width:none; padding:0; font-size:inherit; }
    .plugin-automation-body { position:relative; display:flex; height:100%; min-height:0; }
    .plugin-automation-main { flex:1; min-width:0; overflow:auto; padding:var(--automation-inset); }
    .plugin-automation-main > * { max-width:880px; margin-inline:auto; }
    .plugin-automation-head { display:flex; align-items:center; justify-content:space-between; gap:var(--automation-gap); margin-bottom:var(--automation-gap); }
    .plugin-automation-title { margin:0; font-size:var(--font-title); line-height:var(--line-ui); font-weight:var(--weight-semibold); color:var(--ink-strong); }
    .plugin-automation-toolbar { display:flex; align-items:center; gap:8px; margin-bottom:12px; }
    .plugin-automation-search { flex:1; min-width:0; display:flex; align-items:center; gap:8px; height:2.6em; border:1px solid var(--field-border); border-radius:var(--wuu-radius-control, var(--radius-sm)); padding:0 12px; color:var(--ink-tertiary); background:var(--field-bg); }
    .plugin-automation-search:hover { background:var(--field-hover-bg); }
    .plugin-automation-search svg { width:var(--icon-size-sm); height:var(--icon-size-sm); flex:none; }
    .plugin-automation-search input { width:100%; min-width:0; border:0; padding:0; outline:0; background:transparent; color:inherit; font:inherit; color:var(--ink); }
    .plugin-automation-search input::placeholder { color:var(--ink-tertiary); }
    .plugin-automation-workspace-picker { flex:0 1 34%; min-width:0; }
    .plugin-automation-workspace-picker .plugin-automation-picker-trigger { width:100%; }
    .plugin-automation-filters { display:flex; flex-wrap:wrap; gap:4px; padding-bottom:12px; border-bottom:1px solid var(--hairline-soft); margin-bottom:4px; }
    .plugin-automation-filter { min-height:2.2em; border:0; border-radius:var(--wuu-radius-inner, var(--radius-xs)); padding:4px 10px; background:transparent; color:var(--ink-soft); font:inherit; font-size:var(--font-sm); cursor:pointer; }
    .plugin-automation-filter[aria-pressed="true"], .plugin-automation-filter:hover { background:var(--surface-2); color:var(--ink); }
    .plugin-automation-list { display:flex; flex-direction:column; gap:2px; }
    .plugin-automation-item { width:100%; display:flex; align-items:flex-start; gap:10px; min-width:0; padding:12px 10px; border:0; border-radius:var(--wuu-radius-inner, var(--radius-xs)); text-align:left; background:transparent; color:inherit; font:inherit; cursor:pointer; }
    .plugin-automation-item:hover { background:var(--surface-1); }
    .plugin-automation-item[data-selected="true"] { background:var(--surface-2); }
    .plugin-automation-item[data-paused="true"] .plugin-automation-item-title { color:var(--ink-soft); }
    .plugin-automation-item-icon { display:flex; align-items:center; justify-content:center; width:var(--icon-size); height:1.5em; flex:none; color:var(--ink-soft); }
    .plugin-automation-item-icon svg { width:var(--icon-size); height:var(--icon-size); }
    .plugin-automation-item-icon[data-run="failed"] { color:var(--danger); }
    .plugin-automation-item-main { min-width:0; display:grid; gap:3px; flex:1; }
    .plugin-automation-item-title { overflow:hidden; text-overflow:ellipsis; white-space:nowrap; font-size:inherit; font-weight:var(--weight-medium); line-height:1.5; }
    .plugin-automation-item-meta { overflow:hidden; text-overflow:ellipsis; white-space:nowrap; color:var(--ink-tertiary); font-size:var(--font-sm); line-height:1.5; }
    .plugin-automation-suggestions { margin-top:calc(var(--automation-gap) * 2); }
    .plugin-automation-suggestions > .plugin-automation-group-title { padding-bottom:12px; border-bottom:1px solid var(--hairline-soft); }
    .plugin-automation-group-title { margin:0 0 12px; color:var(--ink); font-size:var(--font-ui); font-weight:var(--weight-medium); line-height:1.4; }
    .plugin-automation-empty { min-height:160px; }
    .plugin-automation-error { color:var(--danger); font-size:var(--font-sm); overflow-wrap:anywhere; }
    .plugin-automation-filtered-empty { padding:24px 0; color:var(--ink-soft); text-align:center; }
    .plugin-automation-detail { flex:0 0 clamp(350px, var(--automation-detail-width, 48%), calc(100% - 280px)); min-width:0; overflow:auto; padding:var(--automation-inset); border-left:1px solid var(--hairline); }
    .plugin-automation-resizer { position:absolute; top:0; bottom:0; left:calc(100% - var(--automation-detail-width)); transform:translateX(-50%); width:10px; padding:0; border:0; outline:0; background:transparent; z-index:2; cursor:col-resize; touch-action:none; -webkit-app-region:no-drag; }
    .plugin-automation-resizer::before { content:""; position:absolute; top:0; bottom:0; left:0; width:10px; }
    .plugin-automation-resizer::after { content:""; position:absolute; top:0; bottom:0; left:50%; width:1px; }
    .plugin-automation-resizer:hover::after, .plugin-automation-resizer:focus-visible::after, .plugin-automation-body[data-resizing="true"] .plugin-automation-resizer::after { background:var(--sidebar-resizer-hover-bg, var(--ink-overlay-18)); box-shadow:0 0 0 1px var(--sidebar-resizer-hover-ring, var(--ink-overlay-8)); }
    .plugin-automation-body[data-resizing="true"], .plugin-automation-body[data-resizing="true"] * { cursor:col-resize !important; user-select:none; }
    .plugin-automation-detail-head { display:flex; align-items:center; gap:8px; min-height:2.6em; margin-bottom:var(--automation-gap); }
    .plugin-automation-detail-status { flex:1; color:var(--ink-tertiary); font-size:var(--font-sm); }
    .plugin-automation .plugin-ui-button { min-height:2.6em; border-radius:var(--wuu-radius-control, var(--radius-sm)); font-size:inherit; }
    .plugin-automation .plugin-automation-detail-close { display:flex; align-items:center; justify-content:center; width:2.2em; min-height:2.2em; padding:0; border-radius:var(--radius-xs); }
    .plugin-automation-detail-close svg { width:var(--icon-size-sm); height:var(--icon-size-sm); }
    .plugin-automation-form { display:flex; flex-direction:column; gap:calc(var(--automation-gap) * 1.5); }
    .plugin-automation-form-identity { display:grid; gap:var(--automation-gap); }
    .plugin-automation .plugin-ui-input { height:2.6em; }
    .plugin-automation .plugin-ui-field-label { font-size:inherit; font-weight:var(--weight-medium); }
    .plugin-automation .plugin-ui-textarea { min-height:6.5em; max-height:20em; font-size:inherit; line-height:1.6; }
    .plugin-automation-form-group { min-width:0; }
    .plugin-automation-form-card { min-width:0; }
    .plugin-automation-advanced { border-top:1px solid var(--hairline-soft); padding-top:12px; }
    .plugin-automation-advanced > summary { color:var(--ink-tertiary); font-size:var(--font-sm); cursor:pointer; width:fit-content; }
    .plugin-automation-advanced[open] > summary { margin-bottom:8px; }
    .plugin-automation-form-row { display:flex; align-items:center; justify-content:space-between; gap:12px; min-height:3em; padding:3px 0; font-size:inherit; }
    .plugin-automation-form-row > span:first-child { flex:none; }
    .plugin-automation-form-row-control { display:flex; justify-content:flex-end; min-width:0; max-width:70%; }
    .plugin-automation-form-row .plugin-ui-checkbox { width:100%; gap:12px; }
    .plugin-automation-field { min-width:0; max-width:100%; width:12em; height:2.6em; border:1px solid transparent; border-radius:var(--wuu-radius-control, var(--radius-sm)); padding:0 8px; background:transparent; color:inherit; font:inherit; text-align:right; }
    .plugin-automation-field:hover { background:var(--field-hover-bg); }
    .plugin-automation-field[type="time"] { width:8em; font-variant-numeric:tabular-nums; }
    .plugin-automation-field::-webkit-calendar-picker-indicator { display:none; }
    .plugin-automation-form-actions { display:flex; align-items:center; justify-content:flex-end; gap:8px; padding-bottom:4px; }
    .plugin-automation-history { display:grid; gap:12px; margin-top:24px; font-size:var(--font-sm); }
    .plugin-automation-history-row { display:flex; justify-content:space-between; gap:12px; color:var(--ink-soft); }
    .plugin-automation-more { position:relative; }
    .plugin-automation-more summary { display:grid; place-items:center; list-style:none; cursor:pointer; width:2.2em; height:2.2em; border-radius:var(--radius-xs); color:var(--ink-soft); }
    .plugin-automation-more summary:hover { background:var(--surface-2); }
    .plugin-automation-more-menu { position:absolute; top:100%; right:0; z-index:5; padding:var(--menu-inset); background:var(--menu-bg); border:1px solid var(--menu-border); border-radius:var(--menu-shell-radius); box-shadow:var(--menu-shadow); }
    .plugin-automation-sr { position:absolute; width:1px; height:1px; margin:-1px; overflow:hidden; clip:rect(0 0 0 0); white-space:nowrap; }
    .plugin-automation :is(button,summary):focus-visible { outline:1px solid var(--ink-soft); outline-offset:2px; box-shadow:none; }
    .plugin-automation :is(input,textarea):focus-visible { outline:0; border-color:var(--ink-soft); box-shadow:none; }
    .plugin-automation-picker { display:inline-flex; min-width:0; max-width:100%; }
    .plugin-automation .plugin-automation-picker-trigger { display:flex; align-items:center; justify-content:space-between; gap:8px; min-width:0; max-width:100%; border:1px solid transparent; color:var(--ink); background:transparent; font-weight:var(--weight-normal); padding:0 8px; }
    .plugin-automation .plugin-automation-picker-trigger:hover, .plugin-automation .plugin-automation-picker-trigger[aria-expanded="true"] { background:var(--field-hover-bg); }
    .plugin-automation-picker-trigger > span { overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
    .plugin-automation-picker-trigger svg { flex:none; width:var(--icon-size-xs); height:var(--icon-size-xs); color:var(--ink-tertiary); }
    .plugin-automation-picker-trigger[aria-expanded="true"] svg { transform:rotate(180deg); }
    .plugin-automation-picker-menu { position:fixed; inset:auto; margin:0; padding:var(--menu-inset); border:1px solid var(--menu-border); border-radius:var(--menu-shell-radius); background:var(--menu-bg); box-shadow:var(--menu-shadow); color:var(--ink); font-size:var(--font-ui); overflow:hidden; }
    .plugin-automation-picker-menu:popover-open { display:flex; flex-direction:column; gap:1px; }
    .plugin-automation-picker-search { display:flex; align-items:center; flex:none; gap:7px; height:2.6em; padding:0 9px; color:var(--ink-tertiary); }
    .plugin-automation-picker-search svg { flex:none; width:var(--icon-size-sm); height:var(--icon-size-sm); }
    .plugin-automation-picker-search input { flex:1; min-width:0; border:0; outline:0; background:transparent; color:var(--ink); font:inherit; }
    .plugin-automation-picker-options { display:flex; flex-direction:column; gap:2px; min-height:0; overflow:auto; }
    .plugin-automation-picker-option { display:flex; flex:none; line-height:var(--line-ui); align-items:center; justify-content:space-between; gap:10px; width:100%; min-width:0; min-height:2.5em; padding:7px 9px; border:0; border-radius:var(--radius-xs); color:var(--ink); background:transparent; font:inherit; text-align:left; cursor:pointer; }
    .plugin-automation-picker-option:hover, .plugin-automation-picker-option:focus-visible, .plugin-automation-picker-option[aria-selected="true"] { background:var(--menu-hover); outline:0; }
    .plugin-automation-picker-option-copy { min-width:0; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
    .plugin-automation-picker-option-copy > span { overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
    .plugin-automation-picker-option small, .plugin-automation-picker-group { color:var(--ink-tertiary); font-size:var(--font-xs); }
    .plugin-automation-picker-option svg { flex:none; width:var(--icon-size-sm); height:var(--icon-size-sm); }
    .plugin-automation-picker-group { padding:6px 9px 3px; }
    .plugin-automation-picker-empty { padding:12px; text-align:center; color:var(--ink-tertiary); }
    @container (max-width: 760px) {
      .plugin-automation-body[data-panel="true"] .plugin-automation-main { display:none; }
      .plugin-automation-resizer { display:none; }
      .plugin-automation-detail { flex:1; width:100%; max-width:none; min-width:0; border:0; }
    }
    @container (max-width: 420px) {
      .plugin-automation { --automation-inset:16px; }
      .plugin-automation-main, .plugin-automation-detail { padding:16px; }
      .plugin-automation-filter { padding-inline:8px; }
    }
    @media (prefers-reduced-motion: reduce) {
      .plugin-automation *, .plugin-automation-picker-menu { animation:none!important; transition:none!important; }
    }
  ` });

  function shortTimezone(timezone) {
    const parts = String(timezone || "").split("/");
    return parts[parts.length - 1] || "UTC";
  }

  function workspaceName(root) {
    const parts = String(root || "").split(/[\\/]/).filter(Boolean);
    return parts[parts.length - 1] || root || "—";
  }

  function formatDateTime(iso, timezone) {
    if (!iso) return null;
    const date = new Date(iso);
    if (Number.isNaN(date.getTime())) return null;
    const options = { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit" };
    try {
      return new Intl.DateTimeFormat(undefined, { ...options, timeZone: timezone || undefined }).format(date);
    } catch {
      return new Intl.DateTimeFormat(undefined, options).format(date);
    }
  }

  // Cron fields already represent wall-clock time in the task's timezone.
  function formatTime(hour, minute) {
    return `${String(hour).padStart(2, "0")}:${String(minute).padStart(2, "0")}`;
  }

  const WEEKDAY_REFERENCE = ["2026-01-04", "2026-01-05", "2026-01-06", "2026-01-07", "2026-01-08", "2026-01-09", "2026-01-10"];

  function weekdayName(day) {
    return new Intl.DateTimeFormat(undefined, { weekday: "long" }).format(new Date(`${WEEKDAY_REFERENCE[day]}T12:00:00`));
  }

  // Recognize the schedules people actually write (daily, weekdays, one
  // weekday per week) and present them in words; anything more expressive
  // stays as the raw cron expression.
  function describeSchedule(cron, timezone, tr) {
    const fields = String(cron || "").trim().split(/\s+/);
    if (fields.length !== 5) return null;
    const [minute, hour, dom, month, dow] = fields;
    if (dom !== "*" || month !== "*") return null;
    if (!/^\d{1,2}$/.test(minute) || !/^\d{1,2}$/.test(hour)) return null;
    const time = formatTime(Number(hour), Number(minute), timezone);
    if (dow === "*") return `${tr("automation.schedule.daily")} ${time}`;
    if (dow === "1-5") return `${tr("automation.schedule.weekdays")} ${time}`;
    if (/^[0-7]$/.test(dow)) return `${tr("automation.schedule.weekly")} ${weekdayName(Number(dow) % 7)} ${time}`;
    return null;
  }

  function runStatus(run) {
    if (!run) return null;
    switch (run.status) {
      case "completed": return "completed";
      case "failed": return "failed";
      case "running":
      case "starting": return "running";
      case "queued": return "queued";
      case "interrupted":
      case "discarded": return "interrupted";
      default: return "queued";
    }
  }

  function SearchIcon() {
    return h("svg", { viewBox: "0 0 16 16", fill: "none", "aria-hidden": true },
      h("circle", { cx: "7", cy: "7", r: "4.5", stroke: "currentColor", strokeWidth: "1.5" }),
      h("path", { d: "m13.5 13.5-3-3", stroke: "currentColor", strokeWidth: "1.5", strokeLinecap: "round" }));
  }

  function CloseIcon() {
    return h("svg", { viewBox: "0 0 16 16", fill: "none", "aria-hidden": true },
      h("path", { d: "m4 4 8 8m0-8-8 8", stroke: "currentColor", strokeWidth: "1.5", strokeLinecap: "round" }));
  }

  function CheckIcon() {
    return h("svg", { viewBox: "0 0 16 16", fill: "none", "aria-hidden": true },
      h("path", { d: "m3 8.5 3.5 3.5L13 5", stroke: "currentColor", strokeWidth: "1.5", strokeLinecap: "round", strokeLinejoin: "round" }));
  }

  function ChevronIcon() {
    return h("svg", { viewBox: "0 0 16 16", fill: "none", "aria-hidden": true },
      h("path", { d: "m4.5 6.5 3.5 3.5 3.5-3.5", stroke: "currentColor", strokeWidth: "1.5", strokeLinecap: "round", strokeLinejoin: "round" }));
  }

  function ClockIcon() {
    return h("svg", { viewBox: "0 0 24 24", fill: "none", stroke: "currentColor", strokeWidth: "1.75", strokeLinecap: "round", strokeLinejoin: "round", "aria-hidden": true },
      h("circle", { cx: "12", cy: "12", r: "9" }), h("path", { d: "M12 7v5l3 2" }));
  }

  // All automation choices share one menu. The browser's top layer lets a
  // menu escape a scrolling detail pane without reaching into host internals.
  function Picker({ label, value, options, onChange, disabled, searchable, placeholder }) {
    const [open, setOpen] = React.useState(false);
    const [query, setQuery] = React.useState("");
    const trigger = React.useRef(null);
    const menu = React.useRef(null);
    const id = React.useId();
    const visible = options.filter((item) => !query.trim() || item.label.toLocaleLowerCase().includes(query.trim().toLocaleLowerCase()));
    const close = () => { setOpen(false); trigger.current?.focus(); };
    React.useLayoutEffect(() => {
      if (!open || !menu.current) return undefined;
      const node = menu.current;
      node.showPopover?.();
      const position = () => {
        const rect = trigger.current.getBoundingClientRect();
        const width = Math.min(Math.max(rect.width, searchable ? 300 : 180), window.innerWidth - 24);
        const below = window.innerHeight - rect.bottom - 12;
        const above = rect.top - 12;
        const flip = below < Math.min(node.scrollHeight, 300) && above > below;
        node.style.width = `${width}px`;
        node.style.maxHeight = `${Math.max(80, Math.min(360, flip ? above : below))}px`;
        node.style.left = `${Math.max(12, Math.min(rect.right - width, window.innerWidth - width - 12))}px`;
        node.style.top = `${flip ? Math.max(12, rect.top - node.offsetHeight - 6) : rect.bottom + 6}px`;
      };
      position();
      (node.querySelector("input") || node.querySelector('[aria-selected="true"]') || node.querySelector('[role="option"]'))?.focus();
      const outside = (event) => { if (!node.contains(event.target) && !trigger.current?.contains(event.target)) setOpen(false); };
      const scroll = (event) => { if (!node.contains(event.target)) position(); };
      document.addEventListener("pointerdown", outside);
      window.addEventListener("resize", position);
      document.addEventListener("scroll", scroll, true);
      return () => { node.hidePopover?.(); document.removeEventListener("pointerdown", outside); window.removeEventListener("resize", position); document.removeEventListener("scroll", scroll, true); };
    }, [open]);
    const keyDown = (event) => {
      if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); close(); return; }
      if (event.key === "Tab") { setOpen(false); trigger.current?.focus(); return; }
      if (event.key !== "ArrowDown" && event.key !== "ArrowUp" && event.key !== "Home" && event.key !== "End") return;
      if ((event.key === "Home" || event.key === "End") && event.target.tagName === "INPUT") return;
      event.preventDefault();
      const items = [...menu.current.querySelectorAll('[role="option"]')];
      const index = items.indexOf(document.activeElement);
      const next = event.key === "Home" ? 0 : event.key === "End" ? items.length - 1 : event.key === "ArrowDown" ? (index + 1) % items.length : (index < 0 ? items.length - 1 : (index - 1 + items.length) % items.length);
      items[next]?.focus();
    };
    const selected = options.find((item) => item.value === value);
    return h("div", { className: "plugin-automation-picker" },
      h(Button, { className: "plugin-automation-picker-trigger", variant: "ghost", ref: trigger, disabled, "aria-label": label, "aria-haspopup": "listbox", "aria-expanded": open, "aria-controls": open ? id : undefined,
        onClick: () => { setQuery(""); setOpen(!open); }, onKeyDown: (event) => { if (event.key === "ArrowDown" || event.key === "ArrowUp") { event.preventDefault(); setOpen(true); } },
      }, h("span", null, selected?.label || placeholder || value), h(ChevronIcon)),
      open ? h("div", { id, ref: menu, popover: "manual", className: "plugin-automation-picker-menu", onKeyDown: keyDown },
        searchable ? h("label", { className: "plugin-automation-picker-search" }, h(SearchIcon), h("input", { type: "search", "aria-label": searchable, placeholder: searchable, value: query, onChange: (event) => setQuery(event.target.value) })) : null,
        h("div", { className: "plugin-automation-picker-options", role: "listbox", "aria-label": label }, visible.map((item) => h("button", { key: item.value, type: "button", role: "option", tabIndex: -1, "aria-selected": item.value === value, className: "plugin-automation-picker-option", onClick: () => { close(); setQuery(""); onChange(item.value); } },
            h("span", { className: "plugin-automation-picker-option-copy", title: item.label }, item.label), item.value === value ? h(CheckIcon) : null)),
          !visible.length ? h("div", { className: "plugin-automation-picker-empty" }, "—") : null)) : null);
  }

  function TargetPicker({ tr, value, threads, disabled, onSelect }) {
    const sorted = [...threads].sort((a, b) => Number(b.pinned === true) - Number(a.pinned === true) || String(b.updatedAt || "").localeCompare(String(a.updatedAt || "")));
    const options = [{ value: "", label: tr("automation.target.new") }, ...sorted.map((thread) => ({ value: thread.id, label: thread.title || thread.id }))];
    return h(Picker, { label: tr("automation.field.target"), value: value.mode === "thread_heartbeat" ? value.threadId : "", options, disabled, searchable: tr("automation.target.search"), placeholder: value.threadTitle || tr("automation.target.thread"), onChange: (id) => onSelect(id ? { mode: "thread_heartbeat", threadId: id, threadTitle: threads.find((thread) => thread.id === id)?.title } : { mode: "new_thread" }) });
  }

  function scheduleParts(cron) {
    const fields = String(cron || "").trim().split(/\s+/);
    const [minute, hour, day, month, weekday] = fields;
    if (fields.length === 5 && /^\d+$/.test(minute) && Number(minute) < 60 && /^\d+$/.test(hour) && Number(hour) < 24 && day === "*" && month === "*") {
      const frequency = weekday === "*" ? "daily" : weekday === "1-5" ? "weekdays" : /^[0-7]$/.test(weekday) ? "weekly" : "custom";
      return { frequency, time: formatTime(Number(hour), Number(minute)), day: /^[0-7]$/.test(weekday) ? String(Number(weekday) % 7) : "1" };
    }
    return { frequency: "custom", time: "09:00", day: "1" };
  }

  function ScheduleFields({ tr, draft, onChange }) {
    const parts = scheduleParts(draft.schedule);
    const update = (patch) => {
      const value = { ...parts, ...patch };
      const [hour, minute] = value.time.split(":").map(Number);
      const dow = value.frequency === "weekdays" ? "1-5" : value.frequency === "weekly" ? value.day : "*";
      onChange({ ...draft, schedule: `${minute} ${hour} * * ${dow}` });
    };
    const [custom, setCustom] = React.useState(parts.frequency === "custom");
    return h(React.Fragment, null,
      h(FieldRow, { label: tr("automation.recurring") }, h(Picker, {
        label: tr("automation.recurring"), value: custom ? "custom" : parts.frequency,
        onChange: (frequency) => { setCustom(frequency === "custom"); if (frequency !== "custom") update({ frequency }); },
        options: ["daily", "weekdays", "weekly", "custom"].map((value) => ({ value, label: tr(value === "custom" ? "automation.custom" : `automation.schedule.${value}`) })),
      })),
      custom ? h(FieldRow, { label: tr("automation.schedule") }, h("input", { className: "plugin-automation-field", "aria-label": tr("automation.schedule"), value: draft.schedule, required: true, onChange: (event) => onChange({ ...draft, schedule: event.target.value }) }))
        : h(React.Fragment, null,
          parts.frequency === "weekly" ? h(FieldRow, { label: tr("automation.day") }, h(Picker, { label: tr("automation.day"), value: parts.day, onChange: (day) => update({ day }), options: [1, 2, 3, 4, 5, 6, 0].map((day) => ({ value: String(day), label: weekdayName(day) })) })) : null,
          h(FieldRow, { label: tr("automation.field.time") }, h("input", { className: "plugin-automation-field", type: "time", required: true, "aria-label": tr("automation.field.time"), value: parts.time, onChange: (event) => { if (event.target.value) update({ time: event.target.value }); } }))));
  }

  function FieldRow({ label, children }) {
    return h("div", { className: "plugin-automation-form-row" }, h("span", null, label), h("label", { className: "plugin-automation-form-row-control" }, children));
  }

  function draftFor(task) {
    return { title: task?.title || "", prompt: task?.prompt || "", schedule: task?.cron || "0 9 * * 1-5", timezone: task?.timezone || Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC", mode: task?.mode || "new_thread", heartbeat_thread_id: task?.heartbeat_thread_id || "", recurring: task ? task.recurring === true : true, workspace: task?.workspace_mode || "shared" };
  }

  function Editor({ tr, task, initial, workspace, threads, busy, error, onSave, onPause, onRemove, onClose, runs, readOnly }) {
    const [draft, setDraft] = React.useState(() => initial || draftFor(task));
    const [saved, setSaved] = React.useState(() => initial || draftFor(task));
    const [localError, setLocalError] = React.useState("");
    const [advanced, setAdvanced] = React.useState(() => !draft.recurring || draft.workspace === "worktree" || draft.timezone !== Intl.DateTimeFormat().resolvedOptions().timeZone);
    const dirty = JSON.stringify(draft) !== JSON.stringify(saved);
    const panelRef = React.useRef(null);
    React.useEffect(() => {
      const previous = document.activeElement;
      panelRef.current?.querySelector("input, button")?.focus();
      return () => { if (previous instanceof HTMLElement && previous.isConnected) previous.focus(); };
    }, []);
    const save = async (event) => {
      event.preventDefault();
      setLocalError("");
      if (!draft.prompt.trim()) return;
      try { new Intl.DateTimeFormat(undefined, { timeZone: draft.timezone }); }
      catch { setAdvanced(true); setLocalError(`${tr("automation.timezone")}: ${draft.timezone}`); return; }
      const normalized = { ...draft, title: draft.title.trim() || draft.prompt.trim() };
      if (await onSave(normalized)) { setSaved(normalized); setDraft(normalized); }
    };
    return h("aside", { className: "plugin-automation-detail", ref: panelRef, "aria-label": task?.title || tr("automation.new"), onKeyDown: (event) => { if (event.key === "Escape" && !busy) { event.stopPropagation(); onClose(); } } },
      h("div", { className: "plugin-automation-detail-head" },
        h("span", { className: "plugin-automation-detail-status" }, !task ? tr("automation.new") : readOnly ? tr("automation.run.completed") : tr(task.paused ? "automation.paused" : "automation.filter.active")),
        task && !readOnly ? h(React.Fragment, null,
          h(Button, { variant: "ghost", disabled: busy, onClick: onPause }, tr(task.paused ? "automation.resume" : "automation.pause")),
          h("details", { className: "plugin-automation-more" }, h("summary", { "aria-label": tr("automation.more") }, "⋯"), h("div", { className: "plugin-automation-more-menu" }, h(Button, { variant: "danger", disabled: busy, onClick: onRemove }, tr("automation.remove"))))) : null,
        h(Button, { className: "plugin-automation-detail-close", variant: "ghost", disabled: busy, "aria-label": tr("automation.close"), onClick: onClose }, h(CloseIcon))),
      h("form", { className: "plugin-automation-form", onSubmit: save },
        h("fieldset", { disabled: busy || readOnly, style: { border: 0, padding: 0, margin: 0, minWidth: 0, display: "contents" } },
          h("div", { className: "plugin-automation-form-identity" },
            h(TextInput, { className: "plugin-automation-form-name", "aria-label": tr("automation.name"), placeholder: tr("automation.placeholder.name"), value: draft.title, onChange: (event) => setDraft({ ...draft, title: event.target.value }) }),
            h(TextArea, { className: "plugin-automation-form-prompt", "aria-label": tr("automation.prompt"), required: true, rows: 3, placeholder: tr("automation.placeholder.prompt"), value: draft.prompt, onChange: (event) => setDraft({ ...draft, prompt: event.target.value }) })),
          h("div", { className: "plugin-automation-form-card" },
            h("div", { className: "plugin-automation-form-row" }, h("span", null, tr("automation.field.target")), h("span", { className: "plugin-automation-form-row-control" }, h(TargetPicker, {
              tr, threads, disabled: busy || readOnly,
              value: { mode: draft.mode, threadId: draft.heartbeat_thread_id, threadTitle: threads.find((thread) => thread.id === draft.heartbeat_thread_id)?.title },
              onSelect: (target) => setDraft({ ...draft, mode: target.mode, heartbeat_thread_id: target.threadId || "", workspace: target.mode === "thread_heartbeat" ? "shared" : draft.workspace }),
            }))),
            h(ScheduleFields, { tr, draft, onChange: setDraft })),
          h("details", { className: "plugin-automation-advanced", open: advanced, onToggle: (event) => setAdvanced(event.currentTarget.open) },
            h("summary", null, tr("automation.advanced")),
            h(FieldRow, { label: tr("automation.timezone") }, h("input", { className: "plugin-automation-field", "aria-label": tr("automation.timezone"), required: true, value: draft.timezone, onChange: (event) => setDraft({ ...draft, timezone: event.target.value }) })),
            h("div", { className: "plugin-automation-form-row" }, h(Checkbox, { label: tr("automation.once"), checked: !draft.recurring, onChange: (event) => setDraft({ ...draft, recurring: !event.target.checked }) })),
            draft.mode !== "thread_heartbeat" ? h("div", { className: "plugin-automation-form-row" }, h(Checkbox, { label: tr("automation.field.isolation"), title: tr("automation.isolationHelp"), checked: draft.workspace === "worktree", onChange: (event) => setDraft({ ...draft, workspace: event.target.checked ? "worktree" : "shared" }) })) : null)),
        error || localError ? h("div", { className: "plugin-automation-error", role: "alert" }, error || localError) : null,
        !readOnly ? h("div", { className: "plugin-automation-form-actions" },
          task && dirty ? h("span", { className: "plugin-automation-detail-status", role: "status" }, tr("automation.unsaved")) : null,
          h(Button, { variant: "ghost", disabled: busy, onClick: onClose }, tr("automation.cancel")),
          h(Button, { type: "submit", variant: "primary", disabled: busy || !draft.prompt.trim() || (!!task && !dirty) }, tr(task ? "automation.save" : "automation.create"))) : null),
      runs.length ? h("section", { className: "plugin-automation-history" }, h("h3", { className: "plugin-automation-group-title" }, tr("automation.history")), runs.slice(0, 5).map((run) => h("div", { key: run.id }, h("div", { className: "plugin-automation-history-row" }, h("span", null, tr(`automation.run.${runStatus(run)}`)), h("time", null, formatDateTime(run.triggered_at, draft.timezone))), run.error ? h("p", { className: "plugin-automation-error" }, run.error) : null))) : null);
  }

  function useEditorResize() {
    const bodyRef = React.useRef(null);
    const drag = React.useRef(null);
    const [width, setWidth] = React.useState(null);
    const [available, setAvailable] = React.useState(1000);
    const [resizing, setResizing] = React.useState(false);
    React.useLayoutEffect(() => {
      const node = bodyRef.current;
      const measure = () => setAvailable(node.clientWidth);
      measure();
      const observer = typeof ResizeObserver === "function" ? new ResizeObserver(measure) : null;
      observer?.observe(node);
      return () => observer?.disconnect();
    }, []);
    const maximum = Math.max(350, available - 280);
    const clamp = (value) => Math.max(350, Math.min(maximum, value));
    const current = clamp(width ?? Math.min(560, available * 0.48));
    const end = () => { drag.current = null; setResizing(false); };
    return {
      bodyRef, resizing, width: current,
      separator: {
        role: "separator", tabIndex: 0, "aria-orientation": "vertical",
        "aria-valuemin": 350, "aria-valuemax": maximum, "aria-valuenow": Math.round(current),
        onPointerDown: (event) => {
          if (event.button !== 0) return;
          event.preventDefault();
          event.currentTarget.focus();
          const node = bodyRef.current;
          // Pointer coordinates include the host's zoom; widths use layout pixels.
          drag.current = { id: event.pointerId, x: event.clientX, width: current, scale: node.getBoundingClientRect().width / node.clientWidth || 1 };
          event.currentTarget.setPointerCapture(event.pointerId);
          setResizing(true);
        },
        onPointerMove: (event) => {
          const session = drag.current;
          if (session?.id === event.pointerId) setWidth(clamp(session.width - (event.clientX - session.x) / session.scale));
        },
        onPointerUp: (event) => {
          if (drag.current?.id !== event.pointerId) return;
          end();
          event.currentTarget.releasePointerCapture(event.pointerId);
        },
        onPointerCancel: end, onLostPointerCapture: end,
        onDoubleClick: () => setWidth(null),
        onKeyDown: (event) => {
          const next = { ArrowLeft: current + 24, ArrowRight: current - 24, Home: 350, End: maximum }[event.key];
          if (next === undefined) return;
          event.preventDefault(); setWidth(clamp(next));
        },
      },
    };
  }

  function Catalog(props) {
    const tr = props.translate;
    const resize = useEditorResize();
    const [tasks, setTasks] = React.useState([]);
    const [runs, setRuns] = React.useState([]);
    const [workspaces, setWorkspaces] = React.useState([]);
    const [workspaceID, setWorkspaceID] = React.useState("");
    const [selection, setSelection] = React.useState(null);
    const [busy, setBusy] = React.useState(false);
    const [error, setError] = React.useState("");
    const [query, setQuery] = React.useState("");
    const [filter, setFilter] = React.useState("all");
    const [threads, setThreads] = React.useState([]);
    const epoch = React.useRef(0);
    const workspace = workspaces.find((item) => item.id === workspaceID);
    const refresh = React.useCallback(async (id) => {
      const current = ++epoch.current;
      if (!id) return;
      try {
        const [list, history] = await Promise.all([api.invokeRuntime("automation.list", {}, { workspaceId: id }), api.invokeRuntime("automation.run.list", {}, { workspaceId: id })]);
        if (current !== epoch.current) return;
        if (list?.workspace?.id !== id) throw new Error("Automation runtime returned a different workspace");
        setTasks(Array.isArray(list.tasks) ? list.tasks : []);
        setRuns(Array.isArray(history?.runs) ? history.runs : []);
      } catch (reason) { if (current === epoch.current) setError(String(reason)); }
    }, []);
    React.useEffect(() => {
      let cancelled = false;
      api.listWorkspaces().then((snapshot) => {
        if (cancelled) return;
        const items = (snapshot?.workspaces || []).filter((item) => item.available !== false && item.id);
        setWorkspaces(items);
        setWorkspaceID(items.find((item) => item.id === snapshot.activeWorkspaceId)?.id || items[0]?.id || "");
      }).catch((reason) => { if (!cancelled) setError(String(reason)); });
      return () => { cancelled = true; epoch.current++; };
    }, []);
    React.useEffect(() => {
      void refresh(workspaceID);
      const timer = setInterval(() => { if (!document.hidden) void refresh(workspaceID); }, 15000);
      return () => { clearInterval(timer); epoch.current++; };
    }, [refresh, workspaceID]);
    React.useEffect(() => {
      let cancelled = false;
      setThreads([]);
      if (workspace?.root && typeof api.listThreads === "function") api.listThreads(workspace.root).then((items) => { if (!cancelled) setThreads(items || []); }).catch(() => {});
      return () => { cancelled = true; };
    }, [workspace?.root]);
    const act = async (method, input) => {
      if (!workspace || busy) return null;
      setBusy(true); setError("");
      try {
        const result = await api.invokeRuntime(method, { ...input, workspace_id: workspace.id, workspace_root: workspace.root }, { workspaceId: workspace.id });
        await refresh(workspace.id);
        return result;
      } catch (reason) { setError(String(reason)); return null; }
      finally { setBusy(false); }
    };
    const sortedRuns = [...runs].sort((a, b) => new Date(b.triggered_at) - new Date(a.triggered_at));
    // One-shot tasks leave the schedule on dispatch. Their retained snapshots
    // keep completed work reachable without changing scheduler persistence.
    const completed = sortedRuns.filter((run) => !run.task?.recurring && run.status === "completed" && !tasks.some((task) => task.id === run.task_id));
    const completedTasks = [...new Map(completed.map((run) => [run.task_id, run.task])).values()].filter(Boolean);
    const candidates = filter === "completed" ? completedTasks : filter === "all" ? [...tasks, ...completedTasks] : tasks.filter((task) => filter === "paused" ? task.paused : !task.paused);
    const visible = candidates.filter((task) => `${task.title}\n${task.prompt}`.toLowerCase().includes(query.trim().toLowerCase()));
    const selectedTask = selection?.id ? tasks.find((task) => task.id === selection.id) || completedTasks.find((task) => task.id === selection.id) : null;
    const panelOpen = !!workspace && !!selection && (selection.kind === "new" || !!selectedTask);
    const create = (template) => { setError(""); setSelection({ kind: "new", key: String(Date.now()), draft: { ...draftFor(), ...(template ? { title: tr(`automation.template.${template}`), prompt: tr(`automation.template.${template}.prompt`), schedule: template === "review" ? "0 16 * * 5" : "0 9 * * 1-5" } : {}) } }); };
    return h("main", { className: "plugin-automation" }, h(Page, null,
      h("div", { className: "plugin-automation-body", ref: resize.bodyRef, style: { "--automation-detail-width": `${resize.width}px` }, "data-resizing": resize.resizing ? "true" : "false", "data-panel": panelOpen ? "true" : "false" },
        h("section", { className: "plugin-automation-main", "aria-label": tr("automation.title") },
          h("div", { className: "plugin-automation-head" }, h("h1", { className: "plugin-automation-title" }, tr("automation.title")),
            h(Button, { variant: "primary", disabled: busy || !workspace, onClick: () => create() }, tr("automation.create"))),
          h("div", { className: "plugin-automation-toolbar" },
            h("label", { className: "plugin-automation-search" }, h(SearchIcon), h("span", { className: "plugin-automation-sr" }, tr("automation.search")), h("input", { type: "search", value: query, placeholder: tr("automation.search"), onChange: (event) => setQuery(event.target.value) })),
            h("div", { className: "plugin-automation-workspace-picker" }, h(Picker, {
              label: tr("automation.workspace"), value: workspaceID, disabled: busy || !workspaces.length,
              placeholder: tr("automation.workspaceNone"),
              options: workspaces.map((item) => ({ value: item.id, label: item.name || workspaceName(item.root) })),
              onChange: (id) => { epoch.current++; setWorkspaceID(id); setSelection(null); setTasks([]); setRuns([]); setError(""); },
            }))),
          h("div", { className: "plugin-automation-filters" }, ["all", "active", "paused", "completed"].map((value) => h("button", { key: value, className: "plugin-automation-filter", "aria-pressed": filter === value, onClick: () => setFilter(value) }, tr(value === "paused" ? "automation.paused" : value === "completed" ? "automation.run.completed" : `automation.filter.${value}`)))),
          error && !panelOpen ? h("p", { className: "plugin-automation-error", role: "alert" }, error) : null,
          !tasks.length && !completedTasks.length ? h(EmptyState, { className: "plugin-automation-empty", title: tr("automation.empty"), description: workspace ? undefined : tr("automation.workspaceNone") }) : !visible.length ? h("p", { className: "plugin-automation-filtered-empty" }, tr("automation.filter.empty")) : h("div", { className: "plugin-automation-list" }, visible.map((task) => {
            const done = !tasks.some((item) => item.id === task.id);
            const lastRun = sortedRuns.find((run) => run.task_id === task.id);
            const meta = done
              ? [tr("automation.run.completed"), formatDateTime(lastRun?.completed_at || lastRun?.triggered_at, task.timezone)].filter(Boolean).join(" · ")
              : [describeSchedule(task.cron, task.timezone, tr) || task.cron, task.paused ? tr("automation.paused") : `${tr("automation.next")} ${formatDateTime(task.next_run_at, task.timezone) || "—"}`].join(" · ");
            return h("button", { key: task.id, className: "plugin-automation-item", "data-selected": selection?.id === task.id, "data-paused": task.paused, "aria-pressed": selection?.id === task.id, disabled: busy, onClick: () => { setError(""); setSelection({ kind: "task", id: task.id }); } },
              h("span", { className: "plugin-automation-item-icon", "data-run": runStatus(sortedRuns.find((run) => run.task_id === task.id)) }, done ? h(CheckIcon) : h(ClockIcon)),
              h("span", { className: "plugin-automation-item-main" }, h("span", { className: "plugin-automation-item-title" }, task.title || task.prompt), h("span", { className: "plugin-automation-item-meta", title: `${task.cron} · ${task.timezone}` }, meta)));
          })),
          workspace ? h("section", { className: "plugin-automation-suggestions" }, h("h2", { className: "plugin-automation-group-title" }, tr("automation.suggestions")), ["brief", "review", "check"].map((key) => h("button", { key, className: "plugin-automation-item", disabled: busy, onClick: () => create(key) }, h("span", { className: "plugin-automation-item-icon" }, h(ClockIcon)), h("span", { className: "plugin-automation-item-main" }, h("span", { className: "plugin-automation-item-title" }, tr(`automation.template.${key}`)), h("span", { className: "plugin-automation-item-meta" }, tr(`automation.template.${key}.prompt`)))))) : null),
        panelOpen ? h("div", { ...resize.separator, className: "plugin-automation-resizer", "aria-label": tr("automation.resize") }) : null,
        panelOpen ? h(Editor, {
          key: `${workspaceID}:${selection.id || selection.key}`, tr, task: selectedTask, initial: selection.draft, workspace, threads, busy, error,
          readOnly: !!selectedTask && !tasks.some((task) => task.id === selectedTask.id),
          runs: sortedRuns.filter((run) => run.task_id === selectedTask?.id), onClose: () => { setSelection(null); setError(""); },
          onPause: () => act("automation.update", { id: selectedTask.id, paused: !selectedTask.paused }),
          onRemove: async () => { if (await act("automation.remove", { id: selectedTask.id })) setSelection(null); },
          onSave: async (draft) => {
            const result = await act(selectedTask ? "automation.update" : "automation.create", { ...draft, ...(selectedTask ? { id: selectedTask.id } : { durable: true }) });
            if (!result) return false;
            if (!selectedTask) setSelection({ kind: "task", id: result.id });
            return true;
          },
        }) : null)));
  }

  api.registerViewType({ id: "automation.catalog", title: "Automations", icon: "clock", defaultRegion: "primary", persistence: "durable", render: (props) => h(Catalog, props) });
}
