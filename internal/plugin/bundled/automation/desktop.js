export async function activate(api) {
  const React = api.react;
  const h = React.createElement;
  const { Page, Button, Select, Checkbox, EmptyState } = api.ui;

  api.registerLocale({ id: "automation-en", locale: "en-US", entries: {
    "automation.save": "Save changes", "automation.saved": "Saved", "automation.unsaved": "Unsaved changes",
    "automation.custom": "Custom", "automation.once": "Once at the next scheduled time", "automation.day": "Day",
    "automation.suggestions": "Suggestions", "automation.history": "Recent runs", "automation.more": "More actions",
    "automation.template.brief": "Morning briefing", "automation.template.brief.prompt": "Summarize recent changes in this project, pending work, and anything that needs my attention today.",
    "automation.template.review": "Weekly review", "automation.template.review.prompt": "Review this week's project progress. Summarize completed work, outstanding issues, and priorities for next week.",
    "automation.template.check": "Follow up", "automation.template.check.prompt": "Check the progress of pending work in this project. Report meaningful changes, blockers, and decisions that need my attention.",

    "automation.title": "Automations", "automation.subtitle": "Scheduled prompts and recurring work.",
    "automation.new": "New automation", "automation.empty": "No automations yet",
    "automation.emptyHelp": "Scheduled prompts will show up here.",
    "automation.search": "Search automations", "automation.filter.all": "All", "automation.filter.active": "Active",
    "automation.filter.empty": "No tasks match this filter.",
    "automation.name": "Name", "automation.prompt": "Prompt", "automation.schedule": "Cron schedule", "automation.timezone": "Timezone", "automation.workspace": "Workspace",
    "automation.recurring": "Repeat", "automation.workspaceHelp": "Tasks and runs shown below belong to this workspace.", "automation.workspaceNone": "No available project workspaces",
    "automation.isolation": "Run in an isolated git worktree", "automation.isolationHelp": "Each run executes in its own worktree instead of the live project directory.",
    "automation.create": "Create", "automation.cancel": "Cancel", "automation.pause": "Pause", "automation.resume": "Resume", "automation.remove": "Delete",
    "automation.paused": "Paused",
    "automation.mode.new": "New thread", "automation.mode.wake": "Wake session", "automation.mode.worktree": "worktree",
    "automation.next": "Next", "automation.run.completed": "Completed", "automation.run.failed": "Failed",
    "automation.run.running": "Running", "automation.run.queued": "Queued", "automation.run.interrupted": "Interrupted", "automation.run.never": "No runs yet",
    "automation.schedule.daily": "Daily", "automation.schedule.weekdays": "Weekdays", "automation.schedule.weekly": "Weekly",
    "automation.close": "Close", "automation.group.details": "Details", "automation.group.schedule": "Frequency",
    "automation.field.target": "Runs in", "automation.field.project": "Project", "automation.field.time": "Time", "automation.field.isolation": "Isolated run",
    "automation.target.new": "New chat each run", "automation.target.thread": "Existing chat",
    "automation.target.search": "Search chats", "automation.target.chats": "Chats", "automation.target.pinned": "Pinned", "automation.target.empty": "No matching chats",
    "automation.placeholder.name": "Task title", "automation.placeholder.prompt": "What should this automation do?", "automation.placeholder.schedule": "0 9 * * 1-5",
  }});
  api.registerLocale({ id: "automation-zh", locale: "zh-CN", entries: {
    "automation.save": "保存修改", "automation.saved": "已保存", "automation.unsaved": "修改未保存",
    "automation.custom": "自定义", "automation.once": "仅下次计划时间执行", "automation.day": "星期",
    "automation.suggestions": "建议", "automation.history": "最近运行", "automation.more": "更多操作",
    "automation.template.brief": "每日简报", "automation.template.brief.prompt": "汇总这个项目最近的变更、待办工作，以及今天需要我关注的事项。",
    "automation.template.review": "每周回顾", "automation.template.review.prompt": "回顾这个项目本周的进展，整理已完成的工作、尚未解决的问题和下周的重点。",
    "automation.template.check": "跟进监控", "automation.template.check.prompt": "检查这个项目待办工作的进展，报告有意义的变化、阻碍和需要我决定的事项。",

    "automation.title": "自动化", "automation.subtitle": "按计划运行的提示词与周期性任务。",
    "automation.new": "新建自动化", "automation.empty": "还没有自动化任务",
    "automation.emptyHelp": "按时间执行的提示词会显示在这里。",
    "automation.search": "搜索自动化", "automation.filter.all": "全部", "automation.filter.active": "已开启",
    "automation.filter.empty": "没有符合筛选的任务。",
    "automation.name": "名称", "automation.prompt": "提示词", "automation.schedule": "Cron 时间", "automation.timezone": "时区", "automation.workspace": "工作区",
    "automation.recurring": "重复", "automation.workspaceHelp": "下方任务和运行记录都属于这个工作区。", "automation.workspaceNone": "没有可用的项目工作区",
    "automation.isolation": "在独立 git worktree 中运行", "automation.isolationHelp": "每次运行都在自己的 worktree 中执行，不直接改动当前项目目录。",
    "automation.create": "创建", "automation.cancel": "取消", "automation.pause": "暂停", "automation.resume": "继续", "automation.remove": "删除",
    "automation.paused": "已暂停",
    "automation.mode.new": "新会话", "automation.mode.wake": "唤醒会话", "automation.mode.worktree": "worktree",
    "automation.next": "下次", "automation.run.completed": "已完成", "automation.run.failed": "失败",
    "automation.run.running": "运行中", "automation.run.queued": "排队中", "automation.run.interrupted": "已中断", "automation.run.never": "暂无运行",
    "automation.schedule.daily": "每天", "automation.schedule.weekdays": "工作日", "automation.schedule.weekly": "每周",
    "automation.close": "关闭", "automation.group.details": "详情", "automation.group.schedule": "频率",
    "automation.field.target": "运行于", "automation.field.project": "项目", "automation.field.time": "时间", "automation.field.isolation": "隔离运行",
    "automation.target.new": "每次新会话", "automation.target.thread": "指定会话",
    "automation.target.search": "搜索会话", "automation.target.chats": "会话", "automation.target.pinned": "已置顶", "automation.target.empty": "没有匹配的会话",
    "automation.placeholder.name": "任务标题", "automation.placeholder.prompt": "描述这次自动化要做什么", "automation.placeholder.schedule": "0 9 * * 1-5",
  }});
  api.registerStyle({ id: "automation-catalog", css: `
    .plugin-automation { height:100%; min-height:0; container-type:inline-size; color:var(--wuu-color-text, var(--ink)); background:var(--wuu-color-canvas, var(--paper)); }
    .plugin-automation *, .plugin-automation *::before { box-sizing:border-box; }
    .plugin-automation .plugin-ui-page { width:100%; height:100%; max-width:none; padding:0; }
    .plugin-automation-body { display:flex; height:100%; min-height:0; }
    .plugin-automation-main { flex:1; min-width:0; overflow:auto; padding:28px 32px; }
    .plugin-automation-body[data-panel="true"] .plugin-automation-main { padding:20px; }
    .plugin-automation-head { display:flex; align-items:center; justify-content:space-between; gap:12px; margin-bottom:20px; }
    .plugin-automation-title { font-size:20px; line-height:1.4; margin:0; font-weight:600; }
    .plugin-automation-filters { display:flex; flex-wrap:wrap; gap:2px; }
    .plugin-automation-filter { border:0; border-radius:7px; padding:5px 9px; background:transparent; color:var(--wuu-color-text-muted, var(--ink-muted)); cursor:pointer; font:inherit; font-size:12px; }
    .plugin-automation-filter[aria-pressed="true"], .plugin-automation-filter:hover { background:var(--wuu-color-surface-muted, var(--surface-1)); color:var(--wuu-color-text, var(--ink)); }
    .plugin-automation-toolbar { display:flex; align-items:center; gap:12px; margin-bottom:22px; flex-wrap:wrap; }
    .plugin-automation-search { flex:1 1 180px; display:flex; align-items:center; gap:8px; height:32px; border:1px solid var(--wuu-color-border-subtle, var(--hairline)); border-radius:9px; padding:0 10px; color:var(--wuu-color-text-muted, var(--ink-muted)); }
    .plugin-automation-search svg { width:14px; height:14px; flex:none; }
    .plugin-automation-search input { width:100%; min-width:0; border:0; outline:0; background:transparent; color:var(--wuu-color-text, var(--ink)); font:inherit; font-size:13px; }
    .plugin-automation-workspace-picker { flex:none; max-width:100%; }
    .plugin-automation-workspace-picker .plugin-ui-field-label { flex:none; white-space:nowrap; font-size:12px; color:var(--wuu-color-text-muted, var(--ink-muted)); }
    .plugin-automation-workspace-picker .plugin-ui-field { display:flex; align-items:center; gap:8px; }
    .plugin-automation-workspace-picker select { max-width:180px; }
    .plugin-automation-list { display:flex; flex-direction:column; gap:3px; }
    .plugin-automation-item { width:100%; display:flex; align-items:flex-start; gap:10px; min-width:0; padding:11px 10px; border:0; border-radius:9px; text-align:left; background:transparent; color:inherit; font:inherit; cursor:pointer; }
    .plugin-automation-item:hover, .plugin-automation-item[data-selected="true"] { background:var(--wuu-color-surface-muted, var(--surface-1)); }
    .plugin-automation-item[data-paused="true"] .plugin-automation-item-main { opacity:0.55; }
    .plugin-automation-item-dot { width:12px; height:12px; margin-top:3px; flex:none; border:1.5px solid var(--wuu-color-text-muted, var(--ink-muted)); border-radius:50%; }
    .plugin-automation-item-dot[data-run="failed"] { border-color:var(--wuu-color-danger, var(--danger)); }
    .plugin-automation-item-dot[data-run="running"] { background:var(--wuu-color-text-muted, var(--ink-muted)); }
    .plugin-automation-item-main { min-width:0; display:grid; gap:3px; flex:1; }
    .plugin-automation-item-title { overflow:hidden; text-overflow:ellipsis; white-space:nowrap; font-size:13px; font-weight:500; line-height:1.4; }
    .plugin-automation-item-meta { overflow:hidden; text-overflow:ellipsis; white-space:nowrap; color:var(--wuu-color-text-muted, var(--ink-muted)); font-size:12px; line-height:1.5; }
    .plugin-automation-suggestions { margin-top:24px; padding-top:20px; border-top:1px solid var(--wuu-color-border-subtle, var(--hairline)); }
    .plugin-automation-group-title { margin:0 0 10px; color:var(--wuu-color-text-muted, var(--ink-muted)); font-size:12px; font-weight:500; }
    .plugin-automation-empty { min-height:160px; }
    .plugin-automation-error { color:var(--wuu-color-danger, var(--danger)); font-size:13px; overflow-wrap:anywhere; }
    .plugin-automation-filtered-empty { padding:24px 0; color:var(--wuu-color-text-muted, var(--ink-muted)); font-size:13px; text-align:center; }
    .plugin-automation-detail { flex:0 0 46%; width:46%; min-width:340px; max-width:520px; overflow:auto; padding:20px 24px; border-left:1px solid var(--wuu-color-border-subtle, var(--hairline)); }
    .plugin-automation-detail-head { display:flex; align-items:center; gap:8px; margin-bottom:18px; }
    .plugin-automation-detail-status { flex:1; color:var(--wuu-color-text-muted, var(--ink-muted)); font-size:12px; }
    .plugin-automation-detail-close { display:flex; align-items:center; justify-content:center; width:28px; height:28px; border:0; border-radius:7px; background:transparent; color:var(--wuu-color-text-muted, var(--ink-muted)); cursor:pointer; }
    .plugin-automation-detail-close:hover { background:var(--wuu-color-surface-muted, var(--surface-1)); }
    .plugin-automation-detail-close svg { width:14px; height:14px; }
    .plugin-automation-form { display:flex; flex-direction:column; gap:24px; }
    .plugin-automation-form-identity { display:grid; gap:14px; }
    .plugin-automation-form-name { width:100%; border:0; background:transparent; color:inherit; font:inherit; font-size:17px; font-weight:600; padding:4px 0; }
    .plugin-automation-form-prompt { width:100%; min-height:100px; resize:vertical; border:1px solid var(--wuu-color-border-subtle, var(--hairline)); border-radius:12px; background:transparent; color:inherit; font:inherit; font-size:13px; line-height:1.6; padding:12px; }
    .plugin-automation-form-group { min-width:0; }
    .plugin-automation-form-card { border:1px solid var(--wuu-color-border-subtle, var(--hairline)); border-radius:12px; padding:0 12px; }
    .plugin-automation-form-row { display:flex; align-items:center; justify-content:space-between; gap:12px; min-height:43px; border-bottom:1px solid var(--wuu-color-border-subtle, var(--hairline)); font-size:13px; }
    .plugin-automation-form-row:last-child { border:0; }
    .plugin-automation-form-row > span:first-child { flex:none; }
    .plugin-automation-form-row-control { display:flex; justify-content:flex-end; min-width:0; max-width:72%; }
    .plugin-automation-form-row .plugin-ui-checkbox { width:100%; flex-direction:row-reverse; justify-content:space-between; }
    .plugin-automation-field { min-width:0; max-width:100%; width:180px; border:0; border-radius:6px; padding:6px 2px; background:var(--wuu-color-canvas, var(--paper)); color:inherit; font:inherit; text-align:right; }
    select.plugin-automation-field { width:auto; text-align-last:right; }
    .plugin-automation-form-actions { display:flex; align-items:center; justify-content:flex-end; gap:8px; }
    .plugin-automation-history { display:grid; gap:10px; margin-top:24px; font-size:12px; }
    .plugin-automation-history-row { display:flex; justify-content:space-between; gap:12px; color:var(--wuu-color-text-muted, var(--ink-muted)); }
    .plugin-automation-more { position:relative; }
    .plugin-automation-more summary { list-style:none; cursor:pointer; padding:4px 8px; }
    .plugin-automation-more-menu { position:absolute; top:100%; right:0; z-index:5; padding:6px; background:var(--wuu-color-canvas, var(--paper)); border:1px solid var(--wuu-color-border-subtle, var(--hairline)); border-radius:9px; }
    .plugin-automation-sr { position:absolute; width:1px; height:1px; margin:-1px; overflow:hidden; clip:rect(0 0 0 0); white-space:nowrap; }
    .plugin-automation input:focus-visible, .plugin-automation textarea:focus-visible, .plugin-automation button:focus-visible, .plugin-automation select:focus-visible, .plugin-automation summary:focus-visible { outline:2px solid var(--wuu-color-text-muted, var(--ink-muted)); outline-offset:2px; }
    @container (max-width: 720px) {
      .plugin-automation-body[data-panel="true"] .plugin-automation-main { display:none; }
      .plugin-automation-detail { flex:1; width:100%; max-width:none; min-width:0; border:0; padding:20px; }
      .plugin-automation-main { padding:20px; }
      .plugin-automation-head { flex-wrap:wrap; }
    }
    .plugin-automation-target { position:relative; display:inline-flex; min-width:0; max-width:100%; }
    .plugin-automation-target-button { display:inline-flex; align-items:center; gap:6px; max-width:100%; min-height:28px; border:0; border-radius:8px; padding:3px 8px; background:transparent; color:var(--wuu-color-text, var(--ink)); font:inherit; font-size:var(--font-ui,13px); cursor:pointer; transition:background-color var(--motion-fast,120ms) ease; }
    .plugin-automation-target-button:not(:disabled):hover { background:var(--wuu-color-surface-muted, var(--surface-2)); }
    .plugin-automation-target-button:disabled { cursor:default; opacity:0.6; }
    .plugin-automation-target-button > svg { width:12px; height:12px; flex:none; color:var(--wuu-color-text-muted, var(--ink-muted)); }
    .plugin-automation-target-value { min-width:0; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
    .plugin-automation-target-menu { position:absolute; top:calc(100% + 6px); right:0; z-index:70; width:min(300px, 80vw); max-height:340px; overflow:auto; display:flex; flex-direction:column; gap:2px; box-sizing:border-box; border:1px solid var(--wuu-color-border-subtle, var(--hairline)); border-radius:12px; padding:6px; background:var(--wuu-color-canvas, var(--paper, #fff)); box-shadow:0 12px 32px var(--ink-overlay-12, rgba(18,18,18,0.14)); }
    .plugin-automation-target[data-align="left"] .plugin-automation-target-menu { right:auto; left:0; }
    .plugin-automation-target-search { display:flex; align-items:center; gap:6px; height:30px; flex:none; margin-bottom:2px; border-radius:8px; padding:0 8px; background:var(--wuu-color-surface-muted, var(--surface-1)); color:var(--wuu-color-text-muted, var(--ink-muted)); }
    .plugin-automation-target-search svg { width:13px; height:13px; flex:none; }
    .plugin-automation-target-search input { flex:1; min-width:0; border:0; outline:0; padding:0; background:transparent; color:var(--wuu-color-text, var(--ink)); font:inherit; font-size:var(--font-sm,12px); }
    .plugin-automation-target-option { display:flex; align-items:center; gap:8px; width:100%; border:0; border-radius:8px; padding:7px 8px; background:transparent; color:var(--wuu-color-text, var(--ink)); font:inherit; font-size:var(--font-ui,13px); text-align:left; cursor:pointer; }
    .plugin-automation-target-option:hover { background:var(--wuu-color-surface-muted, var(--surface-1)); }
    .plugin-automation-target-option-main { flex:1; min-width:0; display:grid; gap:1px; }
    .plugin-automation-target-option-title { overflow:hidden; text-overflow:ellipsis; white-space:nowrap; }
    .plugin-automation-target-option[aria-selected="true"] .plugin-automation-target-option-title { font-weight:var(--weight-medium,500); }
    .plugin-automation-target-option-meta { overflow:hidden; text-overflow:ellipsis; white-space:nowrap; color:var(--wuu-color-text-muted, var(--ink-muted)); font-size:11px; }
    .plugin-automation-target-option > svg { width:14px; height:14px; flex:none; color:var(--wuu-color-text-muted, var(--ink-muted)); }
    .plugin-automation-target-section { padding:6px 8px 2px; color:var(--wuu-color-text-muted, var(--ink-muted)); font-size:11px; }
    .plugin-automation-target-empty { margin:0; padding:8px; color:var(--wuu-color-text-muted, var(--ink-muted)); font-size:var(--font-sm,12px); text-align:center; }

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

  // Shared "runs in" picker: "new chat each run" plus the workspace's live
  // conversations (pinned first, then most recently updated). The host
  // already filters ephemeral and archived threads; when thread listing is
  // unavailable the menu simply offers the new-chat option.
  function TargetPicker({ tr, value, threads, disabled, align, onSelect }) {
    const [open, setOpen] = React.useState(false);
    const [query, setQuery] = React.useState("");
    const rootRef = React.useRef(null);
    React.useEffect(() => {
      if (!open) return undefined;
      const onPointerDown = (event) => {
        if (rootRef.current && !rootRef.current.contains(event.target)) setOpen(false);
      };
      document.addEventListener("mousedown", onPointerDown);
      return () => document.removeEventListener("mousedown", onPointerDown);
    }, [open]);
    const isSelected = (thread) => value.mode === "thread_heartbeat" && value.threadId === thread.id;
    const label = value.mode === "thread_heartbeat" ? value.threadTitle || tr("automation.target.thread") : tr("automation.target.new");
    const normalized = query.trim().toLowerCase();
    const visible = [...threads]
      .sort((a, b) => ((b.pinned === true) - (a.pinned === true)) || String(b.updatedAt || "").localeCompare(String(a.updatedAt || "")))
      .filter((thread) => !normalized || String(thread.title || "").toLowerCase().includes(normalized))
      .slice(0, 20);
    const pinned = visible.filter((thread) => thread.pinned === true);
    const rest = visible.filter((thread) => thread.pinned !== true);
    const choose = (target) => {
      setOpen(false);
      setQuery("");
      onSelect(target);
    };
    const renderThread = (thread) => h("button", {
      key: thread.id,
      type: "button",
      className: "plugin-automation-target-option",
      "aria-selected": isSelected(thread) ? "true" : "false",
      onClick: () => choose({ mode: "thread_heartbeat", threadId: thread.id, threadTitle: thread.title }),
    },
      h("span", { className: "plugin-automation-target-option-main" },
        h("span", { className: "plugin-automation-target-option-title" }, thread.title),
        h("span", { className: "plugin-automation-target-option-meta" }, formatDateTime(thread.updatedAt) || "")),
      isSelected(thread) ? h(CheckIcon) : null);
    return h("div", { className: "plugin-automation-target", ref: rootRef, "data-align": align || undefined },
      h("button", {
        type: "button",
        className: "plugin-automation-target-button",
        disabled,
        "aria-expanded": open ? "true" : "false",
        onClick: () => { setOpen(!open); setQuery(""); },
      },
        h("span", { className: "plugin-automation-target-value" }, label),
        h(ChevronIcon)),
      open ? h("div", {
        className: "plugin-automation-target-menu",
        onKeyDown: (event) => {
          if (event.key === "Escape") {
            event.stopPropagation();
            setOpen(false);
          }
        },
      },
        h("label", { className: "plugin-automation-target-search" },
          h("span", { className: "plugin-automation-sr" }, tr("automation.target.search")),
          h(SearchIcon),
          h("input", { type: "search", autoFocus: true, value: query, placeholder: tr("automation.target.search"), onChange: (event) => setQuery(event.target.value) })),
        h("button", {
          type: "button",
          className: "plugin-automation-target-option",
          "aria-selected": value.mode !== "thread_heartbeat" ? "true" : "false",
          onClick: () => choose({ mode: "new_thread" }),
        },
          h("span", { className: "plugin-automation-target-option-main" },
            h("span", { className: "plugin-automation-target-option-title" }, tr("automation.target.new"))),
          value.mode !== "thread_heartbeat" ? h(CheckIcon) : null),
        pinned.length > 0 ? h("div", { className: "plugin-automation-target-section" }, tr("automation.target.pinned")) : null,
        pinned.map(renderThread),
        rest.length > 0 ? h("div", { className: "plugin-automation-target-section" }, tr("automation.target.chats")) : null,
        rest.map(renderThread),
        normalized && visible.length === 0 ? h("p", { className: "plugin-automation-target-empty" }, tr("automation.target.empty")) : null)
      : null);
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
      h(FieldRow, { label: tr("automation.recurring") }, h("select", {
        className: "plugin-automation-field", "aria-label": tr("automation.recurring"), value: custom ? "custom" : parts.frequency,
        onChange: (event) => { const frequency = event.target.value; setCustom(frequency === "custom"); if (frequency !== "custom") update({ frequency }); },
      }, ["daily", "weekdays", "weekly", "custom"].map((value) => h("option", { key: value, value }, tr(value === "custom" ? "automation.custom" : `automation.schedule.${value}`))))),
      custom ? h(FieldRow, { label: tr("automation.schedule") }, h("input", { className: "plugin-automation-field", "aria-label": tr("automation.schedule"), value: draft.schedule, required: true, onChange: (event) => onChange({ ...draft, schedule: event.target.value }) }))
        : h(React.Fragment, null,
          parts.frequency === "weekly" ? h(FieldRow, { label: tr("automation.day") }, h("select", { className: "plugin-automation-field", "aria-label": tr("automation.day"), value: parts.day, onChange: (event) => update({ day: event.target.value }) }, [1, 2, 3, 4, 5, 6, 0].map((day) => h("option", { key: day, value: String(day) }, weekdayName(day))))) : null,
          h(FieldRow, { label: tr("automation.field.time") }, h("input", { className: "plugin-automation-field", type: "time", required: true, "aria-label": tr("automation.field.time"), value: parts.time, onChange: (event) => { if (event.target.value) update({ time: event.target.value }); } }))),
      h(FieldRow, { label: tr("automation.timezone") }, h("input", { className: "plugin-automation-field", "aria-label": tr("automation.timezone"), required: true, value: draft.timezone, onChange: (event) => onChange({ ...draft, timezone: event.target.value }) })),
      h("div", { className: "plugin-automation-form-row" }, h(Checkbox, { label: tr("automation.once"), checked: !draft.recurring, onChange: (event) => onChange({ ...draft, recurring: !event.target.checked }) })));
  }

  function FieldRow({ label, children }) {
    return h("label", { className: "plugin-automation-form-row" }, h("span", null, label), h("span", { className: "plugin-automation-form-row-control" }, children));
  }

  function Group({ title, children }) {
    return h("section", { className: "plugin-automation-form-group" }, h("h3", { className: "plugin-automation-group-title" }, title), h("div", { className: "plugin-automation-form-card" }, children));
  }

  function draftFor(task) {
    return { title: task?.title || "", prompt: task?.prompt || "", schedule: task?.cron || "0 9 * * 1-5", timezone: task?.timezone || Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC", mode: task?.mode || "new_thread", heartbeat_thread_id: task?.heartbeat_thread_id || "", recurring: task ? task.recurring === true : true, workspace: task?.workspace_mode || "shared" };
  }

  function Editor({ tr, task, initial, workspace, threads, busy, error, onSave, onPause, onRemove, onClose, runs, readOnly }) {
    const [draft, setDraft] = React.useState(() => initial || draftFor(task));
    const [saved, setSaved] = React.useState(() => initial || draftFor(task));
    const [localError, setLocalError] = React.useState("");
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
      catch { setLocalError(`${tr("automation.timezone")}: ${draft.timezone}`); return; }
      const normalized = { ...draft, title: draft.title.trim() || draft.prompt.trim() };
      if (await onSave(normalized)) { setSaved(normalized); setDraft(normalized); }
    };
    return h("aside", { className: "plugin-automation-detail", ref: panelRef, "aria-label": task?.title || tr("automation.new"), onKeyDown: (event) => { if (event.key === "Escape" && !busy) { event.stopPropagation(); onClose(); } } },
      h("div", { className: "plugin-automation-detail-head" },
        h("span", { className: "plugin-automation-detail-status" }, !task ? tr("automation.new") : readOnly ? tr("automation.run.completed") : tr(task.paused ? "automation.paused" : "automation.filter.active")),
        task && !readOnly ? h(React.Fragment, null,
          h(Button, { variant: "ghost", disabled: busy, onClick: onPause }, tr(task.paused ? "automation.resume" : "automation.pause")),
          h("details", { className: "plugin-automation-more" }, h("summary", { "aria-label": tr("automation.more") }, "⋯"), h("div", { className: "plugin-automation-more-menu" }, h(Button, { variant: "danger", disabled: busy, onClick: onRemove }, tr("automation.remove"))))) : null,
        h("button", { type: "button", className: "plugin-automation-detail-close", disabled: busy, "aria-label": tr("automation.close"), onClick: onClose }, h(CloseIcon))),
      h("form", { className: "plugin-automation-form", onSubmit: save },
        h("fieldset", { disabled: busy || readOnly, style: { border: 0, padding: 0, margin: 0, minWidth: 0, display: "contents" } },
          h("div", { className: "plugin-automation-form-identity" },
            h("label", null, h("span", { className: "plugin-automation-sr" }, tr("automation.name")), h("input", { className: "plugin-automation-form-name", placeholder: tr("automation.placeholder.name"), value: draft.title, onChange: (event) => setDraft({ ...draft, title: event.target.value }) })),
            h("label", null, h("span", { className: "plugin-automation-sr" }, tr("automation.prompt")), h("textarea", { className: "plugin-automation-form-prompt", required: true, rows: 4, placeholder: tr("automation.placeholder.prompt"), value: draft.prompt, onChange: (event) => setDraft({ ...draft, prompt: event.target.value }) }))),
          h(Group, { title: tr("automation.group.details") },
            h("div", { className: "plugin-automation-form-row" }, h("span", null, tr("automation.field.target")), h("span", { className: "plugin-automation-form-row-control" }, h(TargetPicker, {
              tr, threads, disabled: busy || readOnly,
              value: { mode: draft.mode, threadId: draft.heartbeat_thread_id, threadTitle: threads.find((thread) => thread.id === draft.heartbeat_thread_id)?.title },
              onSelect: (target) => setDraft({ ...draft, mode: target.mode, heartbeat_thread_id: target.threadId || "", workspace: target.mode === "thread_heartbeat" ? "shared" : draft.workspace }),
            }))),
            h("div", { className: "plugin-automation-form-row" }, h("span", null, tr("automation.field.project")), h("span", { className: "plugin-automation-item-meta", title: workspace.root }, workspace.name || workspaceName(workspace.root))),
            h("div", { className: "plugin-automation-form-row" }, h(Checkbox, { label: tr("automation.field.isolation"), title: tr("automation.isolationHelp"), checked: draft.workspace === "worktree", disabled: draft.mode === "thread_heartbeat", onChange: (event) => setDraft({ ...draft, workspace: event.target.checked ? "worktree" : "shared" }) }))),
          h(Group, { title: tr("automation.group.schedule") }, h(ScheduleFields, { tr, draft, onChange: setDraft }))),
        error || localError ? h("div", { className: "plugin-automation-error", role: "alert" }, error || localError) : null,
        !readOnly ? h("div", { className: "plugin-automation-form-actions" },
          task ? h("span", { className: "plugin-automation-detail-status", role: "status" }, tr(dirty ? "automation.unsaved" : "automation.saved")) : null,
          h(Button, { variant: "ghost", disabled: busy, onClick: onClose }, tr("automation.cancel")),
          h(Button, { type: "submit", variant: "primary", disabled: busy || !draft.prompt.trim() || (!!task && !dirty) }, tr(task ? "automation.save" : "automation.create"))) : null),
      runs.length ? h("section", { className: "plugin-automation-history" }, h("h3", { className: "plugin-automation-group-title" }, tr("automation.history")), runs.slice(0, 5).map((run) => h("div", { key: run.id }, h("div", { className: "plugin-automation-history-row" }, h("span", null, tr(`automation.run.${runStatus(run)}`)), h("time", null, formatDateTime(run.triggered_at, draft.timezone))), run.error ? h("p", { className: "plugin-automation-error" }, run.error) : null))) : null);
  }

  function Catalog(props) {
    const tr = props.translate;
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
      h("div", { className: "plugin-automation-body", "data-panel": panelOpen ? "true" : "false" },
        h("section", { className: "plugin-automation-main", "aria-label": tr("automation.title") },
          h("div", { className: "plugin-automation-head" }, panelOpen ? null : h("h1", { className: "plugin-automation-title" }, tr("automation.title")),
            panelOpen ? h("div", { className: "plugin-automation-filters" }, ["all", "active", "paused", "completed"].map((value) => h("button", { key: value, className: "plugin-automation-filter", "aria-pressed": filter === value, onClick: () => setFilter(value) }, tr(value === "paused" ? "automation.paused" : value === "completed" ? "automation.run.completed" : `automation.filter.${value}`)))) : null,
            h(Button, { variant: "primary", disabled: busy || !workspace, onClick: () => create() }, tr("automation.create"))),
          h("div", { className: "plugin-automation-toolbar" },
            h("label", { className: "plugin-automation-search" }, h(SearchIcon), h("span", { className: "plugin-automation-sr" }, tr("automation.search")), h("input", { type: "search", value: query, placeholder: tr("automation.search"), onChange: (event) => setQuery(event.target.value) })),
            h("div", { className: "plugin-automation-workspace-picker" }, h(Select, { label: tr("automation.workspace"), value: workspaceID, disabled: busy || !workspaces.length, onChange: (event) => { epoch.current++; setWorkspaceID(event.target.value); setSelection(null); setTasks([]); setRuns([]); setError(""); } }, workspaces.length ? workspaces.map((item) => h("option", { key: item.id, value: item.id }, item.name || workspaceName(item.root))) : h("option", { value: "" }, tr("automation.workspaceNone"))))),
          !panelOpen ? h("div", { className: "plugin-automation-filters", style: { marginBottom: 14 } }, ["all", "active", "paused", "completed"].map((value) => h("button", { key: value, className: "plugin-automation-filter", "aria-pressed": filter === value, onClick: () => setFilter(value) }, tr(value === "paused" ? "automation.paused" : value === "completed" ? "automation.run.completed" : `automation.filter.${value}`)))) : null,
          error && !panelOpen ? h("p", { className: "plugin-automation-error", role: "alert" }, error) : null,
          !tasks.length && !completedTasks.length ? h(EmptyState, { className: "plugin-automation-empty", title: tr("automation.empty"), description: workspace ? undefined : tr("automation.workspaceNone") }) : !visible.length ? h("p", { className: "plugin-automation-filtered-empty" }, tr("automation.filter.empty")) : h("div", { className: "plugin-automation-list" }, visible.map((task) => {
            const done = !tasks.some((item) => item.id === task.id);
            const meta = [describeSchedule(task.cron, task.timezone, tr) || task.cron, done ? tr("automation.run.completed") : task.paused ? tr("automation.paused") : `${tr("automation.next")} ${formatDateTime(task.next_run_at, task.timezone) || "—"}`].join(" · ");
            return h("button", { key: task.id, className: "plugin-automation-item", "data-selected": selection?.id === task.id, "data-paused": task.paused, "aria-pressed": selection?.id === task.id, disabled: busy, onClick: () => { setError(""); setSelection({ kind: "task", id: task.id }); } },
              h("span", { className: "plugin-automation-item-dot", "data-run": runStatus(sortedRuns.find((run) => run.task_id === task.id)) }),
              h("span", { className: "plugin-automation-item-main" }, h("span", { className: "plugin-automation-item-title" }, task.title || task.prompt), h("span", { className: "plugin-automation-item-meta", title: `${task.cron} · ${task.timezone}` }, meta)));
          })),
          workspace ? h("section", { className: "plugin-automation-suggestions" }, h("h2", { className: "plugin-automation-group-title" }, tr("automation.suggestions")), ["brief", "review", "check"].map((key) => h("button", { key, className: "plugin-automation-item", disabled: busy, onClick: () => create(key) }, h("span", { className: "plugin-automation-item-main" }, h("span", { className: "plugin-automation-item-title" }, tr(`automation.template.${key}`)), h("span", { className: "plugin-automation-item-meta" }, tr(`automation.template.${key}.prompt`)))))) : null),
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
