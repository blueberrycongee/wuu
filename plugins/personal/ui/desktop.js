import { createSettingsPanels } from "./settings-panels.js";
import { sendMotionStyles, useSendMotion } from "./send-motion.js";
export async function activate(api) {
  const React = api.react, h = React.createElement;
  const { Button, TextInput, TextArea, ConversationMessage, ConversationComposer, ConversationView, ConversationAttachment, EmptyState, LoadingState, Checkbox, Select, Avatar } = api.ui;
  const route = { userSpaceId: "local", productDomain: "personal", compositionProfile: "personal" };
  const call = async (method, input) => {
    if (method !== "capture.send") return api.invokeRuntime(method, input, route);
    // Share material is normalized in the owner UI before admission. The exact
    // displayed prompt is the host user-input proof, not a server-side rewrite.
    if (!input || typeof input.request_id !== "string" || typeof input.prompt !== "string" || input.attachments !== undefined) throw new Error("Invalid shared input");
    const { agent } = await api.invokeRuntime("agents.ensure_default", {}, route);
    return api.invokeRuntime("agents.send", { agent_id: agent.agent_id, request_id: input.request_id, prompt: input.prompt }, { ...route, userInput: { requestId: input.request_id, prompt: input.prompt } });
  };
  const settingsPanels = createSettingsPanels(React, api.ui, call);
  api.registerStyle({ id: "personal-settings-panels", css: settingsPanels.styles });
  api.registerStyle({ id: "personal-send-motion", css: sendMotionStyles });
  const words = {
    "en": {
      "profile": "About twin",
      "rename": "Name your twin", "saveName": "Save", "savingName": "Saving…", "nameLabel": "Name", "invalidName": "Use 1–64 characters.", "nameSaveFailed": "Could not save the name. Please try again.",
      "ready": "Ready",
      "sendRefreshFailed": "Message accepted, but conversation history could not refresh. Refresh history instead of sending again.",
      "refreshHistory": "Refresh history",
      "thinking": "Thinking",
      "responding": "Replying",
      "reaction": "React", "agentReaction": "twin reacted",
      "historyFrom": "Sent from (local date)",
      "historyUntil": "Sent through (local date)",
      "historyDateHint": "Searches saved message times. The through date includes that whole local day. Older messages with unknown times are excluded when dates are set.",
      "unknownTimestamp": "Message time unknown",
      "moreHistory": "Load more matches",
      "viewOriginal": "View original message",
      "originalMessage": "Original message",
      "originalUnavailable": "This original message is not available in the returned history. Refresh and try again.",
      "unknown": "Not provided",
      "invalidDates": "The end date must not be before the start date.",
      "historySearch": "Search conversation",
      "searchHistory": "Search history",
      "searchExcerpts": "Matching conversation excerpts",
      "searchPartial": "These results are incomplete. Load more when available, or narrow your search.",
      "searchEmpty": "No matching excerpts were returned",
      "message": "Message",
      "you": "You",
      "notificationGrouping": "Group notifications",
      "notificationGroupingHint": "Group useful updates without delaying conversation replies.",
      "off": "Off",
      "oneMinute": "1 minute",
      "fiveMinutes": "5 minutes",
      "fifteenMinutes": "15 minutes",
      "attachmentLimit": "Choose up to 16 nonempty files, 20 MB total.",
      "invalidFile": "This filename or file type is not supported.",
      "readFile": "Read attachment",
      "download": "Download",
      "verifying": "Verifying attachment…",
      "invalidAttachment": "Attachment verification failed. Refresh and try again.",
      "oversizedAttachment": "This attachment exceeds the 20 MB download limit.",
      "today": "Today",
      "settings": "Settings",
      "placeholder": "Message",
      "stopped": "Stopped",
      "stopping": "Stopping · waiting for confirmation",
      "running": "Working",
      "waiting": "Waiting for you",
      "waiting_user": "Waiting for you",
      "waiting_external": "Waiting for an update",
      "quietFrom": "From",
      "quietTo": "Until",
      "timezone": "Time zone",
      "notifyToggle": "Useful updates",
      "quietToggle": "Pause notifications overnight",
      "completed": "Completed",
      "failed": "Needs attention",
      "queued": "Queued",
      "cancelled": "Stopped",
      "blocked": "Blocked",
      "draft": "Not started",
      "paused": "Paused",
      "active": "Active",
      "emptyChat": "What would you like to take care of?",
      "emptyChatText": "Make a plan, remember something, or just talk.",
      "quiet": "You're all caught up",
      "progress": "In progress",
      "recent": "Recent results",
      "requirements": "Request version",
      "outcome": "Result",
      "stopTask": "Stop this task",
      "openMainChat": "Open chat",
      "replies": "Replies", "replyUnavailable": "Original message unavailable", "replyLoading": "Loading original message…", "replyTaskContext": "Task context",
      "replyMessage": "Reply", "replyTask": "Reply to this task", "replyingMessage": "Replying to message", "replyingTask": "Replying to task", "cancelReply": "Cancel reply",
      "close": "Close",
      "back": "Back",
      "save": "Save changes",
      "hostUnavailable": "twin is not available on this host",
      "memoryHint": "Personal and Coding memory are logically separate. Shell and file access are not isolated by this setting.",
      "retry": "Try again",
      "process": "Task details",
      "trace": "View work history",
      "routines": "Routines",
      "nextRun": "Next",
      "pauseRoutine": "Pause routine",
      "resumeRoutine": "Resume routine",
      "conflict": "Needs clarification",
      "activity": "Recent activity",
      "outputs": "Recent outputs",
      "noOngoing": "Nothing in progress",
      "noOutputs": "Finished outputs will appear here",
      "showMore": "Show more",
      "showLess": "Show less",
      "calendarReview": "Review calendar proposals",
      "applyingChanges": "Applying your changes; waiting for the previous run to stop before continuing.",
      "calendarReadOnly": "Calendar changes are unavailable until an authorized connection is configured.",
      "calendarUnconnected": "Calendar is not connected.",
      "calendarProposed": "Waiting for your review",
      "confirmed": "Confirmed · awaiting execution",
      "calendarUncertain": "Outcome needs verification",
      "reconciliation": "The external outcome still needs checking. Stopping does not undo completed actions."
    },
    "zh": {
      "profile": "关于 twin",
      "rename": "给 twin 取名字", "saveName": "保存", "savingName": "保存中…", "nameLabel": "名字", "invalidName": "请输入 1–64 个字符。", "nameSaveFailed": "名字未保存，请重试。",
      "ready": "已就绪",
      "sendRefreshFailed": "消息已被接收，但记录暂时未刷新。刷新记录即可，无需重新发送。",
      "refreshHistory": "刷新记录",
      "thinking": "正在思考",
      "responding": "正在回复",
      "reaction": "表态", "agentReaction": "twin 的表态",
      "historyFrom": "发送日期从（本地日期）",
      "historyUntil": "发送日期至（本地日期）",
      "historyDateHint": "按已保存的消息时间搜索，结束日期包含本地当天。设置日期后，不计入时间未知的旧消息。",
      "unknownTimestamp": "消息时间未知",
      "moreHistory": "加载更多匹配",
      "viewOriginal": "查看原消息",
      "originalMessage": "原消息",
      "originalUnavailable": "返回的历史中没有这条原消息，请刷新后重试。",
      "unknown": "未提供",
      "invalidDates": "结束日期不能早于开始日期。",
      "historySearch": "搜索主对话",
      "searchHistory": "搜索历史",
      "searchExcerpts": "匹配的聊天片段",
      "searchPartial": "结果尚不完整。可以加载更多时继续查找，或缩小搜索范围。",
      "searchEmpty": "没有返回匹配的聊天片段",
      "message": "消息",
      "you": "你",
      "notificationGrouping": "合并通知",
      "notificationGroupingHint": "合并有用的进展通知，不延迟聊天回复。",
      "off": "关闭",
      "oneMinute": "1 分钟",
      "fiveMinutes": "5 分钟",
      "fifteenMinutes": "15 分钟",
      "attachmentLimit": "最多选择 16 个非空文件，总大小不超过 20 MB。",
      "invalidFile": "文件名或文件类型不受支持。",
      "readFile": "读取附件",
      "download": "下载",
      "verifying": "正在核验附件…",
      "invalidAttachment": "附件核验失败，请刷新后重试。",
      "oversizedAttachment": "附件超过 20 MB 下载上限。",
      "today": "今天",
      "settings": "设置",
      "placeholder": "发送消息",
      "stopped": "已停止",
      "stopping": "正在停止 · 等待宿主确认",
      "running": "处理中",
      "waiting": "等你决定",
      "waiting_user": "等你决定",
      "waiting_external": "等待外部更新",
      "quietFrom": "开始",
      "quietTo": "结束",
      "timezone": "时区",
      "notifyToggle": "接收有用的进展",
      "quietToggle": "在指定时段保持安静",
      "completed": "已完成",
      "failed": "需要处理",
      "queued": "等待开始",
      "cancelled": "已停止",
      "blocked": "受阻",
      "draft": "尚未开始",
      "paused": "已暂停",
      "active": "进行中",
      "emptyChat": "今天，想一起处理什么？",
      "emptyChatText": "安排一件事，记住一个想法，或者随便聊聊。",
      "quiet": "暂时没有新的待办",
      "progress": "正在进行",
      "recent": "最近完成",
      "requirements": "要求版本",
      "outcome": "结果",
      "stopTask": "停止这件事",
      "openMainChat": "打开聊天",
      "replies": "回复", "replyUnavailable": "原消息不可用", "replyLoading": "正在加载原消息…", "replyTaskContext": "事项上下文",
      "replyMessage": "引用", "replyTask": "回复这件事", "replyingMessage": "引用消息", "replyingTask": "回复事项", "cancelReply": "取消引用",
      "close": "关闭",
      "back": "返回",
      "save": "保存修改",
      "hostUnavailable": "当前宿主还不能启用 twin",
      "memoryHint": "Personal 与 Coding 的记忆逻辑隔离。此设置不提供 Shell 和文件访问的强隔离。",
      "retry": "重试",
      "process": "事项详情",
      "trace": "查看处理过程",
      "routines": "例行安排",
      "nextRun": "下次",
      "pauseRoutine": "暂停例行安排",
      "resumeRoutine": "恢复例行安排",
      "conflict": "待核对",
      "activity": "最近活动",
      "outputs": "最近输出",
      "noOngoing": "暂时没有正在进行的事",
      "noOutputs": "完成的输出会显示在这里",
      "showMore": "显示更多",
      "showLess": "收起",
      "calendarReview": "查看日历提案",
      "applyingChanges": "正在应用你的修改，等上一轮处理停止后继续",
      "calendarReadOnly": "尚未配置授权连接，当前无法修改日历。",
      "calendarUnconnected": "日历尚未连接。",
      "calendarProposed": "等待你确认",
      "confirmed": "已确认，等待执行",
      "calendarUncertain": "结果仍待核对",
      "reconciliation": "外部结果仍需核对。停止不会撤销已经发生的操作。"
    }
  };
  api.registerStyle({ id: "personal", css: `
    .mosaic { --mosaic-message-accent:#3566f0;position:relative;height:100%;min-height:0;display:flex;color:var(--ink); }
    .mosaic-main { flex:1;min-width:0;min-height:0;display:flex;flex-direction:column;overflow:hidden; }
    .mosaic-panel-layer { position:absolute;inset:0;z-index:30;pointer-events:none;overflow:hidden; }
    .mosaic-panel-layer[data-panel-phase="open"] { overflow:visible; }
    .mosaic-sidebar { position:absolute;right:8px;top:60px;width:min(300px, calc(100% - 16px));max-height:calc(100% - 68px);min-height:0;display:flex;flex-direction:column;overflow:hidden;border:0;border-radius:16px;background:var(--paper);box-shadow:0 0 0 .5px #00000026,0 3px 7.5px #0000000a,0 0 20px #0000000d;pointer-events:auto;opacity:0;transform:translateX(100%);transition:transform var(--mosaic-profile-motion-duration,300ms) cubic-bezier(.16,1,.3,1),opacity var(--mosaic-profile-motion-duration,300ms) cubic-bezier(.16,1,.3,1); }
    .mosaic-sidebar[data-panel-visible="true"] { opacity:1;transform:translateX(0); }
    .mosaic-sidebar[inert] { pointer-events:none; }
    .mosaic-panel-scroll { min-height:0;overflow:auto;padding:16px;overscroll-behavior:contain; }
    .mosaic-sidebar[hidden] { display:none; }
    .mosaic-profile-head { display:flex;align-items:center;gap:12px;margin-bottom:24px; }
    .mosaic-profile-copy { min-width:0;flex:1; }
    .mosaic-profile-name { font-size:calc(var(--font-ui) * 1.2);font-weight:600;line-height:1.45; }
    .mosaic-profile-copy .mosaic-status { margin-top:4px;font-size:calc(var(--font-ui) * 1.067);line-height:1.25; }
    .mosaic-profile-trigger { position:relative;width:40px;height:40px;border:0;background:transparent;color:inherit;display:grid;place-items:center;padding:0;border-radius:50%;cursor:pointer;font:inherit;-webkit-app-region:no-drag; }
    .mosaic-identity { position:relative;display:flex;flex-direction:column;align-items:center;gap:var(--space-1);z-index:31; }
    .mosaic-identity-label { position:relative;display:flex;flex-direction:column;align-items:center;gap:2px;width:max-content;min-width:0;max-width:240px;padding:6px 12px;border:1px solid var(--hairline);border-radius:16px;background:var(--paper);box-shadow:0 2px 6px #00000010;color:var(--ink);font:inherit;cursor:pointer;-webkit-app-region:no-drag; }
    .mosaic-identity-label strong { max-width:100%;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-size:15px;line-height:20px;font-weight:400; }
    .mosaic-identity-label small { max-width:100%;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-size:12px;line-height:16px;color:var(--ink-muted); }
    .mosaic[data-panel-open="true"] .mosaic-identity { visibility:hidden; }
    .mosaic[data-panel-open="true"] .mosaic-identity-label { visibility:hidden;opacity:0;pointer-events:none; }
    .mosaic-profile-name-button { border:0;background:transparent;padding:0;text-align:left;color:inherit;cursor:pointer;max-width:100%;overflow:hidden;text-overflow:ellipsis;white-space:nowrap; }
    .mosaic-name-backdrop { position:absolute;inset:0;z-index:60;display:grid;place-items:center;background:#00000026;padding:16px; }
    .mosaic-replies { display:flex;flex-direction:column;position:absolute;z-index:40;left:50%;bottom:12px;transform:translateX(-50%);box-sizing:border-box;width:calc(100% - 48px);max-width:calc(100% - 24px);max-height:calc(100% - 48px);border:1px solid var(--hairline);border-radius:36px;background:var(--paper);box-shadow:0 16px 32px -8px rgb(0 0 0 / .19);overflow:hidden; }
    .mosaic .mosaic-replies .plugin-conversation { display:flex;flex-direction:column;height:auto;min-height:0;flex:1 1 auto;max-height:none;overflow:hidden; }
    .mosaic .mosaic-replies .plugin-conversation-titlebar { display:none; }
    .mosaic-replies .title-block { display:none; }
    .mosaic-replies-close { position:absolute;top:16px;right:16px;z-index:6;display:flex;align-items:center;justify-content:center;flex:none;width:28px;height:28px;padding:0;border:1px solid var(--hairline);border-radius:50%;background:var(--paper);box-shadow:0 2px 8px rgb(0 0 0 / .06);color:var(--ink);cursor:pointer; }
    .mosaic-replies-close svg { width:16px;height:16px; }
    .mosaic-replies-close:hover { background:var(--surface-2);color:var(--ink); }
    .mosaic-replies .plugin-conversation-scroll { flex:1 1 auto;min-height:0;max-height:none;margin:0;overflow:auto;padding:20px 24px 12px; }
    .mosaic .mosaic-replies .plugin-conversation-scroll .plugin-conversation-flow.conversation-width { padding:0;--conversation-flow-bottom-space:0px; }
    .mosaic-replies .plugin-conversation-flow > :first-child { padding-right:36px; }
    .mosaic-replies .plugin-conversation-composer { flex:none;width:100%;padding:0 24px 16px;box-sizing:border-box; }
    .mosaic-replies .composer-wrap { margin:0;padding:0; }
    .mosaic-replies .composer-stack { width:100%;--composer-expanded-offset:0px; }
    .mosaic-replies .plugin-conversation-attachment:has(> img) > .conversation-file-card { display:none; }
    .mosaic-replies .plugin-conversation-attachment > img { max-height:none;height:auto;margin-top:0;border-radius:16px; }
    .mosaic-replies .bubble-time-toggle { display:none; }
    .mosaic-replies .plugin-conversation-message:first-of-type > .bubble-message-time { position:static;align-self:center;opacity:1;transform:none;width:auto;padding:0;margin:0 0 12px;text-align:center; }
    .mosaic-replies .plugin-conversation-message:first-of-type > .bubble-message-time[aria-hidden="true"] { visibility:visible; }
    .mosaic-reply-status { color:var(--ink-muted);margin:8px 0; }
    .mosaic .mosaic-draft-notice { color:var(--ink-muted);display:grid;gap:8px;padding:0 16px;margin:8px 0; }
    .mosaic .mosaic-draft-notice > p { display:flex;align-items:center;flex-wrap:wrap;gap:4px 8px;margin:0; }
    .mosaic .mosaic-draft-notice > p > span { flex:1 1 240px;min-width:0;overflow-wrap:anywhere; }
    .mosaic .mosaic-replies .mosaic-draft-notice { padding:0;margin:0 0 12px; }
    .mosaic-reply-task-context { margin:0;overflow-wrap:anywhere; }
    .mosaic-reply-task-context > span { color:var(--ink-muted);font-size:var(--font-sm); }
    .mosaic-reply-task-context > p { margin:8px 0 0; }
    @media(max-width:720px) { .mosaic-replies { width:calc(100% - 24px);border-radius:28px; } .mosaic-replies .plugin-conversation-scroll, .mosaic-replies .plugin-conversation-composer { padding-inline:16px; } }
    .mosaic-name-dialog { position:relative;width:min(360px,100%);max-height:100%;overflow:auto;min-height:450px;display:flex;flex-direction:column;align-items:center;gap:20px;padding:48px 32px 24px;border-radius:24px;background:var(--paper);box-shadow:0 12px 48px #00000026; }
    .mosaic-name-close { position:absolute;right:16px;top:12px;border:0;background:transparent;font-size:24px;color:var(--ink-muted);cursor:pointer; }
    .mosaic-name-input { width:100%;min-width:0;border:0;border-radius:12px;background:var(--surface-1);color:var(--ink);padding:12px 8px;text-align:center;font-size:32px;font-weight:600; }
    .mosaic-name-dialog [data-plugin-avatar] { margin:22px 0; }
    .mosaic-name-save { width:100%;margin-top:auto;border:0;border-radius:999px;min-height:40px;background:var(--ink);color:var(--paper);font:inherit;cursor:pointer; }
    .mosaic-name-save:disabled { opacity:.45;cursor:default; }
    .mosaic-name-error { margin:0;color:var(--danger,var(--ink));font-size:var(--font-ui-small); }
    /* The identity owns its intrinsic row so scrolled text cannot pass beneath it. */
    .mosaic-main > .plugin-conversation { grid-template-rows:auto minmax(0,1fr); }
    .mosaic[data-wuu-personal] .mosaic-main > .plugin-conversation > .plugin-conversation-titlebar { grid-template-columns:minmax(0,1fr);align-self:start;min-height:0;height:auto;padding:var(--space-2) var(--space-3); }
    .mosaic-main > .plugin-conversation > .plugin-conversation-titlebar > .title-block { display:none; }
    .mosaic-main > .plugin-conversation > .plugin-conversation-titlebar > .title-actions { width:100%;justify-content:center; }
    .mosaic .plugin-conversation-titlebar { position:relative;overflow:visible;z-index:10;border:0;background:transparent;backdrop-filter:none;box-shadow:none;pointer-events:none; }
    .mosaic .plugin-conversation-titlebar .mosaic-identity { pointer-events:auto; }
    .mosaic-main > .plugin-conversation > .plugin-conversation-scroll { mask-image:none; }
    .mosaic .plugin-conversation-titlebar .title-block { visibility:hidden; }
    .mosaic .plugin-conversation[data-presentation="bubbles"] { --session-composer-width:100%;--session-outer-width:100%; }
    .mosaic .plugin-conversation-message[data-bubble][data-role="user"] .bubble-message-content { --wuu-message-user-background:var(--mosaic-message-accent); }
    .mosaic-profile-trigger[aria-expanded="true"] { visibility:hidden; }
    .mosaic-profile-trigger:focus-visible { outline:2px solid var(--accent);outline-offset:2px; }
    .mosaic-profile-trigger .mosaic-avatar { width:40px;height:40px; }
    .mosaic-profile-head .mosaic-avatar { width:64px;height:64px; }
    .mosaic-sidebar .mosaic-task { align-items:center;min-height:28px;padding:4px 8px;margin:0 -8px;width:calc(100% + 16px);gap:8px;border-radius:8px;font-size:calc(var(--font-ui) * .9333); }
    .mosaic-sidebar .mosaic-task-mark { width:20px;height:20px; }
    .mosaic-sidebar .mosaic-nav-icon { width:16px;height:16px; }
    .mosaic-sidebar .mosaic-section { margin-bottom:20px; }
    .mosaic-sidebar .mosaic-section:last-child { margin-bottom:0; }
    .mosaic-sidebar .mosaic-section h2 { margin-bottom:8px;font-size:calc(var(--font-ui) * .9333); }
    .mosaic-sidebar [data-action^="show-more-"] { min-height:28px;padding:4px 0;gap:8px;color:var(--ink-muted);font-size:calc(var(--font-ui) * .9333); }
    .mosaic-sidebar [data-action^="show-more-"]::before { content:"⌄";width:20px;text-align:center; }
    @media (prefers-reduced-motion:reduce) { .mosaic-sidebar { transition:none; } }
    :root[data-appearance-motion="reduce"] .mosaic-sidebar { transition:none; }
    .mosaic-sidebar .mosaic-task-title { overflow:hidden;text-overflow:ellipsis;white-space:nowrap; }
    .mosaic-sidebar .mosaic-task-meta { display:none; }
    .mosaic-sidebar .mosaic-detail .mosaic-task-meta,.mosaic-sidebar .mosaic-routines .mosaic-task-meta { display:block; }
    .mosaic-sidebar .mosaic-task-state { font-size:12px; }
    .mosaic-sidebar .mosaic-task-state[data-state="running"],.mosaic-sidebar .mosaic-task-state[data-state="queued"] { color:var(--accent); }
    .mosaic-sidebar .mosaic-detail { min-height:320px; }
    .mosaic-sidebar .mosaic-detail-content { overflow:auto; }
    .mosaic-section { margin-bottom:var(--space-6); }
    .mosaic-section h2,.mosaic-section h3 { display:flex;align-items:center;gap:var(--space-3);font:inherit;color:var(--ink-muted);margin:0 0 var(--space-3); }
    .mosaic-count { padding:0 var(--space-1);border-radius:var(--radius-xs);background:var(--surface-1);font-size:var(--font-ui-small); }
    .mosaic-task { width:100%;display:flex;align-items:flex-start;gap:var(--space-3);padding:var(--space-3) var(--space-2);margin:0 0 var(--space-2);border:0;border-radius:var(--radius-sm);background:transparent;text-align:left;color:var(--ink);font:inherit;cursor:pointer; }
    .mosaic-task:hover,.mosaic-task:focus-visible { background:var(--surface-1); }
    .mosaic-task-mark { flex:none;display:grid;place-items:center;width:22px;height:24px;color:var(--ink-muted); }
    .mosaic-nav-icon { width:18px;height:18px;flex:none; }
    .mosaic-task-copy { flex:1;min-width:0; }
    .mosaic-task-title { display:block;overflow-wrap:anywhere; }
    .mosaic-task-meta { display:block;color:var(--ink-muted);margin:var(--space-1) 0 0;overflow-wrap:anywhere; }
    .mosaic-task-state { flex:none;min-width:10px;color:var(--ink-muted); }
    .mosaic-task-state[data-state="waiting_user"] { color:var(--warning,var(--ink)); }
    .mosaic-avatar { width:32px;height:32px;flex:none;display:grid;place-items:center; }
    .mosaic-status { display:flex;align-items:center;gap:var(--space-2);color:var(--ink-muted); }
    .mosaic-status-dot { width:6px;height:6px;background:var(--accent);border-radius:50%; }
    .mosaic-actions { display:flex;gap:var(--space-2);align-items:center;flex-wrap:wrap; }
    .mosaic-error { display:flex;align-items:center;gap:var(--space-3);padding:var(--space-3) var(--space-5);overflow-wrap:anywhere; }
    .mosaic-main > .mosaic-error:first-child { margin-top:var(--window-titlebar-height);flex:none; }
    .mosaic-error > span { flex:1;min-width:0; }
    .mosaic-empty { display:grid;place-items:center;min-height:240px;padding:var(--space-6); }
    .mosaic-empty .plugin-ui-empty-state { max-width:440px; }
    .mosaic-eyebrow { color:var(--ink-muted);margin:0 0 var(--space-3); }
    .mosaic-panel-scroll:has(> .mosaic-detail) { overflow:hidden; }
    .mosaic-detail { display:flex;flex-direction:column;height:100%;min-height:0; }
    .mosaic-detail-head { display:flex;align-items:center;gap:var(--space-2);margin-bottom:var(--space-5);flex:none; }
    .mosaic-detail-title { flex:1;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font:inherit; }
    .mosaic-detail-content { flex:1;min-height:0;overflow:auto;overflow-wrap:anywhere; }
    .mosaic-detail-content > p { overflow-wrap:anywhere; }
    .mosaic-detail-actions { flex:none;display:flex;gap:var(--space-2);align-items:center;flex-wrap:wrap;padding-top:var(--space-4); }
    .mosaic-detail-content .mosaic-section { margin-top:var(--space-5); }
    .mosaic-memory { padding:var(--space-3) 0;border-bottom:1px solid var(--hairline-soft); }
    .mosaic-memory p { margin:0 0 var(--space-2); }

    .mosaic-routine-backdrop { position:absolute;inset:0;z-index:65;background:var(--modal-backdrop-bg);display:grid;place-items:center;padding:var(--space-5); }
    .mosaic-routine-dialog { width:min(540px,100%);max-height:100%;min-height:0;display:flex;flex-direction:column;border:1px solid var(--hairline-soft);border-radius:var(--radius-md);background:var(--surface-1);color:var(--ink);box-shadow:var(--shadow-modal);font-size:var(--font-ui);overflow:hidden; }
    .mosaic-routine-head { display:flex;gap:var(--space-2);align-items:center;padding:var(--space-3) var(--space-4);flex:none; }
    .mosaic-routine-head h2 { margin:0;font:inherit;font-weight:var(--weight-medium);overflow-wrap:anywhere;min-width:0; }
    .mosaic-routine-head .plugin-ui-button { display:grid;place-items:center;flex:none;width:var(--control-field-height);min-height:var(--control-field-height);padding:0; }
    .mosaic-routine-head svg { width:var(--icon-size);height:var(--icon-size); }
    .mosaic-routine-body { padding:0 var(--space-5) var(--space-4);overflow:auto;min-height:0;overscroll-behavior:contain; }
    .mosaic-routine-section { margin-top:var(--space-4); }
    .mosaic-routine-section h3 { font:inherit;color:var(--ink-tertiary);margin:0 0 var(--space-2);font-weight:var(--weight-regular); }
    .mosaic-routine-card { background:var(--paper);border:1px solid var(--hairline-soft);border-radius:var(--radius-sm);padding:var(--card-padding);margin:0; }
    .mosaic-routine-active { padding:var(--space-2) var(--card-padding); }
    .mosaic-routine-active .plugin-ui-checkbox { width:100%; }
    .mosaic-routine-dialog .plugin-ui-field-label { font-weight:var(--weight-regular); }
    .mosaic-routine-instruction { white-space:pre-wrap;overflow-wrap:anywhere;line-height:1.6; }
    .mosaic-routine-schedule { padding-block:0; }
    .mosaic-routine-schedule > div { display:grid;grid-template-columns:auto minmax(0,1fr);gap:var(--space-4);padding:var(--space-2) 0;align-items:center;border-bottom:1px solid var(--hairline-soft); }
    .mosaic-routine-schedule > div:last-child { border:0; }
    .mosaic-routine-schedule dt { color:var(--ink-soft); }
    .mosaic-routine-schedule dd { margin:0;text-align:right;overflow-wrap:anywhere; }
    .mosaic-routine-meta { color:var(--ink-tertiary);font-size:var(--font-sm);line-height:1.5;margin:var(--space-2) 0 0; }
    .mosaic-routine-link { display:flex;justify-content:space-between;align-items:center;gap:var(--space-3);width:100%;margin-top:var(--space-3);font:inherit;color:var(--ink);cursor:pointer;text-align:left; }
    .mosaic-routine-link { padding-block:var(--space-3); }
    .mosaic-routine-link:hover { background:var(--field-hover-bg); }
    .mosaic-receipt { display:flex;align-items:center;justify-content:flex-end;gap:var(--space-2);font-size:12px;color:var(--ink-muted);padding-top:var(--space-1);flex-wrap:wrap; }
    .mosaic-routine-footer { display:flex;justify-content:flex-end;gap:var(--space-2);padding:0 var(--space-5) var(--space-4);flex:none; }
    .mosaic-routine-link.plugin-ui-button { justify-content:flex-start;background:var(--paper);border:1px solid var(--hairline-soft);padding:var(--space-3) var(--card-padding); }
    .mosaic-routine-editor-identity { display:grid;gap:var(--space-4);margin-bottom:var(--space-5); }
    .mosaic-routine-editor-identity .plugin-ui-input,.mosaic-routine-editor-identity .plugin-ui-textarea { background:var(--paper); }
    .mosaic-routine-editor-identity .plugin-ui-textarea { min-height:8em;max-height:18em;line-height:1.6; }
    .mosaic-routine-fields { background:var(--paper);border:1px solid var(--hairline-soft);border-radius:var(--radius-sm);padding:0 var(--card-padding); }
    .mosaic-routine-fields .mosaic-routine-field + .mosaic-routine-field { border-top:1px solid var(--hairline-soft); }
    .mosaic-routine-fields .plugin-ui-field { display:grid;grid-template-columns:minmax(90px,1fr) minmax(0,1.6fr);gap:var(--space-3);align-items:center;min-height:calc(var(--control-field-height) + var(--space-3)); }
    .mosaic-routine-fields .plugin-ui-field-label { color:var(--ink-soft); }
    .mosaic-routine-fields .plugin-ui-input { border-color:transparent;background:transparent;text-align:right;padding-inline:var(--space-2); }
    .mosaic-routine-fields .plugin-ui-input:hover { background:var(--field-hover-bg); }
    .mosaic-routine-fields .plugin-ui-select { appearance:none;padding-right:var(--space-5);background-image:linear-gradient(45deg,transparent 50%,var(--ink-tertiary) 50%),linear-gradient(135deg,var(--ink-tertiary) 50%,transparent 50%);background-position:calc(100% - 10px) 50%,calc(100% - 6px) 50%;background-size:4px 4px;background-repeat:no-repeat; }
    .mosaic-routine-advanced { margin-top:var(--space-4); }
    .mosaic-routine-advanced > summary { cursor:pointer;color:var(--ink-soft);padding:var(--space-2) 0; }
    .mosaic-routine-advanced[open] > summary { margin-bottom:var(--space-2); }
    .mosaic-routine-error { color:var(--danger);overflow-wrap:anywhere; }
    .mosaic-routine-history { list-style:none;padding:0 var(--card-padding);margin:var(--space-3) 0 0;background:var(--paper);border:1px solid var(--hairline-soft);border-radius:var(--radius-sm); }
    .mosaic-routine-history li { padding:var(--space-3) 0;border-bottom:1px solid var(--hairline-soft);overflow-wrap:anywhere; }
    .mosaic-routine-history li:last-child { border:0; }
    .mosaic-routine-history li p { white-space:pre-wrap;line-height:1.6;margin-bottom:0; }
    .mosaic-routine-run-head { display:flex;justify-content:space-between;gap:var(--space-3);flex-wrap:wrap;align-items:center;min-height:var(--control-field-height); }
    button.mosaic-routine-run-head { width:100%;border:0;padding:0;background:transparent;color:var(--ink);font:inherit;text-align:left;cursor:pointer; }
    button.mosaic-routine-run-head:hover { color:var(--ink-strong);text-decoration:underline; }
    .mosaic-routine-history small,.mosaic-routine-history summary { color:var(--ink-tertiary);font-size:var(--font-sm); }
    .mosaic-routine-history summary { cursor:pointer; }
    .mosaic-routine-history [data-state="completed"] { color:var(--success); }
    .mosaic-routine-history [data-state="failed"] { color:var(--danger); }
    .mosaic-routine-dialog :focus-visible { outline:2px solid var(--wuu-accent);outline-offset:2px; }
    @media(max-width:560px) { .mosaic-routine-backdrop { padding:var(--space-2); } .mosaic-routine-body { padding-inline:var(--space-4); } .mosaic-routine-footer { padding-inline:var(--space-4); } }
    .mosaic-routines summary { display:flex;align-items:center;gap:var(--space-2);cursor:pointer;color:var(--ink-muted); }
    .mosaic-routines summary.mosaic-task { color:var(--ink);list-style:none; }
    .mosaic-routines summary.mosaic-task::-webkit-details-marker { display:none; }
    .mosaic-routine-next { flex:none;max-width:42%;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;color:var(--ink-muted);font-size:var(--font-ui-small); }
    .mosaic-routine-detail { padding:4px 0 12px 32px; }
    .mosaic-settings { display:flex;flex-direction:column;gap:var(--space-5);padding:var(--space-5);max-width:700px; }
    .mosaic-settings p { color:var(--ink-muted); }
    .mosaic-history-search form { display:flex;flex-wrap:wrap;align-items:flex-end;gap:var(--space-3);margin-block:var(--space-3); }
    .mosaic-history-search form > label { flex:1;min-width:170px; }
    .mosaic-attachments { display:flex;flex-direction:column;gap:var(--space-2); }
    .mosaic-calendar-proposals { margin-block:var(--space-5);padding-top:var(--space-3);border-top:1px solid var(--hairline-soft); }
    .mosaic-calendar-proposals p { margin:var(--space-2) 0;color:var(--ink-muted); }
    @media(max-width:480px) { .mosaic-sidebar { right:8px;width:calc(100% - 16px); } .mosaic-panel-scroll { padding:16px; } }
  ` });
  const icons = { clock: "M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18 M12 7v5l-3 3", progress: "M12 3a9 9 0 1 0 9 9 M12 3a9 9 0 0 1 9 9", activity: "M21 11.5a8.5 8.5 0 0 1-12.4 7.6L3 21l1.9-5.6A8.5 8.5 0 1 1 21 11.5", output: "M6 3h8l4 4v14H6z M14 3v5h4 M9 12h6 M9 16h6", panel: "M4 4h16v16H4z M15 4v16", today: "M4 5h16v15H4z M8 3v4 M16 3v4 M4 10h16", tasks: "M9 6h11 M9 12h11 M9 18h11 M3 6l1 1 2-2 M3 12l1 1 2-2 M3 18l1 1 2-2", memory: "M8 4a4 4 0 0 0-4 4 4 4 0 0 0 0 8 4 4 0 0 0 8 2V6a4 4 0 0 0-4-2 M16 4a4 4 0 0 1 4 4 4 4 0 0 1 0 8 4 4 0 0 1-8 2 M8 9h4 M12 15h4", settings: "M12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8 M12 3v2 M12 19v2 M3 12h2 M19 12h2 M5.6 5.6 7 7 M17 17l1.4 1.4 M5.6 18.4 7 17 M17 7l1.4-1.4" };
  const icon = key => h("svg", { className: "mosaic-nav-icon", viewBox: "0 0 24 24", fill: "none", stroke: "currentColor", strokeWidth: 1.5, strokeLinecap: "round", strokeLinejoin: "round", "aria-hidden": true }, h("path", { d: icons[key] }));
  const aliveTask = task => !["completed", "cancelled", "stopped", "failed", "archived"].includes(task.state);

  const fileLimit = 20 * 1024 * 1024, fileCountLimit = 16;
  const safeFilename = name => typeof name === "string" && name.length > 0 && name.length <= 255 && !/[\\/\x00-\x1f\x7f]/.test(name) && ![".", ".."].includes(name);
  const safeMime = mime => typeof mime === "string" && /^[a-zA-Z0-9!#$&^_.+-]+\/[a-zA-Z0-9!#$&^_.+-]+$/.test(mime);
  const digestBytes = async bytes => Array.from(new Uint8Array(await crypto.subtle.digest("SHA-256", bytes))).map(value => value.toString(16).padStart(2, "0")).join("");
  const decodeBytes = data => Uint8Array.from(atob(data), character => character.charCodeAt(0));
  const readFile = file => new Promise((resolve, reject) => { const reader = new FileReader(); reader.onerror = () => reject(reader.error || new Error("File read failed")); reader.onabort = () => reject(new Error("File read cancelled")); reader.onload = () => resolve(String(reader.result).split(",")[1]); reader.readAsDataURL(file); });
  const AttachmentOpenContext = React.createContext(() => {});
  function Attachment({ agentId, taskId, requirementsRevision, seq, attachment, t, autoLoad = false }) {
    const onOpen = React.useContext(AttachmentOpenContext);
    const [url, setURL] = React.useState(""), [loading, setLoading] = React.useState(false), [error, setError] = React.useState("");
    const live = React.useRef(true), blobURL = React.useRef(""), pendingRead = React.useRef(null);
    React.useEffect(() => { live.current = true; return () => { live.current = false; if (blobURL.current) URL.revokeObjectURL(blobURL.current); }; }, []);
    const raster = ["image/png", "image/jpeg", "image/gif", "image/webp", "image/avif"].includes(attachment.mime_type);
    const readVerifiedBlob = async (cancelled = () => api.signal.aborted) => {
      if (attachment.size > fileLimit) throw new Error(t("oversizedAttachment"));
      if (!Number.isSafeInteger(attachment.size) || attachment.size <= 0 || !safeFilename(attachment.name) || !safeMime(attachment.mime_type) || !/^[a-f0-9]{64}$/.test(attachment.sha256)) throw new Error(t("invalidAttachment"));
      const total = Math.ceil(attachment.size / 3) * 4, chunks = [];
      let offset = 0;
      while (offset < total) {
        if (cancelled()) throw new Error("Attachment read cancelled");
        const part = await call(taskId ? "tasks.attachments.read" : "agents.attachments.read", { ...(taskId ? { task_id: taskId, requirements_revision: requirementsRevision } : { agent_id: agentId }), seq, kind: attachment.kind, index: attachment.index, sha256: attachment.sha256, offset });
        if (part.offset !== offset || part.total !== total || part.sha256 !== attachment.sha256 || part.name !== attachment.name || part.content_type !== attachment.mime_type || typeof part.data !== "string" || !part.data.length || part.data.length > 128 * 1024 || part.data.length % 4 || part.data.length + offset > total || !/^[A-Za-z0-9+/]*={0,2}$/.test(part.data)) throw new Error(t("invalidAttachment"));
        chunks.push(part.data); offset += part.data.length;
      }
      const bytes = decodeBytes(chunks.join(""));
      if (bytes.length !== attachment.size || await digestBytes(bytes) !== attachment.sha256) throw new Error(t("invalidAttachment"));
      if (cancelled()) throw new Error("Attachment read cancelled");
      return new Blob([bytes], { type: raster ? attachment.mime_type : "application/octet-stream" });
    };
    const read = async () => {
      if (blobURL.current) return blobURL.current;
      setLoading(true); setError("");
      try {
        const blob = await readVerifiedBlob(() => !live.current || api.signal.aborted);
        if (!live.current || api.signal.aborted) return;
        blobURL.current = URL.createObjectURL(blob); setURL(blobURL.current); return blobURL.current;
      } catch (cause) { if (live.current) setError(cause instanceof Error ? cause.message : String(cause)); }
      finally { if (live.current) setLoading(false); }
    };
    const load = () => {
      if (!pendingRead.current) pendingRead.current = read().finally(() => { pendingRead.current = null; });
      return pendingRead.current;
    };
    React.useEffect(() => { if (autoLoad && raster) void load(); }, [autoLoad, raster]);
    return h(ConversationAttachment, { onOpen, resourceId: JSON.stringify([taskId ? "task" : "agent", taskId || agentId, requirementsRevision, seq, attachment.kind, attachment.index, attachment.sha256]), name: attachment.name, mediaType: attachment.mime_type, size: attachment.size, url, loading, error, loadLabel: t(loading ? "verifying" : "readFile"), downloadLabel: t("download"), onLoad: load, onRead: readVerifiedBlob });
  }
  function TaskTrace({ taskId, taskRevision, requirementsRevision, requirements, t }) {
    const [records, setRecords] = React.useState(null), [error, setError] = React.useState(""), [revision, setRevision] = React.useState(requirementsRevision);
    const generation = React.useRef(0);
    React.useEffect(() => {
      const epoch = ++generation.current; setError(""); setRecords(null);
      void call("tasks.history", { task_id: taskId, requirements_revision: revision }).then(result => { if (epoch === generation.current) setRecords(result.records || []); }).catch(cause => { if (epoch === generation.current) setError(String(cause)); });
      return () => { generation.current++; };
    }, [taskId, taskRevision, revision]);
    return h("div", { "data-task-trace": true, "aria-busy": records === null && !error }, requirements?.length > 1 && h(Select, { label: t("requirements"), value: revision, onChange: event => setRevision(Number(event.target.value)) }, requirements.map(item => h("option", { key: item.revision, value: item.revision }, item.revision))), error ? h("p", { role: "alert" }, error) : records?.filter(record => !record.hidden && !(record.role === "user" && record.read_only) && (record.attachments?.length || ["user", "assistant"].includes(record.role))).map(record => h(ConversationMessage, {
      key: `${revision}:${record.seq}`, id: `task:${taskId}:${revision}:${record.seq}`, timestamp: record.at, role: record.role === "user" ? "user" : "assistant",
      content: ["user", "assistant"].includes(record.role) ? record.display_content || record.content || "" : "", linkPolicy: "external-only", messageActions: "none",
      attachments: record.attachments?.length ? h("div", { className: "mosaic-attachments" }, record.attachments.map(attachment => h(Attachment, { key: `${revision}:${record.seq}:${attachment.kind}:${attachment.index}:${attachment.sha256}`, taskId, requirementsRevision: revision, seq: record.seq, attachment, t }))) : undefined,
    })));
  }
  function AgentHistorySearch({ agentId, name, t }) {
    const [query, setQuery] = React.useState(""), [from, setFrom] = React.useState(""), [until, setUntil] = React.useState("");
    const [result, setResult] = React.useState(null), [error, setError] = React.useState(""), [searching, setSearching] = React.useState(false);
    const [original, setOriginal] = React.useState(null), [originalError, setOriginalError] = React.useState(""), [reading, setReading] = React.useState(false);
    const generation = React.useRef(0), originalGeneration = React.useRef(0);
    React.useEffect(() => () => { generation.current++; originalGeneration.current++; }, []);
    const invalidDates = Boolean(from && until && from > until);
    const reset = () => { generation.current++; originalGeneration.current++; setSearching(false); setReading(false); setResult(null); setOriginal(null); setError(""); setOriginalError(""); };
    const search = async cursor => {
      if (!query.trim() || searching || invalidDates) return;
      const epoch = ++generation.current; setSearching(true); setError("");
      if (!cursor) { originalGeneration.current++; setResult(null); setOriginal(null); setOriginalError(""); setReading(false); }
      const bounds = {};
      if (from) bounds.time_min = new Date(`${from}T00:00:00`).toISOString();
      if (until) { const end = new Date(`${until}T00:00:00`); end.setDate(end.getDate() + 1); bounds.time_max = end.toISOString(); }
      try {
        const found = await call("agents.search", { agent_id: agentId, query: query.trim(), limit: 25, ...bounds, ...(cursor ? { cursor } : {}) });
        if (epoch === generation.current) setResult(current => ({ ...found, matches: cursor ? [...new Map([...(current?.matches || []), ...(found.matches || [])].map(item => [item.seq, item])).values()] : found.matches || [] }));
      } catch (cause) { if (epoch === generation.current) setError(String(cause)); }
      finally { if (epoch === generation.current) setSearching(false); }
    };
    const readOriginal = async seq => {
      const epoch = ++originalGeneration.current; setOriginal(null); setOriginalError(""); setReading(true);
      try {
        const history = await call("agents.history", { agent_id: agentId, start_seq: seq, end_seq: seq });
        const record = history.records?.find(item => item.seq === seq && ["user", "assistant"].includes(item.role));
        if (!record) throw new Error(t("originalUnavailable"));
        if (epoch === originalGeneration.current) setOriginal(record);
      } catch (cause) { if (epoch === originalGeneration.current) setOriginalError(String(cause)); }
      finally { if (epoch === originalGeneration.current) setReading(false); }
    };
    const timestamp = item => h("p", { className: "mosaic-eyebrow", "data-history-timestamp": item.at || "unknown" }, item.at || t("unknownTimestamp"));
    const matches = result?.matches?.filter(item => ["user", "assistant"].includes(item.role)) || [];
    return h("section", { className: "mosaic-history-search" },
      h("form", { onSubmit: event => { event.preventDefault(); void search(); } },
        h(TextInput, { label: t("historySearch"), "data-agent-history-query": true, value: query, onChange: event => { reset(); setQuery(event.target.value); } }),
        h(TextInput, { type: "date", label: t("historyFrom"), "data-history-date": "from", value: from, onChange: event => { reset(); setFrom(event.target.value); } }),
        h(TextInput, { type: "date", label: t("historyUntil"), "data-history-date": "until", value: until, onChange: event => { reset(); setUntil(event.target.value); } }),
        h(Button, { type: "submit", "data-action": "search-agent-history", disabled: searching || !query.trim() || invalidDates }, t("searchHistory"))),
      h("p", { className: "mosaic-eyebrow" }, t("historyDateHint")), invalidDates && h("p", { role: "alert" }, t("invalidDates")), error && h("p", { role: "alert" }, error),
      result && h("section", { "data-history-search-results": true }, h("h2", null, t("searchExcerpts")),
        (result.has_more || result.budget_exhausted) && h("p", { "data-history-search-partial": true, role: "status" }, t("searchPartial")),
        !matches.length && h("p", null, t("searchEmpty")), matches.map(item => h("article", { key: item.seq, className: "mosaic-memory" },
          h("p", { className: "mosaic-eyebrow" }, `${item.role === "user" ? t("you") : name} · ${t("message")} ${item.seq}`), timestamp(item),
          h(ConversationMessage, { role: item.role, content: item.excerpt || "", linkPolicy: "external-only" }),
          h(Button, { "data-history-hit": item.seq, disabled: reading, onClick: () => readOriginal(item.seq) }, t("viewOriginal")))),
        result.next_cursor && h(Button, { "data-action": "more-agent-history", disabled: searching, onClick: () => search(result.next_cursor) }, t("moreHistory"))),
      originalError && h("p", { role: "alert", "data-history-original-error": true }, originalError),
      original && h("article", { className: "mosaic-memory", "data-history-original": original.seq }, h("h2", null, t("originalMessage")), timestamp(original),
        h(ConversationMessage, { role: original.role, content: original.display_content || original.content || "", linkPolicy: "external-only" }),
        original.attachments?.map(attachment => h(Attachment, { key: `${agentId}:${original.seq}:${attachment.kind}:${attachment.index}:${attachment.sha256}`, agentId, seq: original.seq, attachment, t }))));
  }
  // Recovery stays in this desktop profile and Personal user space. Never persist
  // attachment bytes/paths, quoted history, live streams or runtime configuration.
  const recoveryPrefix = `wuu.personal.draftRecovery.v1:${route.userSpaceId}:`;
  const recoveryAge = 24 * 60 * 60 * 1000, recoveryLimit = 64 * 1024;
  const conversations = new Map(), draftListeners = new Set();
  const genuiDrafts = new Map();
  const recoveryKey = id => recoveryPrefix + encodeURIComponent(id);
  const readRecovery = raw => {
    if (!raw || raw.length > recoveryLimit) return null;
    const value = JSON.parse(raw);
    if (value.version !== 1 || typeof value.prompt !== "string" || value.prompt.length > 32 * 1024 ||
        !Number.isFinite(value.updatedAt) || value.updatedAt > Date.now() || Date.now() - value.updatedAt >= recoveryAge ||
        typeof value.attachmentsMissing !== "boolean") return null;
    const pending = value.pending;
    if (pending && (typeof pending.requestID !== "string" || pending.requestID.length > 128 ||
        pending.prompt !== value.prompt || typeof pending.fileKey !== "string" ||
        (pending.relationKey !== undefined && typeof pending.relationKey !== "string"))) return null;
    return { prompt: value.prompt, attachmentsMissing: value.attachmentsMissing, pending: pending ? { prompt: pending.prompt, fileKey: pending.fileKey, relationKey: pending.relationKey, requestID: pending.requestID, genuiContext: pending.genuiContext, uncertain: true } : null, recoveryUpdatedAt: value.updatedAt };
  };
  const draftFor = id => {
    if (!conversations.has(id)) {
      const draft = { prompt: "", files: [], pending: null, submitting: false, queueIds: new Set(), dequeuedIds: new Set() };
      if (id) try {
        const raw = localStorage.getItem(recoveryKey(id));
        let saved;
        try { saved = readRecovery(raw); } catch { /* A damaged entry cannot become a draft. */ }
        if (saved) Object.assign(draft, saved);
        else if (raw) localStorage.removeItem(recoveryKey(id));
      } catch { draft.storageError = true; draft.recoveryUnread = true; }
      conversations.set(id, draft);
    }
    return conversations.get(id);
  };
  const saveDraft = (id, draft) => {
    if (!id) return;
    try {
      const key = recoveryKey(id), missing = Boolean(draft.attachmentsMissing || draft.files.length);
      if (!draft.prompt && !missing) localStorage.removeItem(key);
      else {
        const saved = { version: 1, prompt: draft.prompt, attachmentsMissing: missing,
          updatedAt: draft.recoveryUpdatedAt || Date.now(),
          pending: draft.pending?.prompt === draft.prompt ? draft.pending : null };
        const raw = JSON.stringify(saved);
        if (draft.prompt.length > 32 * 1024 || raw.length > recoveryLimit) {
          localStorage.removeItem(key);
          throw new Error("Draft exceeds recovery limit");
        }
        localStorage.setItem(key, raw);
      }
      // Per-context entries avoid one window overwriting another conversation.
      // Bound retention and total storage without extending unchanged draft ages.
      const entries = [];
      for (const storedKey of Object.keys(localStorage).filter(value => value.startsWith(recoveryPrefix))) {
        const raw = localStorage.getItem(storedKey);
        let saved;
        try { saved = readRecovery(raw); } catch { /* Discard malformed recovery only. */ }
        if (!saved) localStorage.removeItem(storedKey);
        else entries.push({ key: storedKey, at: saved.recoveryUpdatedAt });
      }
      entries.sort((a, b) => b.at - a.at);
      for (const entry of entries.slice(20)) localStorage.removeItem(entry.key);
      draft.storageError = false;
    } catch { draft.storageError = true; }
  };
  const updateDraft = (id, update) => {
    let draft = draftFor(id);
    const editing = ["prompt", "files", "attachmentsMissing"].some(key => Object.hasOwn(update, key));
    if (draft.recoveryUnread && !editing) {
      const replyTarget = draft.replyTarget;
      conversations.delete(id);
      draft = draftFor(id);
      if (replyTarget) draft.replyTarget = replyTarget;
      if (draft.recoveryUnread) { Object.assign(draft, update); draftListeners.forEach(listener => listener()); return; }
    }
    if (editing) draft.recoveryUnread = false;
    if (["prompt", "files", "replyTarget"].some(key => Object.hasOwn(update, key))) draft.editVersion = (draft.editVersion || 0) + 1;
    if (Object.hasOwn(update, "replyTarget") && draft.pending?.relationKey !== JSON.stringify(update.replyTarget?.relation)) draft.pending = null;
    if (["prompt", "files", "attachmentsMissing"].some(key => Object.hasOwn(update, key))) draft.recoveryUpdatedAt = Date.now();
    Object.assign(draft, update);
    saveDraft(id, draft);
    draftListeners.forEach(listener => listener());
  };
  const streams = new Map();
  let mainSessionID = "";
  const streamFor = id => { if (!streams.has(id)) streams.set(id, { items: new Map(), completedItems: new Set(), completedTurns: new Set(), version: 0, running: false }); return streams.get(id); };
  const seenEvents = new WeakSet();
  api.onHostEvent(event => {
    if (!event || typeof event !== "object" || seenEvents.has(event) || event.kind !== "notification") return;
    seenEvents.add(event);
    const params = event.message?.params, session = params?.thread_id || params?.session_id || params?.message?.thread_id || params?.queued?.thread_id;
    if (!mainSessionID || session !== mainSessionID) return;
    const stream = streamFor(session), method = event.message.method, item = params.item, itemID = item?.id || params.item_id;
    const itemKey = `${params.turn_id}:${itemID}`;
    const queues = draftFor(session).queueIds;
    stream.version++;
    if (method === "turn/queued") { const id = params.message?.id || params.queued?.id; if (id && !draftFor(session).dequeuedIds.has(id)) queues.add(id); }
    if (["turn/started", "turn/dequeued"].includes(method) && params.queue_id) { queues.delete(params.queue_id); draftFor(session).dequeuedIds.add(params.queue_id); }
    if (method === "turn/started") { stream.running = true; stream.activeTurnID = params.turn?.id || params.turn_id; }
    if (["turn/completed", "turn/error"].includes(method)) {
      const turnID = params.turn?.id || params.turn_id;
      stream.completedTurns.add(turnID);
      // Durable sequence IDs are stamped after item/completed. The terminal
      // turn is the persistence barrier and owns the final item identities.
      if (params.turn?.items_view === "full") {
        const retained = new Set(params.turn.items.map(item => `${turnID}:${item.id}`));
        for (const [key, message] of stream.items) if (message.turn_id === turnID && !retained.has(key)) stream.items.delete(key);
      }
      for (const item of params.turn?.items || []) {
        if (item.type !== "agent_message") continue;
        const key = `${turnID}:${item.id}`, previous = stream.items.get(key);
        if (!previous) continue;
        stream.items.set(key, { ...previous, seq: item.seq, relation: item.relation, source_id: item.source_id, content: item.text ?? previous.content, streaming: false });
        stream.completedItems.add(key);
      }
      for (const message of stream.items.values()) if (message.turn_id === turnID) message.streaming = false;
      if (!stream.activeTurnID || stream.activeTurnID === turnID) { stream.running = false; stream.activeTurnID = undefined; }
      if (draftFor(session).stopTarget === turnID) updateDraft(session, { stopState: undefined, stopTarget: undefined });
    }
    if (method === "item/removed") { stream.items.delete(itemKey); stream.completedItems.add(itemKey); }
    if (["item/agentMessage/delta", "item/agentMessage/replace"].includes(method)) {
      if (!itemID || stream.completedItems.has(itemKey) || stream.completedTurns.has(params.turn_id)) return;
      const previous = stream.items.get(itemKey) || { id: itemID, turn_id: params.turn_id, content: "" };
      const content = method.endsWith("replace") ? params.text ?? params.delta ?? "" : previous.content + (params.delta || "");
      stream.items.set(itemKey, { ...previous, content, streaming: true }); stream.running = true; stream.activeTurnID = params.turn_id;
    }
    if (["item/started", "item/completed"].includes(method) && item?.type === "agent_message") {
      if (stream.completedTurns.has(params.turn_id)) return;
      if (stream.completedItems.has(itemKey) && method === "item/started") return;
      const previous = stream.items.get(itemKey);
      if (method === "item/completed") stream.completedItems.add(itemKey);
      stream.items.set(itemKey, { id: itemID, turn_id: params.turn_id, seq: item.seq, relation: item.relation, source_id: item.source_id, content: method === "item/started" && previous ? previous.content : item.text ?? previous?.content ?? "", streaming: method !== "item/completed" });
    }
    draftListeners.forEach(listener => listener());
  });
  function PersonalPreferences({ locale }) {
    const t = key => (locale?.startsWith("zh") ? words.zh : words.en)[key] || key;
    const [preferences, setPreferences] = React.useState(null), [error, setError] = React.useState(""), [saving, setSaving] = React.useState(false);
    React.useEffect(() => { let active = true; call("settings.get").then(result => { if (active) setPreferences(result.settings); }).catch(cause => { if (active) setError(String(cause)); }); return () => { active = false; }; }, []);
    const save = async () => { if (saving) return; setSaving(true); setError(""); try { const result = await call("settings.update", { expected_revision: preferences.revision, notifications: preferences.notifications, quiet_hours: preferences.quiet_hours, notification_batch_seconds: preferences.notification_batch_seconds ?? 0 }); setPreferences(result.settings); } catch (cause) { setError(String(cause)); } finally { setSaving(false); } };
    return h("section", { className: "mosaic-settings", "data-personal-preferences": true }, error && h("p", { role: "alert" }, error), !preferences ? h(LoadingState, { label: "twin" }) : h(React.Fragment, null,
      h(Checkbox, { label: t("notifyToggle"), checked: preferences.notifications, onChange: event => setPreferences({ ...preferences, notifications: event.target.checked }) }),
      h(Select, { label: t("notificationGrouping"), description: t("notificationGroupingHint"), "data-notification-batch": true, value: preferences.notification_batch_seconds ?? 0, onChange: event => setPreferences({ ...preferences, notification_batch_seconds: Number(event.target.value) }) }, [[0, "off"], [60, "oneMinute"], [300, "fiveMinutes"], [900, "fifteenMinutes"]].map(([value, label]) => h("option", { key: value, value }, t(label)))),
      h(Checkbox, { label: t("quietToggle"), checked: preferences.quiet_hours.enabled, onChange: event => setPreferences({ ...preferences, quiet_hours: { ...preferences.quiet_hours, enabled: event.target.checked } }) }),
      h("div", { className: "mosaic-actions" }, h(TextInput, { type: "time", label: t("quietFrom"), value: preferences.quiet_hours.start, onChange: event => setPreferences({ ...preferences, quiet_hours: { ...preferences.quiet_hours, start: event.target.value } }) }), h(TextInput, { type: "time", label: t("quietTo"), value: preferences.quiet_hours.end, onChange: event => setPreferences({ ...preferences, quiet_hours: { ...preferences.quiet_hours, end: event.target.value } }) })),
      h(TextInput, { label: t("timezone"), value: preferences.quiet_hours.timezone, onChange: event => setPreferences({ ...preferences, quiet_hours: { ...preferences.quiet_hours, timezone: event.target.value } }) }),
      h(Button, { "data-action": "save-notifications", disabled: saving, onClick: save }, t("save")), h("p", null, t("memoryHint"))), h(settingsPanels.Connections), h(settingsPanels.Review, { locale }));
  }
  let identityAgent;
  const identityListeners = new Set();
  const publishIdentity = value => {
    if (!value || (identityAgent && identityAgent.agent_id === value.agent_id && identityAgent.revision > value.revision)) return identityAgent;
    identityAgent = value;
    api.setNavigationTitle?.("personal", Array.from(value.name?.trim() || "twin").slice(0, 64).join(""));
    identityListeners.forEach(listener => listener(value));
    return value;
  };
  const routineStatus = (status, locale, t) => status === "needs_authorization" ? locale.startsWith("zh") ? "需要重新授权" : "Authorization required" : status === "active" ? locale.startsWith("zh") ? "已启用" : "Enabled" : t(status);
  const routineSchedule = (routine, locale) => {
    const zh = locale.startsWith("zh"), fields = routine.cron?.trim().split(/\s+/), zone = routine.timezone || "UTC";
    if (!fields) { const at = new Date(routine.run_at); return Number.isFinite(at.getTime()) ? `${zh ? "仅一次 · " : "Once · "}${new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeStyle: "short", timeZone: zone }).format(at)}` : (zh ? "未设置" : "Not scheduled"); }
    const [minute, hour, dom, month, dow] = fields;
    if (fields.length === 5 && /^\d+$/.test(minute) && /^\d+$/.test(hour) && dom === "*" && month === "*") {
      const time = `${hour.padStart(2, "0")}:${minute.padStart(2, "0")}`;
      if (dow === "*") return `${zh ? "每天" : "Every day"} ${time}`;
      if (/^[0-6]$/.test(dow)) { const day = new Intl.DateTimeFormat(locale, { weekday: "long", timeZone: "UTC" }).format(new Date(Date.UTC(2026, 0, 4 + Number(dow)))); return `${zh ? "每周" : "Every "}${day} ${time}`; }
      if (dow === "1-5") return `${zh ? "工作日" : "Weekdays"} ${time}`;
    }
    return `${zh ? "自定义重复规则" : "Custom recurrence"} · ${routine.cron}`;
  };
  function RoutineDialog({ routine, locale, t, onClose, onChanged, onOpenTask }) {
    const [view, setView] = React.useState("details"), [form, setForm] = React.useState(null), [busy, setBusy] = React.useState(false), [error, setError] = React.useState(""), [history, setHistory] = React.useState(null), [historyLoading, setHistoryLoading] = React.useState(false);
    const dialog = React.useRef(null), pending = React.useRef(false), historyVersion = React.useRef(0), alive = React.useRef(true);
    const zh = locale.startsWith("zh"), label = (en, cn) => zh ? cn : en;
    const stamp = value => { const parsed = new Date(value); return Number.isFinite(parsed.getTime()) ? new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeStyle: "short", timeZone: routine.timezone }).format(parsed) : label("Time unknown", "时间未知"); };
    React.useEffect(() => () => { alive.current = false; historyVersion.current++; }, []);
    React.useEffect(() => { const body = dialog.current?.querySelector(".mosaic-routine-body"); if (body) body.scrollTop = 0; dialog.current?.querySelector(view === "edit" ? '[name="title"]' : '[data-action="back-routine"]')?.focus({ preventScroll: true }); }, [view]);
    const beginEdit = () => {
      const fields = routine.cron?.trim().split(/\s+/), simple = fields?.length === 5 && /^\d+$/.test(fields[0]) && /^\d+$/.test(fields[1]) && fields[2] === "*" && fields[3] === "*";
      const instant = new Date(routine.run_at || Date.now()), parts = Object.fromEntries(new Intl.DateTimeFormat("en-CA", { timeZone: routine.timezone, year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hourCycle: "h23", timeZoneName: "longOffset" }).formatToParts(instant).map(part => [part.type, part.value]));
      const localTime = `${parts.year}-${parts.month}-${parts.day}T${parts.hour}:${parts.minute}`, offset = parts.timeZoneName.replace("GMT", "") || "+00:00";
      setForm({ title: routine.title, prompt: routine.prompt, timezone: routine.timezone, kind: routine.kind, missed_policy: routine.missed_policy || "run_once", expected_revision: routine.revision, schedule_type: routine.run_at ? "once" : simple && fields[4] === "*" ? "daily" : simple && /^[0-6]$/.test(fields[4]) ? "weekly" : "cron", time: simple ? `${fields[1].padStart(2,"0")}:${fields[0].padStart(2,"0")}` : "09:00", weekday: simple && /^[0-6]$/.test(fields[4]) ? fields[4] : "1", cron: routine.cron || "0 9 * * *", run_at: localTime, utc_offset: offset });
      setError(""); setView("edit");
    };
    const mutateRoutine = async (method, input) => {
      if (pending.current) return;
      pending.current = true; setBusy(true); setError("");
      try { const result = await call(method, { routine_id: routine.routine_id, ...input }); if (!alive.current) return; onChanged(result.routine); setView("details"); setForm(null); }
      catch (cause) { if (alive.current) setError(String(cause.message || cause)); }
      finally { pending.current = false; if (alive.current) setBusy(false); }
    };
    const save = event => {
      event.preventDefault(); if (pending.current) return;
      try {
        new Intl.DateTimeFormat(locale, { timeZone: form.timezone.trim() });
        const values = { title: form.title.trim(), prompt: form.prompt.trim(), timezone: form.timezone.trim(), kind: form.kind, missed_policy: form.missed_policy, expected_revision: form.expected_revision };
        if (!values.title || !values.prompt) throw new Error(label("Title and instruction are required.", "请填写标题和完整指令。"));
        if (form.schedule_type === "once") { const at = `${form.run_at}:00${form.utc_offset.trim()}`; if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/.test(form.run_at) || !/^[+-](?:0\d|1[0-4]):[0-5]\d$/.test(form.utc_offset.trim()) || !Number.isFinite(Date.parse(at))) throw new Error(label("Choose a date, time and valid UTC offset such as +08:00.", "请选择日期、时间和有效的 UTC 偏移，例如 +08:00。")); values.run_at = new Date(at).toISOString(); }
        else if (form.schedule_type === "cron") { if (form.cron.trim().split(/\s+/).length !== 5) throw new Error(label("Use a five-field cron expression.", "请填写五段式 cron 表达式。")); values.cron = form.cron.trim(); }
        else { if (!/^([01]\d|2[0-3]):[0-5]\d$/.test(form.time)) throw new Error(label("Choose a valid time.", "请选择有效时间。")); const [hour, minute] = form.time.split(":").map(Number); values.cron = `${minute} ${hour} * * ${form.schedule_type === "weekly" ? form.weekday : "*"}`; }
        void mutateRoutine("routines.update", values);
      } catch (cause) { setError(String(cause.message || cause)); }
    };
    const loadHistory = async (more = false) => {
      const version = ++historyVersion.current; setHistoryLoading(true); setError(""); setView("history");
      try { const result = await call("routines.history", { routine_id: routine.routine_id, ...(more && history?.cursor ? { cursor: history.cursor } : {}) }); if (alive.current && version === historyVersion.current) setHistory(current => more && current ? { ...result, events: [...new Map([...current.events, ...result.events].map(item => [item.event_id, item])).values()], tasks: [...new Map([...current.tasks, ...result.tasks].map(item => [item.task_id, item])).values()], skipped_records: (current.skipped_records || 0) + (result.skipped_records || 0) } : result); }
      catch (cause) { if (alive.current && version === historyVersion.current) setError(String(cause.message || cause)); }
      finally { if (alive.current && version === historyVersion.current) setHistoryLoading(false); }
    };
    const back = () => { if (pending.current) { onClose(); return; } historyVersion.current++; setError(""); if (view === "details") onClose(); else { setView("details"); setForm(null); } };
    const field = (name, title, props = {}) => h("div", { className: "mosaic-routine-field" }, h(TextInput, { label: title, name, value: form[name], disabled: busy, onChange: e => setForm({ ...form, [name]: e.target.value }), ...props }));
    const select = (name, title, choices) => h("div", { className: "mosaic-routine-field" }, h(Select, { label: title, name, value: form[name], disabled: busy, onChange: e => setForm({ ...form, [name]: e.target.value }) }, choices.map(([value, text]) => h("option", { key: value, value }, text))));
    const stateLabel = state => ({ completed: label("Succeeded", "已完成"), failed: label("Failed", "失败"), cancelled: label("Cancelled", "已取消"), interrupted: label("Interrupted", "已中断"), discarded: label("Discarded", "未执行"), dispatched: label("Dispatched", "已派发"), pending: label("Pending", "等待处理"), unknown: label("Unverified", "尚未核实") }[state] || t(state));
    const rows = history ? [...(history.tasks || []).map(task => ({ id: task.task_id, task, kind: "task", at: task.created_at, state: task.state, error: task.error, output: typeof task.result === "string" ? task.result : task.result?.summary })), ...(history.events || []).filter(event => event.kind === "reminder" || !(history.tasks || []).some(task => task.request_id === event.event_id)).map(event => ({ id: event.event_id, kind: event.kind, at: event.started_at || event.created_at || event.scheduled_at, state: event.state || "unknown", error: event.error, output: event.output }))].sort((a,b) => (Date.parse(b.at) || 0) - (Date.parse(a.at) || 0)) : [];
    return h("div", { className: "mosaic-routine-backdrop" }, h("section", { ref: dialog, className: "mosaic-routine-dialog", role: "dialog", "aria-modal": true, "aria-label": routine.title, "data-routine-detail": routine.routine_id, tabIndex: -1, onKeyDown: event => {
      if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); back(); }
      if (event.key === "Tab") { const nodes = [...dialog.current.querySelectorAll('button:not(:disabled),input:not(:disabled),textarea:not(:disabled),select:not(:disabled),summary,a[href]')].filter(node => node.tabIndex >= 0 && (!node.closest('details:not([open])') || node.tagName === 'SUMMARY')); const first = nodes[0], last = nodes.at(-1); if (event.shiftKey && (document.activeElement === first || document.activeElement === dialog.current)) { event.preventDefault(); last?.focus(); } else if (!event.shiftKey && (document.activeElement === last || document.activeElement === dialog.current)) { event.preventDefault(); first?.focus(); } }
    } },
      h("header", { className: "mosaic-routine-head" }, h(Button, { variant: "ghost", type: "button", "data-action": "back-routine", "aria-label": view === "details" || busy ? t("close") : t("back"), onClick: back }, h("svg", { viewBox: "0 0 24 24", fill: "none", stroke: "currentColor", strokeWidth: 1.5, "aria-hidden": true }, h("path", { d: view === "details" || busy ? "M6 6l12 12M18 6 6 18" : "M15 5l-7 7 7 7" }))), h("h2", null, view === "edit" ? label("Edit routine", "编辑例行安排") : view === "history" ? label("Run history", "运行记录") : routine.title)),
      h("div", { className: "mosaic-routine-body" }, busy && h("p", { role: "status" }, label("Saving… Closing this window will not cancel the request.", "正在保存…关闭此窗口不会取消请求。")), error && h("div", { role: "alert", className: "mosaic-routine-error" }, h("p", null, error), view === "edit" && h(Button, { variant: "ghost", disabled: busy, "data-action": "reload-routine", onClick: async () => { if (pending.current) return; pending.current = true; setBusy(true); try { const result = await call("routines.list"); const latest = result.routines.find(item => item.routine_id === routine.routine_id); if (alive.current && latest) { onChanged(latest); setForm(null); setView("details"); setError(""); } } catch (cause) { if (alive.current) setError(String(cause.message || cause)); } finally { pending.current = false; if (alive.current) setBusy(false); } } }, label("Discard draft and reload latest", "放弃草稿，加载最新版本"))),
        view === "details" ? h(React.Fragment, null,
          h("div", { className: "mosaic-routine-card mosaic-routine-active" }, h(Checkbox, { label: routineStatus(routine.status, locale, t), role: "switch", "aria-checked": routine.status === "active", checked: routine.status === "active", "aria-label": label("Enabled", "已启用"), "data-action": "toggle-routine", disabled: busy || routine.cancellation_pending || !["active","paused"].includes(routine.status), onChange: () => mutateRoutine(routine.status === "active" ? "routines.pause" : "routines.resume", { expected_revision: routine.revision }) })),
          routine.cancellation_pending && h("p", { role: "status" }, t("applyingChanges")), routine.last_error && h("p", { role: "alert" }, routine.last_error),
          routine.authorization?.expires_at && h("p", { role: "status" }, label("Unattended authorization expires: ", "无人值守授权到期："), stamp(routine.authorization.expires_at)),
          routine.status === "needs_authorization" && h(React.Fragment, null,
            h("p", null, label("This schedule is paused. Renewing starts from the next scheduled time; missed runs are not replayed.", "此安排已暂停。重新授权后从下一次计划时间开始，不会补执行过期期间错过的任务。")),
            h(Button, { "data-action": "authorize-routine", disabled: busy || routine.cancellation_pending, onClick: () => mutateRoutine("routines.authorize", { expected_revision: routine.revision }) }, label("Renew authorization", "重新授权"))),
          h("section", { className: "mosaic-routine-section" }, h("h3", null, label("Instruction", "完整指令")), h("div", { className: "mosaic-routine-card mosaic-routine-instruction" }, routine.prompt)),
          h("section", { className: "mosaic-routine-section" }, h("h3", null, label("Schedule", "执行计划")), h("dl", { className: "mosaic-routine-card mosaic-routine-schedule" },
            h("div", null, h("dt", null, label("Repeats", "重复规则")), h("dd", null, routineSchedule(routine, locale))), h("div", null, h("dt", null, t("timezone")), h("dd", null, routine.timezone)), h("div", null, h("dt", null, label("Next run", "下次执行")), h("dd", null, routine.status === "active" && routine.binding?.next_run_at ? stamp(routine.binding.next_run_at) : routine.status === "active" ? label("Awaiting scheduler", "等待调度确认") : routineStatus(routine.status, locale, t)))), h("p", { className: "mosaic-routine-meta" }, routine.kind === "task" ? label("Task", "任务") : label("Reminder", "提醒"), " · ", routine.missed_policy === "skip" ? label("Skip missed runs", "错过时跳过") : label("Catch up once after a missed run", "错过后补执行一次"))),
          h("button", { type: "button", className: "mosaic-routine-card mosaic-routine-link", "data-action": "routine-history", onClick: () => loadHistory() }, label("Run history", "运行记录"), h("span", { "aria-hidden": true }, "→")), h(Button, { variant: "ghost", className: "mosaic-routine-card mosaic-routine-link", "data-action": "edit-routine", disabled: busy || routine.cancellation_pending || ["stopping", "stopped"].includes(routine.status), onClick: beginEdit }, label("Edit routine", "编辑例行安排"))) : view === "history" ? h(React.Fragment, null,
            h("p", { className: "mosaic-eyebrow" }, routine.title, " · ", routine.timezone), h(Button, { variant: "ghost", disabled: historyLoading, "data-action": "refresh-routine-history", onClick: () => loadHistory() }, label("Refresh", "刷新")), historyLoading ? h("p", { role: "status" }, label("Loading run history…", "正在加载运行记录…")) : !error && !rows.length ? h("p", null, history?.has_more ? label("No runs in this page. More history is available.", "当前页没有运行记录，可继续加载。") : label("No runs recorded yet.", "暂无运行记录。")) : null,
            history?.has_more && h("p", { className: "mosaic-eyebrow" }, label("Loaded records are sorted by time. Load more to see earlier or other records.", "已加载记录按时间排序；加载更多可查看更早或其他记录。")), history?.skipped_records > 0 && h("p", { role: "status" }, label("Some unreadable historical records were skipped.", "部分无法读取的历史记录已跳过。")), history?.has_more && h(Button, { "data-action": "more-routine-history", disabled: historyLoading, onClick: () => loadHistory(true) }, label("Load more history", "加载更多记录")), h("ol", { className: "mosaic-routine-history" }, rows.map(row => h("li", { key: `${row.kind}:${row.id}`, "data-run-state": row.state }, h(row.task ? "button" : "div", { className: "mosaic-routine-run-head", ...(row.task ? { type: "button", "data-routine-task-id": row.id, onClick: () => onOpenTask(row.task), "aria-label": `${stamp(row.at)} · ${stateLabel(row.state)} · ${t("process")}` } : {}) }, h("time", null, stamp(row.at)), h("span", { "data-state": row.state }, stateLabel(row.state))), h("small", null, row.kind === "task" ? label("Task", "任务") : row.kind === "reminder" ? label("Reminder", "提醒") : row.kind === "calendar" ? label("Calendar trigger", "日历触发") : label("Legacy run", "历史运行")), row.output && h("details", null, h("summary", null, label("Result", "查看结果")), h("p", null, row.output)), row.error && h("p", { className: "mosaic-routine-error" }, row.error))))) : h("form", { id: "mosaic-routine-editor", onSubmit: save },
            h("div", { className: "mosaic-routine-editor-identity" }, field("title", label("Title", "标题"), { required: true }), h(TextArea, { label: label("Instruction", "完整指令"), name: "prompt", required: true, rows: 6, value: form.prompt, disabled: busy, onChange: e => setForm({ ...form, prompt: e.target.value }) })),
            h("section", { className: "mosaic-routine-section" }, h("h3", null, label("Schedule", "执行计划")), h("div", { className: "mosaic-routine-fields" }, select("schedule_type", label("Repeat", "重复"), [["daily", label("Every day", "每天")],["weekly",label("Every week", "每周")],["cron",label("Custom cron", "自定义 cron")], ...(!routine.source ? [["once",label("Once", "仅一次")]] : [])]),
            ["daily","weekly"].includes(form.schedule_type) && field("time", label("Time", "时间"), { type: "time", required: true }), form.schedule_type === "weekly" && select("weekday", label("Day", "星期"), Array.from({ length: 7 }, (_, day) => [String(day), new Intl.DateTimeFormat(locale, { weekday: "long", timeZone: "UTC" }).format(new Date(Date.UTC(2026,0,4+day)))])), form.schedule_type === "cron" && field("cron", label("Cron", "Cron"), { required: true }), form.schedule_type === "once" && h(React.Fragment, null, field("run_at", label("Date and time", "日期和时间"), { type: "datetime-local", required: true }), field("utc_offset", label("UTC offset", "UTC 偏移"), { required: true, placeholder: "+08:00" }))), h("p", { className: "mosaic-routine-meta" }, form.timezone, form.schedule_type === "once" ? label(" · Check the offset if this date crosses a daylight-saving change.", " · 若跨越夏令时切换，请核对偏移。") : "")),
            h("details", { className: "mosaic-routine-advanced", "data-routine-advanced": true }, h("summary", null, label("Advanced settings", "高级设置")), h("div", { className: "mosaic-routine-fields" }, field("timezone", t("timezone"), { placeholder: "Asia/Shanghai" }), select("kind", label("Type", "类型"), routine.source ? [["task",label("Task", "任务")]] : [["task",label("Task", "任务")],["reminder",label("Reminder", "提醒")]]), select("missed_policy", label("If missed", "错过执行时"), [["run_once",label("Run once when available", "补执行一次")],["skip",label("Skip", "跳过")]]))),
            h("p", { className: "mosaic-routine-meta" }, label("Saving stops the previous run. Completed actions are not undone.", "保存会停止之前的运行，已完成的操作不会撤销。")))),
      view === "edit" && h("footer", { className: "mosaic-routine-footer", "data-view": view }, h(React.Fragment, null, h(Button, { variant: "ghost", "data-action": "cancel-routine-edit", disabled: busy, onClick: back }, label("Cancel", "取消")), h(Button, { variant: "primary", type: "submit", form: "mosaic-routine-editor", "data-action": "save-routine", disabled: busy }, busy ? t("savingName") : t("save"))))));
  }

  function RenameDialog({ agent, t, onSave, onClose }) {
    const [name, setName] = React.useState(agent.name || "twin"), [saving, setSaving] = React.useState(false), [error, setError] = React.useState("");
    const input = React.useRef(null), dialog = React.useRef(null), alive = React.useRef(true);
    const trimmed = name.trim(), valid = trimmed.length > 0 && Array.from(trimmed).length <= 64, changed = trimmed !== (agent.name || "twin").trim();
    React.useEffect(() => { alive.current = true; input.current?.focus(); input.current?.select(); return () => { alive.current = false; }; }, []);
    React.useEffect(() => { (saving ? dialog.current : input.current)?.focus(); }, [saving]);
    const save = async event => {
      event.preventDefault(); if (saving || !valid || !changed) return;
      setSaving(true); setError("");
      try { await onSave(trimmed); }
      catch (cause) { if (alive.current) { setSaving(false); setError(`${t("nameSaveFailed")} ${cause instanceof Error ? cause.message : String(cause)}`); } }
    };
    return h("div", { className: "mosaic-name-backdrop", onPointerDown: event => { if (!saving && event.target === event.currentTarget) onClose(); } }, h("form", { ref: dialog, className: "mosaic-name-dialog", tabIndex: -1, role: "dialog", "aria-modal": true, "aria-label": t("rename"), onSubmit: save, onKeyDown: event => {
      if (event.key === "Escape") { event.stopPropagation(); event.preventDefault(); if (!saving) onClose(); }
      if (event.key === "Tab") { const controls = [...dialog.current.querySelectorAll("button:not(:disabled),input:not(:disabled)")]; const first = controls[0], last = controls[controls.length - 1]; if (!controls.length) { event.preventDefault(); dialog.current.focus(); return; } if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); } else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); } }
    } }, h("button", { type: "button", className: "mosaic-name-close", disabled: saving, "aria-label": t("close"), onClick: onClose }, "×"), h("input", { ref: input, className: "mosaic-name-input", value: name, disabled: saving, "aria-label": t("nameLabel"), "aria-invalid": !valid, onChange: event => { setName(event.target.value); setError(""); } }), h(Avatar, { size: 160, motion: "ambient" }), !valid && h("p", { className: "mosaic-name-error", role: "status" }, t("invalidName")), error && h("p", { className: "mosaic-name-error", role: "alert" }, error), h("button", { type: "submit", className: "mosaic-name-save", disabled: saving || !valid || !changed }, saving ? t("savingName") : t("saveName"))));
  }
  const replySources = new Map();
  function useReplySource({ reference, records, sessionID, agentID }) {
    const key = `${agentID}:${reference?.session_id}:${reference?.seq}`;
    const valid = reference?.session_id === sessionID && Number.isSafeInteger(reference?.seq) && reference.seq > 0;
    const visible = valid ? records.find(record => record.seq === reference.seq && ["user", "assistant"].includes(record.role)) : undefined;
    const [resolved, setResolved] = React.useState(null), [attempt, setAttempt] = React.useState(0);
    React.useEffect(() => {
      if (!valid || visible) return;
      let current = true;
      if (!replySources.has(key)) {
        const request = call("agents.history", { agent_id: agentID, start_seq: reference.seq, end_seq: reference.seq })
          .then(result => result.records?.find(record => record.seq === reference.seq && ["user", "assistant"].includes(record.role)) ?? null)
          .catch(() => null).then(record => { if (!record && replySources.get(key) === request) replySources.delete(key); return record; });
        replySources.set(key, request);
      }
      void replySources.get(key).then(record => { if (current) setResolved({ key, record }); });
      return () => { current = false; };
    }, [key, valid, visible, attempt]);
    return { record: visible || (resolved?.key === key ? resolved.record : undefined), loading: valid && !visible && resolved?.key !== key,
      retry: valid ? () => { setResolved(null); setAttempt(value => value + 1); } : undefined };
  }
  function ReplyReference({ reference, records, sessionID, agentID, t, onOpen }) {
    const { record, loading, retry } = useReplySource({ reference, records, sessionID, agentID });
    const excerpt = record ? record.display_content || record.content || record.attachments?.map(item => item.name).join(", ") || t("message") : t(loading ? "replyLoading" : "replyUnavailable");
    return h("button", { type: "button", className: "plugin-reply-reference", "data-reply-reference": true, "data-unavailable": !record && !loading, disabled: loading || (!record && !retry), title: record ? t("viewOriginal") : retry && !loading ? t("retry") : excerpt, onClick: event => { event.currentTarget.focus({ preventScroll: true }); if (record) onOpen(record); else retry?.(); } }, h("svg", { viewBox: "0 0 24 24", fill: "none", stroke: "currentColor", strokeWidth: 1.5, "aria-hidden": true }, h("path", { d: "M4 5v5a3 3 0 0 0 3 3h13m-5-5 5 5-5 5" })), h("span", null, excerpt, !record && !loading && retry ? ` · ${t("retry")}` : ""));
  }
  function ReplyOriginal({ reference, records, sessionID, agentID, t, renderMessage }) {
    const { record, loading, retry } = useReplySource({ reference, records, sessionID, agentID });
    return record ? renderMessage(record, true) : h("p", { className: "mosaic-reply-status", role: "status" }, t(loading ? "replyLoading" : "replyUnavailable"), !loading && retry && h(Button, { onClick: retry }, t("retry")));
  }
  function PersonalHome({ locale, host }) {
    const t = key => (locale?.startsWith("zh") ? words.zh : words.en)[key] || key;
    const [agent, setAgent] = React.useState(null), [records, setRecords] = React.useState([]), [historyInfo, setHistoryInfo] = React.useState(null);
    const [tasks, setTasks] = React.useState([]), [routines, setRoutines] = React.useState([]), [selection, setSelection] = React.useState(null);
    const [routineID, setRoutineID] = React.useState(null), routineTrigger = React.useRef(null);
    const selectedRoutine = routines.find(item => item.routine_id === routineID);
    const closeRoutine = () => { setRoutineID(null); requestAnimationFrame(() => { if (routineTrigger.current?.isConnected) routineTrigger.current.focus({ preventScroll: true }); }); };
    const changeRoutine = value => setRoutines(current => current.map(item => item.routine_id === value.routine_id && item.revision <= value.revision ? value : item));
    const [calendar, setCalendar] = React.useState({ operations: [] }), [calendarError, setCalendarError] = React.useState(""), [calendarBusy, setCalendarBusy] = React.useState(false);
    const [loading, setLoading] = React.useState(true), [error, setError] = React.useState("");
    const [sendRefreshError, setSendRefreshError] = React.useState("");
    const receiptStates = React.useRef(new Map());
    const acceptReceipt = (receipt, detail = {}) => {
      if (!receipt || receipt.session_id !== currentAgent.current?.main_session_id || (detail.requestID && detail.requestID !== receipt.request_id) || !receipt.request_id || !receipt.receipt_id || !receipt.client_id || !receipt.queue_id || !receipt.host_request_id || !receipt.accepted_at || !Number.isSafeInteger(receipt.revision) || receipt.revision < 1 || !["accepted", "in_history"].includes(receipt.receipt_state)) return false;
      const previous = receiptStates.current.get(receipt.request_id);
      if (previous?.receipt && previous.receipt.receipt_id !== receipt.receipt_id) return false;
      // Seeing the history row is independent of the execution receipt revision.
      if (previous && detail.inHistory) previous.inHistory = true;
      if (previous?.receipt && previous.receipt.revision > receipt.revision) return true;
      receiptStates.current.set(receipt.request_id, { ...previous, ...detail, receipt, state: receipt.execution_state });
      if (mounted.current) setDraftVersion(value => value + 1);
      return true;
    };
    const resumeReceipt = async entry => {
      const receipt = entry.receipt;
      if (!receipt || entry.busy) return;
      entry.busy = true; setDraftVersion(value => value + 1);
      try {
        const input = { agent_id: currentAgent.current.agent_id, request_id: receipt.request_id, receipt_id: receipt.receipt_id };
        const result = await api.invokeRuntime("agents.messages.resume", input, { ...route, userInput: { requestId: receipt.request_id, prompt: "", resumeReceiptId: receipt.receipt_id } });
        if (result?.receipt) acceptReceipt(result.receipt);
        const status = await call("agents.messages.status", { agent_id: currentAgent.current.agent_id, request_id: receipt.request_id });
        acceptReceipt(status.receipt);
        await loadConversation(currentAgent.current);
      } catch (cause) { report(cause); }
      finally { const latest = receiptStates.current.get(receipt.request_id); if (latest) latest.busy = false; if (mounted.current) setDraftVersion(value => value + 1); }
    };
    const receiptFooter = (receipt, fallback) => {
      const entry = (receipt && receiptStates.current.get(receipt.request_id)) || fallback;
      if (!entry && !receipt) return undefined;
      const r = entry?.receipt || receipt, state = r?.execution_state || entry?.state;
      const zh = locale?.startsWith("zh");
      const labels = zh ? { sending: "发送中", unknown: "送达状态待确认", rejected: "发送失败", queued: "已送达", held: "等待继续", running: "已送达", completed: "已送达", failed: "处理失败", interrupted: "处理已停止", cancelled: "已取消", unconfirmed: "处理结果待确认" } : { sending: "Sending", unknown: "Delivery unconfirmed", rejected: "Send rejected", queued: "Delivered", held: "Awaiting continue", running: "Delivered", completed: "Delivered", failed: "Processing failed", interrupted: "Processing stopped", cancelled: "Cancelled", unconfirmed: "Outcome unconfirmed" };
      if (!labels[state]) return undefined;
      return h("div", { className: "mosaic-receipt", role: "status", "data-receipt-state": state, "data-receipt-id": r?.receipt_id }, h("span", null, labels[state]), r?.receipt_state === "accepted" && state === "held" && h(Button, { "data-action": "resume-accepted-input", disabled: entry?.busy, onClick: () => resumeReceipt(entry || { receipt: r }) }, zh ? "继续" : "Continue"));
    };
    const [busy, setBusy] = React.useState(false), [readingFiles, setReadingFiles] = React.useState(false);
    const [nameOpen, setNameOpen] = React.useState(false);
    const [replySelection, setReplySelection] = React.useState(null);
    const [replyHistory, setReplyHistory] = React.useState(null);
    const reactionStates = React.useRef(new Map()), reactionTargets = React.useRef(new Set());
    const [, redrawReactions] = React.useState(0);
    // Only rendered history participates in recovery polling. Closing an older
    // reply thread releases its targets rather than growing a lifetime watchlist.
    reactionTargets.current = new Set();
    React.useEffect(() => { for (const [key, value] of reactionStates.current) if (!reactionTargets.current.has(key) && !value.pending) reactionStates.current.delete(key); });
    const replyLoadVersion = React.useRef(0), replyPaging = React.useRef(false);
    const replyPanelRef = React.useRef(null), replyTrigger = React.useRef(null);
    const [panelOpen, setPanelOpen] = React.useState(false), [showAll, setShowAll] = React.useState(false), [showAllOutputs, setShowAllOutputs] = React.useState(false);
    const [panelPresent, setPanelPresent] = React.useState(false), [panelPhase, setPanelPhase] = React.useState("closed"), [panelVisible, setPanelVisible] = React.useState(false);
    const [, setDraftVersion] = React.useState(0);
    const mounted = React.useRef(true), currentAgent = React.useRef(null), sequence = React.useRef(0), readingRef = React.useRef(false), controlRef = React.useRef(false), historyFillRef = React.useRef(false), detailTrigger = React.useRef(null), rootRef = React.useRef(null);
    const sendMotion = useSendMotion(React, rootRef);
    const panelRef = React.useRef(null), profileTrigger = React.useRef(null), focusVersion = React.useRef(0), panelID = React.useId();
    const reducePanelMotion = () => document.documentElement.dataset.appearanceMotion === "reduce" || window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;
    const openPanel = () => { focusVersion.current++; if (panelPresent) setPanelVisible(true); setPanelPresent(true); setPanelPhase("opening"); setPanelOpen(true); };
    const closePanel = (restoreFocus = true) => {
      const version = ++focusVersion.current;
      setPanelOpen(false); setPanelVisible(false); setPanelPhase("closing");
      if (restoreFocus) requestAnimationFrame(() => { if (version === focusVersion.current) profileTrigger.current?.focus({ preventScroll: true }); });
    };
    React.useLayoutEffect(() => {
      if (!panelPresent) return;
      let frame, secondFrame, timer;
      const token = getComputedStyle(panelRef.current).getPropertyValue("--mosaic-profile-motion-duration").trim();
      const duration = token ? Math.min(2400, Math.max(0, parseFloat(token) * (token.endsWith("ms") ? 1 : 1000))) : 300;
      const delay = Number.isFinite(duration) ? duration : 300;
      const settle = () => { setPanelPhase(panelOpen ? "open" : "closed"); if (!panelOpen) { setPanelPresent(false); setSelection(null); } };
      if (reducePanelMotion()) { setPanelVisible(panelOpen); settle(); }
      else if (panelOpen) {
        // Only a fresh entrance needs staged frames; retained exits reverse immediately.
        if (panelVisible) timer = setTimeout(settle, delay);
        else frame = requestAnimationFrame(() => { secondFrame = requestAnimationFrame(() => { setPanelVisible(true); timer = setTimeout(settle, delay); }); });
      } else timer = setTimeout(settle, delay);
      const reduced = () => { if (reducePanelMotion()) { cancelAnimationFrame(frame); cancelAnimationFrame(secondFrame); clearTimeout(timer); setPanelVisible(panelOpen); settle(); } };
      const media = window.matchMedia?.("(prefers-reduced-motion: reduce)");
      media?.addEventListener("change", reduced);
      const observer = new MutationObserver(reduced);
      observer.observe(document.documentElement, { attributes: true, attributeFilter: ["data-appearance-motion"] });
      return () => { cancelAnimationFrame(frame); cancelAnimationFrame(secondFrame); clearTimeout(timer); media?.removeEventListener("change", reduced); observer.disconnect(); };
    }, [panelOpen, panelPresent]);
    React.useEffect(() => {
      if (!panelOpen) return;
      panelRef.current?.focus({ preventScroll: true });
      const outside = event => {
        if (event.target.closest?.(".mosaic-routine-backdrop")) return;
        if (!panelRef.current?.contains(event.target) && !profileTrigger.current?.contains(event.target)) closePanel(false);
      };
      document.addEventListener("pointerdown", outside, true);
      return () => document.removeEventListener("pointerdown", outside, true);
    }, [panelOpen]);
    React.useEffect(() => { identityListeners.add(setAgent); if (identityAgent) setAgent(identityAgent); return () => identityListeners.delete(setAgent); }, []);
    const calendarRead = React.useRef(0), calendarOpening = React.useRef(false), historyRecovery = React.useRef({ complete: false, endSeq: undefined });
    currentAgent.current = agent;
    const conversationID = agent?.main_session_id || "", draft = draftFor(conversationID), files = draft.files;
    const stream = streamFor(conversationID), running = stream.running, activeTurnID = stream.activeTurnID, queuedCount = draft.queueIds.size, liveMessages = [...stream.items.values()], stopState = draft.stopState;
    const task = tasks.find(item => item.task_id === selection?.taskId);
    const artifactOutputs = records.flatMap(record => (record.attachments || []).filter(attachment => attachment.kind === "artifact").map(attachment => ({ seq: record.seq, at: record.at, attachment })));
    const selectedArtifact = artifactOutputs.find(output => output.seq === selection?.seq && output.attachment.sha256 === selection?.sha256);
    const pendingCalendarOperations = calendar.operations.filter(item => !["completed", "cancelled"].includes(item.status));
    const report = cause => { if (mounted.current && !api.signal.aborted) setError(cause instanceof Error ? cause.message : String(cause)); };
    const reactionKey = (identity, seq) => `${identity.main_session_id}:${seq}`;
    const acceptReaction = (identity, seq, reactions) => {
      const key = reactionKey(identity, seq), previous = reactionStates.current.get(key);
      if (!reactions || (previous && previous.value.revision > reactions.revision)) return;
      reactionStates.current.set(key, { ...previous, value: reactions });
    };
    const refreshReactions = async identity => {
      if (!identity || !mounted.current) return;
      const seqs = [...reactionTargets.current].filter(key => key.startsWith(`${identity.main_session_id}:`)).map(key => Number(key.slice(identity.main_session_id.length + 1)));
      for (let offset = 0; offset < seqs.length; offset += 100) {
        const result = await call("agents.reactions", { agent_id: identity.agent_id, session_id: identity.main_session_id, message_seqs: seqs.slice(offset, offset + 100) });
        if (!mounted.current || currentAgent.current?.main_session_id !== identity.main_session_id) return;
        if (result.session_id !== identity.main_session_id) continue;
        for (const record of result.records || []) acceptReaction(identity, record.seq, record.reactions);
      }
      if (mounted.current) redrawReactions(value => value + 1);
    };
    const changeReaction = async (record, emoji) => {
      const identity = currentAgent.current, key = reactionKey(identity, record.seq);
      const before = reactionStates.current.get(key) || { value: record.reactions || { revision: 0 } };
      if (before.pending) return;
      const request = { agent_id: identity.agent_id, session_id: identity.main_session_id, message_seq: record.seq, emoji, expected_revision: before.value.revision, request_id: crypto.randomUUID() };
      reactionStates.current.set(key, { value: before.value, pending: true, optimistic: emoji }); redrawReactions(value => value + 1);
      try {
        const result = await call("agents.react", request);
        if (result.session_id !== identity.main_session_id || result.message_seq !== record.seq) throw new Error("Reaction target mismatch");
        acceptReaction(identity, record.seq, result.reactions);
        const current = reactionStates.current.get(key);
        reactionStates.current.set(key, { value: current.value });
      } catch (cause) {
        const current = reactionStates.current.get(key);
        reactionStates.current.set(key, { value: current.value, error: cause instanceof Error ? cause.message : String(cause) });
        // A lost response may already have committed. Reconcile durable metadata,
        // without replaying a turn or guessing whether the write succeeded.
        await refreshReactions(identity).catch(() => {});
      } finally { if (mounted.current) redrawReactions(value => value + 1); }
    };
    const reactionFor = record => {
      if (!agent || record.streaming || !Number.isSafeInteger(record.seq) || record.seq < 1 || !["user", "assistant"].includes(record.role)) return undefined;
      const key = reactionKey(agent, record.seq); reactionTargets.current.add(key);
      acceptReaction(agent, record.seq, record.reactions || { revision: 0 });
      const state = reactionStates.current.get(key), owner = record.role === "assistant";
      return { emoji: owner && state.pending ? state.optimistic || undefined : state.value[owner ? "owner" : "agent"], label: t(owner ? "reaction" : "agentReaction"), pending: state.pending, error: state.error, onChange: owner && record.reaction_eligible === true ? emoji => { void changeReaction(record, emoji); } : undefined };
    };
    const loadEarlierHistory = async () => {
      const identity = agent, boundary = historyInfo;
      if (!identity || !boundary?.has_earlier || historyFillRef.current) return;
      historyFillRef.current = true;
      try {
        const history = await call("agents.history", { agent_id: identity.agent_id, end_seq: boundary.first_seq - 1 });
        if (!mounted.current || currentAgent.current?.agent_id !== identity.agent_id) return;
        if (history.first_seq >= boundary.first_seq) throw new Error("history pagination did not advance");
        setRecords(current => [...new Map([...(history.records || []), ...current].map(record => [record.seq, record])).values()].sort((a, b) => a.seq - b.seq));
        setHistoryInfo(current => current?.first_seq === boundary.first_seq ? { first_seq: history.first_seq, has_earlier: history.has_earlier } : current);
      } finally { historyFillRef.current = false; }
    };
    const loadTasks = async () => { const result = await call("tasks.list"); if (mounted.current) setTasks(current => (result.tasks || []).map(item => { const previous = current.find(value => value.task_id === item.task_id); return previous?.revision > item.revision ? previous : item; })); };
    const loadRoutines = async () => { const result = await call("routines.list"); if (mounted.current) setRoutines(current => (result.routines || []).map(item => { const previous = current.find(value => value.routine_id === item.routine_id); return previous?.revision > item.revision ? previous : item; })); };
    const loadCalendar = async identity => {
      if (!identity) return;
      const version = ++calendarRead.current;
      try {
        const [status, result] = await Promise.all([call("calendar.status", { agent_id: identity.agent_id }), call("calendar.operations.list", { agent_id: identity.agent_id })]);
        if (mounted.current && version === calendarRead.current) { setCalendar({ status, operations: result.operations || [] }); setCalendarError(""); }
      } catch (cause) { if (mounted.current && version === calendarRead.current) setCalendarError(cause instanceof Error ? cause.message : String(cause)); }
    };
    const openCalendar = async operationId => {
      if (calendarOpening.current) return;
      calendarOpening.current = true; setCalendarBusy(true);
      try { await host.openView("personal-calendar.confirmation", { region: "overlay", context: operationId ? { operationId } : {}, persistence: "session", reveal: false }); }
      catch (cause) { if (mounted.current) setCalendarError(cause instanceof Error ? cause.message : String(cause)); }
      finally { calendarOpening.current = false; if (mounted.current) setCalendarBusy(false); }
    };
    const loadConversation = async identity => {
      if (!identity) return;
      const epoch = ++sequence.current, stream = streamFor(identity.main_session_id), version = stream.version;
      const current = () => mounted.current && epoch === sequence.current && !api.signal.aborted;
      try {
        const [opened, latest] = await Promise.all([call("agents.open", { agent_id: identity.agent_id }), call("agents.history", { agent_id: identity.agent_id })]);
        if (!current()) return;
        if (opened.agent?.main_session_id && opened.agent.main_session_id !== identity.main_session_id) throw new Error("main_session_binding_conflict");
        publishIdentity(opened.agent);
        for (const entry of opened.pending_inputs || []) acceptReceipt(entry.receipt, entry);
        for (const record of latest.records || []) if (record.receipt) acceptReceipt(record.receipt, { prompt: record.display_content || record.content, relation: record.relation, inHistory: true });
        const receiptReadErrors = [];
        const readReceiptStatus = async requestID => {
          try { return await call("agents.messages.status", { agent_id: identity.agent_id, request_id: requestID }); }
          catch (cause) { receiptReadErrors.push(cause); return {}; }
        };
        const pendingIDs = new Set((opened.pending_inputs || []).map(entry => entry.receipt?.request_id));
        for (const [requestID, entry] of receiptStates.current) {
          const receipt = entry.receipt;
          if (receipt?.session_id !== identity.main_session_id || pendingIDs.has(requestID) || !["queued", "held", "running", "unconfirmed"].includes(receipt.execution_state)) continue;
          // Absence from the bounded pending/history views is not a terminal state.
          // Resolve the original identity without replaying or dropping its bubble.
          const status = await readReceiptStatus(requestID);
          if (!current()) return;
          acceptReceipt(status.receipt, { requestID });
        }
        for (const [draftID, saved] of conversations) {
          if (draftID !== identity.main_session_id && !draftID.startsWith(identity.main_session_id + ":")) continue;
          const pending = saved.pending, editVersion = saved.editVersion;
          if (!pending?.uncertain || saved.submitting) continue;
          const detail = { requestID: pending.requestID, prompt: pending.prompt, ...(saved.replyTarget?.relation ? { relation: saved.replyTarget.relation } : {}), sessionID: identity.main_session_id, draftID };
          if (!receiptStates.current.has(pending.requestID)) receiptStates.current.set(pending.requestID, { ...detail, state: "unknown" });
          const status = await readReceiptStatus(pending.requestID);
          if (!current()) return;
          if (acceptReceipt(status.receipt, detail) && saved.pending?.requestID === pending.requestID && saved.editVersion === editVersion) updateDraft(draftID, { pending: null, prompt: "", files: [], attachmentsMissing: false, error: "" });
        }
        let history = latest;
        const recovery = historyRecovery.current;
        // The runtime bounds each audit scan. Finish the initial visible page
        // across those boundaries, retaining progress if a newer read takes over.
        while (!recovery.complete && !history.records?.length && history.has_earlier && history.first_seq > 1) {
          recovery.endSeq = Math.min(recovery.endSeq ?? Infinity, history.first_seq - 1);
          history = await call("agents.history", { agent_id: identity.agent_id, end_seq: recovery.endSeq });
          if (!current()) return;
          if (history.first_seq > recovery.endSeq) throw new Error("history recovery did not advance");
        }
        recovery.complete = true;
        setRecords(current => [...new Map([...current, ...(history.records || [])].map(record => [record.seq, record])).values()].sort((a, b) => a.seq - b.seq));
        setHistoryInfo(current => current && current.first_seq < history.first_seq ? current : { first_seq: history.first_seq, has_earlier: history.has_earlier });
        for (const [id, message] of stream.items) {
          if (!message.streaming && (history.records || []).some(record => (message.seq !== undefined && record.seq === message.seq) || (message.source_id && record.provider_item_id === message.source_id))) stream.items.delete(id);
        }
        const state = opened.session?.state;
        if (version === stream.version) {
          stream.running = ["running", "queued"].includes(state);
          stream.activeTurnID = opened.turn?.state === "running" ? opened.turn.turn_id : undefined;
          const pending = draftFor(identity.main_session_id);
          if (pending.stopTarget && opened.turn?.turn_id === pending.stopTarget && ["completed", "failed", "interrupted", "cancelled"].includes(opened.turn.state)) updateDraft(identity.main_session_id, { stopState: undefined, stopTarget: undefined });
        }
        draftListeners.forEach(listener => listener());
        if (receiptReadErrors.length) throw receiptReadErrors[0];
      } catch (cause) { if (current()) throw cause; }
      finally { if (current()) setLoading(false); }
    };
    const refreshAcceptedSend = async () => {
      const refresh = loadConversation(agent), epoch = sequence.current;
      try { await refresh; if (mounted.current && epoch === sequence.current) setSendRefreshError(""); }
      catch (cause) { if (mounted.current && epoch === sequence.current) setSendRefreshError(String(cause.message || cause)); }
    };
    const initialize = async () => {
      setError(""); setLoading(true);
      try {
        const result = await call("agents.ensure_default", { name: "twin" });
        if (!result.agent?.main_session_id) throw new Error("main_session_recovery_required");
        if (!mounted.current) return;
        const identity = publishIdentity(result.agent);
        mainSessionID = identity.main_session_id; currentAgent.current = identity;
        await Promise.all([loadConversation(identity), loadTasks(), loadRoutines(), loadCalendar(identity)]);
      } catch (cause) { report(cause); }
      finally { if (mounted.current) setLoading(false); }
    };
    React.useEffect(() => {
      mounted.current = true;
      const changed = () => setDraftVersion(value => value + 1); draftListeners.add(changed);
      void initialize();
      return () => { mounted.current = false; sequence.current++; draftListeners.delete(changed); };
    }, []);
    React.useEffect(() => {
      let checking = false, recheck = false;
      const check = async () => {
        if (!mounted.current || !currentAgent.current) return;
        if (checking) { recheck = true; return; }
        checking = true;
        try { await Promise.all([loadTasks(), loadRoutines(), loadConversation(currentAgent.current), loadCalendar(currentAgent.current), refreshReactions(currentAgent.current)]); } catch (cause) { report(cause); }
        finally { checking = false; if (recheck) { recheck = false; void check(); } }
      };
      const timer = setInterval(check, 5000);
      let homeActive = false;
      const stopWorkbench = api.application.workbench.subscribe(() => {
        const snapshot = api.application.workbench.getSnapshot();
        const active = new Set(Object.values(snapshot.activeViewByRegion));
        const next = snapshot.views.some(view => active.has(view.id) && view.pluginId === api.pluginId && view.viewTypeId === "personal.home");
        if (next && !homeActive) void check();
        homeActive = next;
      });
      const subscription = api.onHostEvent(event => {
        const invalidation = event.message?.params;
        if (event.kind === "notification" && event.message?.method === "plugin/client/invalidated") {
          if (invalidation?.plugin_id === api.pluginId && invalidation?.fingerprint === api.generation && invalidation?.user_space_id === route.userSpaceId && invalidation?.product_domain === route.productDomain && invalidation?.topic === "reactions") void refreshReactions(currentAgent.current).catch(report);
          return;
        }
        const params = event.message?.params, session = params?.thread_id || params?.session_id || params?.message?.thread_id || params?.queued?.thread_id;
        if (event.kind === "notification" && session && session !== currentAgent.current?.main_session_id) return;
        if (event.kind === "notification" && /delta$/.test(event.message?.method || "")) return;
        if (event.type === "routine-state-changed" || event.kind === "notification") void check();
      });
      return () => { clearInterval(timer); stopWorkbench(); subscription.dispose(); };
    }, []);
    const mutate = async action => { if (controlRef.current) return; controlRef.current = true; setBusy(true); setError(""); try { await action(); } catch (cause) { report(cause); } finally { controlRef.current = false; if (mounted.current) setBusy(false); } };
    React.useLayoutEffect(() => {
      if (!agent || loading || replySelection || !historyInfo?.has_earlier) return;
      const viewport = rootRef.current?.querySelector(".mosaic-main > .plugin-conversation .plugin-conversation-scroll");
      if (!viewport) return;
      let frame;
      const fill = () => {
        cancelAnimationFrame(frame);
        frame = requestAnimationFrame(() => {
          if (viewport.clientHeight > 0 && viewport.scrollHeight <= viewport.clientHeight + 1) void loadEarlierHistory().catch(report);
        });
      };
      fill();
      const observer = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(fill);
      observer?.observe(viewport);
      return () => { cancelAnimationFrame(frame); observer?.disconnect(); };
    }, [agent?.agent_id, loading, replySelection, historyInfo?.first_seq, historyInfo?.has_earlier, records.length, liveMessages.length]);
    const selectFiles = async (selected, draftID = conversationID) => {
      if (!selected.length || readingRef.current || !agent) return;
      const id = draftID; readingRef.current = true; setReadingFiles(true); updateDraft(id, { error: "" }); if (id === conversationID) setError("");
      try {
        const existing = draftFor(id).files;
        if (existing.length + selected.length > fileCountLimit || selected.some(file => !file.size) || existing.reduce((total, file) => total + file.size, 0) + selected.reduce((total, file) => total + file.size, 0) > fileLimit) throw new Error(t("attachmentLimit"));
        const next = [];
        for (const file of selected) {
          const mime = file.type || "application/octet-stream";
          if (!safeFilename(file.name) || !safeMime(mime)) throw new Error(t("invalidFile"));
          const data = await readFile(file), bytes = decodeBytes(data);
          next.push({ sha256: await digestBytes(bytes), filename: file.name, media_type: mime, data, size: bytes.length });
        }
        updateDraft(id, { files: [...draftFor(id).files, ...next], ...(next.length ? { attachmentsMissing: false } : {}) });
      } catch (cause) { updateDraft(id, { error: cause instanceof Error ? cause.message : String(cause) }); if (id === conversationID) report(cause); }
      finally { readingRef.current = false; if (mounted.current) setReadingFiles(false); }
    };
    const setReply = async (target, source, taskContext) => {
      const version = ++replyLoadVersion.current;
      replyTrigger.current = document.activeElement;
      const reference = target.relation.reply_to;
      try {
        const history = taskContext ? null : await call("agents.history", { agent_id: agent.agent_id, reply_seq: reference.seq });
        if (!mounted.current || version !== replyLoadVersion.current) return;
        const key = taskContext ? `${conversationID}:task:${JSON.stringify(target.relation.metadata.task_links)}` : `${conversationID}:reply:${history.reply_root_seq}`;
        if (JSON.stringify(draftFor(key).replyTarget?.relation) !== JSON.stringify(target.relation)) updateDraft(key, { replyTarget: target });
        for (const record of history?.records || []) if (record.receipt) acceptReceipt(record.receipt, { prompt: record.display_content || record.content, relation: record.relation, inHistory: true });
        setReplyHistory(history);
        closePanel(false);
        setReplySelection({ key, source, taskContext, rootSeq: history?.reply_root_seq });
      } catch (cause) { if (version === replyLoadVersion.current) report(cause); }
    };
    const closeReplies = () => {
      replyLoadVersion.current++;
      setReplySelection(null);
      requestAnimationFrame(() => { const trigger = replyTrigger.current; if (trigger?.isConnected && !trigger.closest('[inert],[hidden]')) trigger.focus({ preventScroll: true }); else rootRef.current?.querySelector('.mosaic-main textarea')?.focus({ preventScroll: true }); });
    };
    React.useEffect(() => { if (replySelection) replyPanelRef.current?.querySelector("textarea")?.focus({ preventScroll: true }); }, [replySelection?.key]);
    const replyMessage = message => {
      if (message.role !== "assistant") return;
      setReply({ label: t("replyingMessage"), preview: message.display_content || message.content || message.attachments?.map(item => item.name).join(", ") || "", relation: { reply_to: { session_id: conversationID, seq: message.seq }, ...(message.relation?.metadata?.task_links ? { metadata: { task_links: message.relation.metadata.task_links } } : {}) } }, message);
    };
    const loadReplyHistory = async (earlier = false) => {
      if (!replySelection || replySelection.taskContext || replyPaging.current || (earlier && !replyHistory?.has_earlier)) return;
      const version = replyLoadVersion.current;
      replyPaging.current = true;
      try {
        const page = await call("agents.history", { agent_id: agent.agent_id, reply_seq: replySelection.rootSeq, ...(earlier ? { end_seq: replyHistory.first_seq - 1 } : {}) });
        if (!mounted.current || version !== replyLoadVersion.current) return;
        for (const record of page.records || []) if (record.receipt) acceptReceipt(record.receipt, { prompt: record.display_content || record.content, relation: record.relation, inHistory: true });
        setReplyHistory(current => ({ ...page, ...(!earlier && current?.records.length ? { first_seq: current.first_seq, has_earlier: current.has_earlier } : {}), records: [...new Map([...(current?.records || []), ...page.records].map(record => [record.seq, record])).values()].sort((a, b) => a.seq - b.seq) }));
      } catch (cause) { if (version === replyLoadVersion.current) updateDraft(replySelection.key, { error: String(cause.message || cause) }); }
      finally { replyPaging.current = false; }
    };
    React.useEffect(() => { void loadReplyHistory(); }, [records.at(-1)?.seq, running]);
    React.useEffect(() => {
      if (!replyHistory?.has_earlier || !replySelection || replySelection.taskContext) return;
      const viewport = replyPanelRef.current?.querySelector('.plugin-conversation-scroll');
      if (!viewport) return;
      const load = () => { if (viewport.clientHeight > 0 && (viewport.scrollTop < 80 || viewport.scrollHeight <= viewport.clientHeight + 1)) void loadReplyHistory(true); };
      viewport.addEventListener('scroll', load, { passive: true });
      const frame = requestAnimationFrame(load);
      return () => { viewport.removeEventListener('scroll', load); cancelAnimationFrame(frame); };
    }, [replySelection?.key, replyHistory]);
    const genuiContext = () => {
      const surfaces = new Map();
      for (const record of [...records, ...(replyHistory?.records || [])]) for (const part of record.ui_parts || []) {
        const surface = part.surface;
        if (surface?.schemaVersion === 1) surfaces.set(surface.surfaceId, { surfaceId: surface.surfaceId, state: surface.state });
      }
      for (const [key, draft] of genuiDrafts) if (key.startsWith(`${agent.agent_id}:`)) surfaces.set(draft.surfaceId, draft);
      return structuredClone([...surfaces.values()]);
    };
    const send = async (submitted, draftID = conversationID) => {
      const id = draftID, state = draftFor(id), text = submitted ?? state.prompt, sentFiles = state.files, sentTarget = state.replyTarget;
      if (!agent || state.submitting || state.attachmentsMissing || readingRef.current || (!text.trim() && !sentFiles.length)) return;
      const references = sentFiles.map(({ sha256, filename, media_type }) => ({ sha256, filename, media_type })), fileKey = JSON.stringify(references), relationKey = JSON.stringify(sentTarget?.relation);
      if (!state.pending || state.pending.prompt !== text || state.pending.fileKey !== fileKey || state.pending.relationKey !== relationKey) state.pending = { prompt: text, fileKey, relationKey, requestID: crypto.randomUUID(), genuiContext: genuiContext() };
      const requestID = state.pending.requestID, editVersion = state.editVersion;
      if (!receiptStates.current.has(requestID)) sendMotion.begin(requestID, id);
      const stream = streamFor(conversationID), version = stream.version;
      updateDraft(id, { submitting: true, error: "" }); if (id === conversationID) setError("");
      const detail = { requestID, prompt: text, relation: sentTarget?.relation, attachments: references, sessionID: conversationID, draftID: id };
      receiptStates.current.set(requestID, { ...detail, ...receiptStates.current.get(requestID), state: "sending" });
      const recover = async () => { const status = await call("agents.messages.status", { agent_id: agent.agent_id, request_id: requestID }); return acceptReceipt(status.receipt, detail) ? { receipt: status.receipt, state: status.receipt.execution_state, queue_id: status.receipt.queue_id, turn_id: status.receipt.hostturn_id } : null; };
      try {
        let result = state.pending.uncertain ? await recover() : null;
        if (!result) result = await api.invokeRuntime("agents.send", { agent_id: agent.agent_id, request_id: requestID, prompt: text, ...(state.pending.genuiContext?.length ? { genui_context: state.pending.genuiContext } : {}), ...(sentTarget ? { relation: sentTarget.relation } : {}), ...(sentFiles.length ? { attachments: references } : {}) }, { ...route, userInput: { requestId: requestID, prompt: text, ...(sentTarget ? { relation: sentTarget.relation } : {}), ...(sentFiles.length ? { attachments: sentFiles.map(({ size, ...file }) => file) } : {}) } });
        if (!acceptReceipt(result?.receipt, detail)) {
          result = await recover();
          if (!result) throw new Error(locale?.startsWith("zh") ? "尚未确认持久接收，请保留草稿并重试。" : "Durable delivery is not confirmed. Keep the draft and retry.");
        }
        const latest = draftFor(id);
        updateDraft(id, { pending: null, ...(latest.editVersion === editVersion ? { prompt: "", files: [], attachmentsMissing: false } : {}) });
        if (result?.state === "queued" && result.queue_id && !draftFor(conversationID).dequeuedIds.has(result.queue_id)) { draftFor(conversationID).queueIds.add(result.queue_id); updateDraft(conversationID, {}); }
        if (result?.state === "running" && result.turn_id && version === stream.version && !stream.completedTurns.has(result.turn_id)) { stream.activeTurnID = result.turn_id; stream.running = true; stream.version++; updateDraft(id, {}); }
      } catch (cause) {
        let recovered; try { recovered = await recover(); } catch {}
        if (recovered) { const latest = draftFor(id); updateDraft(id, { pending: null, ...(latest.editVersion === editVersion ? { prompt: "", files: [], attachmentsMissing: false } : {}) }); }
        else {
          const current = draftFor(id); if (current.pending?.requestID === requestID) current.pending.uncertain = true;
          const detailError = cause instanceof Error ? cause.message : String(cause);
          receiptStates.current.set(requestID, { ...detail, state: /request_id_conflict/.test(detailError) ? "rejected" : "unknown" });
          updateDraft(id, { error: detailError }); if (id === conversationID) report(cause); return;
        }
      }
      finally { updateDraft(id, { submitting: false }); }
      // A successful admission is not undone by a failed read. Never turn a
      // history refresh into a retry of the accepted message or its attachments.
      if (mounted.current) await refreshAcceptedSend();
    };
    const stopReply = async () => {
      const id = conversationID, pending = draftFor(id), target = pending.stopTarget || activeTurnID;
      if (pending.stopState === "pending" || !agent || !target || streamFor(id).completedTurns.has(target)) return;
      // Freeze the reviewed turn before the RPC; a retry must never cancel its successor.
      updateDraft(id, { stopState: "pending", stopTarget: target }); setError("");
      try { await call("agents.stop_reply", { agent_id: agent.agent_id, turn_id: target }); }
      catch (cause) { if (draftFor(id).stopTarget === target) updateDraft(id, { stopState: "retry" }); report(cause); return; }
      if (draftFor(id).stopTarget === target) updateDraft(id, { stopState: undefined, stopTarget: undefined });
      if (streamFor(id).activeTurnID === target) streamFor(id).activeTurnID = undefined;
      // Refresh failure does not undo a successful (or idempotent no-op) stop.
      try { await loadConversation(agent); } catch (cause) { report(cause); }
    };
    const replyToQuestion = async (questionID, prompt, relation) => {
      const requestID = `question-${questionID}`;
      const questionDraft = draftFor(`${conversationID}:${requestID}`);
      questionDraft.genuiContext ??= genuiContext();
      const result = await api.invokeRuntime("agents.send", { agent_id: agent.agent_id, request_id: requestID, prompt, ...(questionDraft.genuiContext.length ? { genui_context: questionDraft.genuiContext } : {}), ...(relation ? { relation } : {}) }, { ...route, userInput: { requestId: requestID, prompt, ...(relation ? { relation } : {}) } });
      if (result?.state === "queued" && result.queue_id && !draftFor(conversationID).dequeuedIds.has(result.queue_id)) { draftFor(conversationID).queueIds.add(result.queue_id); updateDraft(conversationID, {}); }
      // Offer replies have their own admission and never consume a composer draft.
      try { await loadConversation(agent); } catch (cause) { report(cause); }
    };
    const openDetail = (event, item, kind = "task") => { focusVersion.current++; detailTrigger.current = `[data-${kind === "output" ? "output-task" : "task"}-id="${item.task_id}"]`; setSelection({ taskId: item.task_id, kind }); };
    React.useEffect(() => { if (selection) rootRef.current?.querySelector('[data-action="close-detail"]')?.focus({ preventScroll: true }); }, [selection]);
    const closeDetail = () => { const version = ++focusVersion.current; setSelection(null); requestAnimationFrame(() => { if (version !== focusVersion.current) return; const target = rootRef.current?.querySelector(detailTrigger.current); const details = target?.closest("details"); if (details) details.open = true; target?.focus({ preventScroll: true }); }); };
    const date = (value, compact = false, timeZone) => { const parsed = new Date(value); return Number.isFinite(parsed.getTime()) ? new Intl.DateTimeFormat(locale, { month: compact ? "numeric" : "short", day: "numeric", hour: "numeric", minute: "2-digit", ...(timeZone ? { timeZone } : {}) }).format(parsed) : ""; };
    const progressText = task => task.progress === "Previous execution is still stopping; replacement dispatch is held until cancellation is confirmed" ? t("applyingChanges") : task.progress;
    const taskRows = items => items.map(item => h("button", { key: item.task_id, className: "mosaic-task", "data-task-id": item.task_id, onClick: event => openDetail(event, item) },
      h("span", { className: "mosaic-task-mark", "aria-hidden": true }, icon(["running", "queued"].includes(item.state) ? "progress" : "activity")),
      h("span", { className: "mosaic-task-copy" }, h("span", { className: "mosaic-task-title" }, item.title), h("span", { className: "mosaic-task-meta" }, progressText(item) || item.question?.text || t(item.state))),
      h("span", { className: "mosaic-task-state", "data-state": item.state, "aria-label": t(item.state) }, item.state === "waiting_user" || item.reconciliation_required ? "◦" : ["running", "queued"].includes(item.state) ? "●" : "›")));
    const recent = [...tasks].sort((a, b) => String(b.updated_at || "").localeCompare(String(a.updated_at || "")));
    const outputs = tasks.filter(item => item.state === "completed" && item.result).sort((a, b) => String(b.updated_at || "").localeCompare(String(a.updated_at || "")));
    const visibleRoutines = routines.filter(routine => {
      if (routine.status !== "completed" || routine.cron || routine.last_error || routine.cancellation_pending || routine.binding.status !== "active") return true;
      if (routine.kind !== "task") return false;
      // A one-shot binding finishes admitting work before its task and reply settle.
      const work = tasks.filter(task => task.routine_id === routine.routine_id && task.routine_revision === routine.config_revision);
      return !work.length || work.some(task => task.state !== "completed" || task.error || task.reconciliation_required || task.cancel_requested || !["delivered", "suppressed"].includes(task.delivery?.status));
    });
    const scheduledIDs = new Set(visibleRoutines.map(item => item.routine_id));
    const scheduledTime = item => {
      const value = Date.parse(item.binding?.next_run_at || item.run_at || "");
      return Number.isFinite(value) ? value : Infinity;
    };
    const scheduled = [...visibleRoutines].sort((a, b) => scheduledTime(a) - scheduledTime(b) || a.routine_id.localeCompare(b.routine_id));
    const activity = [...scheduled.map(item => ({ routine: item })), ...recent.filter(item => !scheduledIDs.has(item.routine_id)).map(item => ({ task: item }))];
    const routineRow = routine => h("button", { key: `routine:${routine.routine_id}`, type: "button", className: "mosaic-task", "data-routine-id": routine.routine_id, "data-action": "open-routine", onClick: event => { routineTrigger.current = event.currentTarget; setRoutineID(routine.routine_id); } }, h("span", { className: "mosaic-task-mark", "aria-hidden": true }, icon("clock")), h("span", { className: "mosaic-task-copy mosaic-task-title" }, routine.title), h("span", { className: "mosaic-routine-next" }, routine.binding?.next_run_at && routine.status === "active" ? date(routine.binding.next_run_at, true, routine.timezone) : routineStatus(routine.status, locale, t)));

    const openRename = () => { closePanel(false); setNameOpen(true); };
    const closeRename = () => { const version = ++focusVersion.current; setNameOpen(false); requestAnimationFrame(() => { if (version === focusVersion.current) profileTrigger.current?.focus({ preventScroll: true }); }); };
    const saveName = async name => {
      try { const result = await call("agents.update", { agent_id: agent.agent_id, expected_revision: agent.revision, name }); if (!mounted.current) return; publishIdentity(result.agent); closeRename(); }
      catch (cause) { if (mounted.current) await loadConversation(agent).catch(() => {}); throw cause; }
    };
    const displayName = agent?.name || "twin";
    const avatarStatus = running ? liveMessages.some(message => message.streaming && message.content) ? "responding" : "thinking" : queuedCount > 0 ? "queued" : "idle";
    const statusText = running ? t(avatarStatus) : queuedCount > 0 ? `${queuedCount} · ${t("queued")}` : t("ready");
    const avatar = (large = false) => h("span", { className: "mosaic-avatar", "aria-hidden": true }, h(Avatar, { size: large ? 64 : 40, status: avatarStatus, motion: large && panelOpen ? "ambient" : "static" }));
    const sidebar = h("aside", { ref: panelRef, id: panelID, className: "mosaic-sidebar", "data-personal-panel": true, hidden: !panelPresent, inert: !panelOpen, "aria-hidden": !panelOpen, "data-panel-phase": panelPhase, "data-panel-visible": panelVisible, tabIndex: -1, role: "dialog", "aria-label": t("profile"), onKeyDown: event => {
      if (event.key === "Escape") { event.stopPropagation(); event.preventDefault(); if (selection) closeDetail(); else closePanel(); }
      if (event.key === "Tab") {
        const controls = [...panelRef.current.querySelectorAll('button:not(:disabled),a[href],input:not(:disabled),textarea:not(:disabled),select:not(:disabled),summary')].filter(node => !node.closest('[hidden]') && (!node.closest("details:not([open])") || node.tagName === "SUMMARY"));
        const first = controls[0], last = controls[controls.length - 1];
        if (event.shiftKey && (document.activeElement === first || document.activeElement === panelRef.current)) { event.preventDefault(); last?.focus(); }
        else if (!event.shiftKey && (document.activeElement === last || document.activeElement === panelRef.current)) { event.preventDefault(); first?.focus(); }
      }
    } },
      h("div", { className: "mosaic-panel-scroll" }, selectedArtifact ? h("div", { className: "mosaic-detail", "data-artifact-detail": selectedArtifact.attachment.sha256 },
        h("header", { className: "mosaic-detail-head" }, h("button", { type: "button", className: "icon-button", "data-action": "close-detail", "aria-label": t("back"), onClick: closeDetail }, h("svg", { viewBox: "0 0 24 24", fill: "none", stroke: "currentColor", strokeWidth: 1.5, "aria-hidden": true }, h("path", { d: "M15 5l-7 7 7 7" }))), h("span", { className: "mosaic-detail-title", title: selectedArtifact.attachment.name }, selectedArtifact.attachment.name)),
        h("div", { className: "mosaic-detail-content" }, h(Attachment, { key: `${selectedArtifact.seq}:${selectedArtifact.attachment.sha256}`, agentId: agent.agent_id, seq: selectedArtifact.seq, attachment: selectedArtifact.attachment, t }))) : selection && task ? h("div", { className: "mosaic-detail", "data-task-detail": task.task_id },
        h("header", { className: "mosaic-detail-head" }, h("button", { type: "button", className: "icon-button", "data-action": "close-detail", "aria-label": t("back"), onClick: closeDetail }, h("svg", { viewBox: "0 0 24 24", fill: "none", stroke: "currentColor", strokeWidth: 1.5, "aria-hidden": true }, h("path", { d: "M15 5l-7 7 7 7" }))), h("span", { className: "mosaic-detail-title", title: task.title }, task.title)),
        h("div", { className: "mosaic-detail-content" },
          task.goal && h(ConversationMessage, { id: `task:${task.task_id}:${task.requirements_revision}:goal`, role: "user", content: task.goal, linkPolicy: "external-only", messageActions: "none" }),
          h(ConversationMessage, { id: `task:${task.task_id}:${task.requirements_revision}:result`, role: "assistant", content: (typeof task.result === "string" ? task.result : task.result?.summary) || task.question?.text || "", timestamp: task.result?.verified_at, latest: true, linkPolicy: "external-only", messageActions: "none", turnStatus: task.state === "completed" ? "completed" : task.state === "failed" ? "failed" : ["running", "queued", "stopping"].includes(task.state) ? "in_progress" : "interrupted", process: { label: t(task.state), content: progressText(task), details: h(TaskTrace, { key: `${task.task_id}:${task.requirements_revision}`, taskId: task.task_id, taskRevision: task.revision, requirementsRevision: task.requirements_revision, requirements: task.requirements, t }) } }),
          task.error && h("p", { role: "alert" }, task.error),
          task.reconciliation_required && h("p", { role: "status" }, t("reconciliation"))),
        h("div", { className: "mosaic-detail-actions" },
          aliveTask(task) && h(Button, { disabled: busy || task.state === "stopping", "data-action": "stop-task", onClick: () => mutate(async () => { await call("tasks.stop", { task_id: task.task_id, expected_revision: task.revision }); await loadTasks(); }) }, t("stopTask")),
          h(Button, { "data-action": "reply-task", onClick: () => setReply({ label: t("replyingTask"), preview: task.title, relation: { metadata: { task_links: [{ task_id: task.task_id, requirements_revision: task.question?.requirements_revision ?? task.requirements_revision, ...(task.execution?.session_id ? { execution_session_id: task.execution.session_id } : {}), ...(task.question?.request_id ? { question_request_id: task.question.request_id } : {}) }] } } }, undefined, task) }, t("replyTask")),
          h(Button, { "data-action": "open-main-chat", variant: "ghost", onClick: () => { closePanel(false); rootRef.current?.querySelector(".mosaic-main textarea")?.focus({ preventScroll: true }); } }, t("openMainChat"))))  : h(React.Fragment, null,
        h("header", { className: "mosaic-profile-head" }, avatar(true), h("div", { className: "mosaic-profile-copy" }, h("button", { type: "button", className: "mosaic-profile-name mosaic-profile-name-button", "data-action": "rename-personal", "aria-label": `${t("rename")} · ${displayName}`, onClick: openRename }, displayName), h("div", { className: "mosaic-status", role: "status" }, statusText))),
        h("section", { className: "mosaic-section", "data-panel-section": "recent" }, h("h2", null, t("activity")), activity.length ? (showAll ? activity : activity.slice(0, 5)).map(item => item.routine ? routineRow(item.routine) : taskRows([item.task])) : h("p", { className: "mosaic-eyebrow" }, t("quiet")), activity.length > 5 && h(Button, { variant: "ghost", "data-action": "show-more-activity", onClick: () => setShowAll(value => !value) }, t(showAll ? "showLess" : "showMore"))),
        h("section", { className: "mosaic-section", "data-panel-section": "outputs" }, h("h2", null, t("outputs")), artifactOutputs.slice(0, showAllOutputs ? undefined : 3).map(output => h("button", { key: `${output.seq}:${output.attachment.sha256}`, className: "mosaic-task", "data-output-sha256": output.attachment.sha256, onClick: () => { detailTrigger.current = `[data-output-sha256="${output.attachment.sha256}"]`; setSelection({ kind: "artifact", seq: output.seq, sha256: output.attachment.sha256 }); } }, h("span", { className: "mosaic-task-mark", "aria-hidden": true }, icon("output")), h("span", { className: "mosaic-task-copy" }, h("span", { className: "mosaic-task-title" }, output.attachment.name), h("span", { className: "mosaic-task-meta" }, date(output.at))))), outputs.length ? outputs.slice(0, showAllOutputs ? undefined : Math.max(0, 3 - artifactOutputs.length)).map(item => h("button", { key: item.task_id, className: "mosaic-task", "data-output-task-id": item.task_id, onClick: event => openDetail(event, item, "output") }, h("span", { className: "mosaic-task-mark", "aria-hidden": true }, icon("output")), h("span", { className: "mosaic-task-copy" }, h("span", { className: "mosaic-task-title" }, item.title), h("span", { className: "mosaic-task-meta" }, date(item.updated_at))))) : !artifactOutputs.length && h("p", { className: "mosaic-eyebrow" }, t("noOutputs")), artifactOutputs.length + outputs.length > 3 && h(Button, { variant: "ghost", "data-action": "show-more-outputs", onClick: () => setShowAllOutputs(value => !value) }, t(showAllOutputs ? "showLess" : "showMore"))),
        null)));
    const interactiveFor = record => record.ui_parts?.map(snapshot => {
      const input = { agent_id: agent.agent_id, surface_id: snapshot.surface.surfaceId };
      const deliver = async (result, resumeHeld = false) => {
        const admission = result.submission;
        if (!admission || (result.delivery === "held" && !resumeHeld)) return result;
        return api.invokeRuntime("genui.retry", { ...input, ...(admission.resumeReceiptId ? { receipt_id: admission.resumeReceiptId } : {}) }, { ...route, userInput: admission });
      };
      return {
        snapshot, fallbackText: snapshot.surface.spec.fallbackText,
        onStateChange: state => genuiDrafts.set(`${agent.agent_id}:${snapshot.surface.surfaceId}`, { surfaceId: snapshot.surface.surfaceId, state: structuredClone(state) }),
        onSync: request => call("genui.sync", { agent_id: agent.agent_id, request }),
        onAction: async request => deliver(await call("genui.action", { agent_id: agent.agent_id, request })),
        onRefresh: () => call("genui.read", input),
        onRetry: async resumeHeld => deliver(await call("genui.read", input), resumeHeld),
      };
    });
    const renderReplyMessage = record => h(ConversationMessage, { className: sendMotion.className(record.receipt?.request_id), interactive: interactiveFor(record), footer: receiptFooter(record.receipt), reaction: reactionFor(record), key: `reply:${record.seq ?? record.id}`, id: `reply:${conversationID}:${record.seq ?? record.id}`, streaming: record.streaming, role: record.role, content: record.ui_parts?.length ? record.ui_text || "" : record.display_content || record.content || "", timestamp: record.at, linkPolicy: "external-only", attachments: record.attachments?.length ? h(React.Fragment, null, record.attachments.map(attachment => h(Attachment, { key: `${conversationID}:${record.seq}:${attachment.sha256}`, agentId: agent.agent_id, seq: record.seq, attachment, t, autoLoad: true }))) : undefined });
    const replyDraft = replySelection ? draftFor(replySelection.key) : undefined;
    const rootSeq = replySelection?.rootSeq;
    const replyPersisted = replySelection?.taskContext ? records : replyHistory?.records || [];
    const replyMessages = [...replyPersisted, ...liveMessages.filter(message => !replyPersisted.some(record => record.seq === message.seq || (message.source_id && record.provider_item_id === message.source_id))).map(message => ({ ...message, role: "assistant" }))];
    const relatedSeqs = new Set(rootSeq ? [rootSeq, ...replyPersisted.map(record => record.seq)] : []);
    for (let changed = true; changed;) { changed = false; for (const record of replyMessages) if (Number.isSafeInteger(record.seq) && !relatedSeqs.has(record.seq) && record.relation?.reply_to?.session_id === conversationID && relatedSeqs.has(record.relation.reply_to.seq)) { relatedSeqs.add(record.seq); changed = true; } }
    const taskSource = replySelection?.taskContext?.source;
    const taskSourceRef = taskSource?.message_binding === "host_turn" && taskSource.session_id === conversationID && taskSource.message_seq ? { session_id: conversationID, seq: taskSource.message_seq } : undefined;
    const replyRecords = replySelection ? replyMessages.filter(record => ["user", "assistant"].includes(record.role) && (replySelection.taskContext ? record.seq !== taskSourceRef?.seq && record.relation?.metadata?.task_links?.some(link => link.task_id === replySelection.taskContext.task_id) : relatedSeqs.has(record.seq) || record.relation?.reply_to?.session_id === conversationID && relatedSeqs.has(record.relation.reply_to.seq))) : [];
    const pendingReceipts = [...receiptStates.current.entries()].filter(([_, entry]) => (entry.receipt?.session_id || entry.sessionID) === conversationID && (!entry.receipt?.message_seq || entry.draftID && !entry.inHistory) && !records.some(record => record.receipt?.receipt_id && record.receipt.receipt_id === entry.receipt?.receipt_id));
    const renderPendingReceipt = ([requestID, entry], scopeID) => h(ConversationMessage, { className: sendMotion.className(requestID), key: `pending:${requestID}`, id: `pending:${scopeID}:${requestID}`, role: "user", content: entry.prompt || "", attachments: entry.attachments?.length ? h("div", null, entry.attachments.map((file, index) => h("span", { key: index }, file.filename, " "))) : undefined, footer: receiptFooter(entry.receipt, entry), linkPolicy: "external-only" });
    const replyPending = replySelection ? pendingReceipts.filter(([_, entry]) => {
      if (entry.draftID === replySelection.key) return true;
      if (replySelection.taskContext) {
        const expected = replyDraft.replyTarget?.relation?.metadata?.task_links || [];
        return entry.relation?.metadata?.task_links?.some(link => expected.some(target => ["task_id", "requirements_revision", "execution_session_id", "question_request_id"].every(key => link[key] === target[key])));
      }
      return entry.relation?.reply_to?.session_id === conversationID && relatedSeqs.has(entry.relation.reply_to.seq);
    }) : [];
    const sendRefreshNotice = sendRefreshError && h("div", { className: "mosaic-error", role: "alert", "data-send-refresh-error": true }, h("span", null, t("sendRefreshFailed"), " ", sendRefreshError), h(Button, { onClick: refreshAcceptedSend }, t("refreshHistory")));
    const draftNotice = (id, state) => (state.storageError || state.attachmentsMissing) && h("div", { className: "mosaic-draft-notice" },
      state.storageError && h("p", { role: "alert", "data-draft-storage-error": true }, h("span", null, locale?.startsWith("zh") ? "草稿恢复或保存失败，请在关闭前重试（最多保存 32K 字符）。" : "Draft recovery or saving failed. Retry before closing (32K character limit)."), h(Button, { "data-action": "retry-draft-save", onClick: () => updateDraft(id, {}) }, t("retry"))),
      state.attachmentsMissing && h("p", { role: "status", "data-draft-attachments-missing": true }, h("span", null, locale?.startsWith("zh") ? "附件需重新选择；确认不带附件后才能直接发送。" : "Select the attachments again, or confirm sending without them."), h(Button, { "data-action": "continue-without-attachments", onClick: () => updateDraft(id, { attachmentsMissing: false, pending: null }) }, locale?.startsWith("zh") ? "不带附件继续" : "Continue without attachments")));

    const replyPanel = replySelection && h("section", { ref: replyPanelRef, className: "mosaic-replies", role: "dialog", "aria-modal": true, "aria-label": t("replies"), "data-reply-panel": replySelection.key, onKeyDown: event => {
      if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); closeReplies(); }
      if (event.key === "Tab") { const controls = [...replyPanelRef.current.querySelectorAll('button:not(:disabled),textarea:not(:disabled),input:not(:disabled),a[href]')].filter(node => node.tabIndex >= 0 && !node.closest('[hidden]')); const first = controls[0], last = controls[controls.length - 1]; if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus(); } else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus(); } }
    } }, h("button", { type: "button", className: "mosaic-replies-close", "data-action": "close-replies", "aria-label": t("close"), onClick: closeReplies }, h("svg", { viewBox: "0 0 24 24", fill: "none", stroke: "currentColor", strokeWidth: 1.5, "aria-hidden": true }, h("path", { d: "m6 6 12 12M18 6 6 18" }))), sendRefreshNotice, h(ConversationView, { presentation: "bubbles", conversationId: replySelection.key, title: "", showHistorySearch: false, showJumpToLatest: false, searchLabel: "", searchContent: null,
      composer: h(React.Fragment, null, draftNotice(replySelection.key, replyDraft), h(ConversationComposer, { conversationId: replySelection.key, prompt: replyDraft.prompt, placeholder: t("placeholder"), running: false, submitting: replyDraft.submitting, disabled: loading, sendDisabled: replyDraft.submitting || readingFiles || replyDraft.attachmentsMissing, attachments: replyDraft.files.map((file, index) => ({ ...file, id: String(index) })), onChangePrompt: value => updateDraft(replySelection.key, { prompt: value }), onAddAttachments: files => selectFiles(files, replySelection.key), onRemoveAttachment: id => updateDraft(replySelection.key, { files: replyDraft.files.filter((_, index) => String(index) !== id) }), onSend: text => send(text, replySelection.key), onStop: stopReply })) },
      replyDraft.error && h("p", { role: "alert", className: "mosaic-reply-status" }, replyDraft.error),
      replySelection.taskContext ? h("div", { className: "mosaic-reply-task-context" }, h("span", null, t("replyTaskContext")), h("p", null, replySelection.taskContext.title), !taskSourceRef && replySelection.taskContext.goal && h("p", null, replySelection.taskContext.goal)) : null,
      taskSourceRef ? h(ReplyOriginal, { reference: taskSourceRef, records, sessionID: conversationID, agentID: agent.agent_id, t, renderMessage: renderReplyMessage }) : null,
      replyRecords.map(record => renderReplyMessage(record)), replyPending.map(entry => renderPendingReceipt(entry, replySelection.key))));
    return h(AttachmentOpenContext.Provider, { value: () => closePanel(false) }, h("div", { ref: rootRef, className: "mosaic", "data-wuu-personal": true, "data-panel-open": panelOpen }, h("main", { className: "mosaic-main", inert: nameOpen || Boolean(replySelection) || Boolean(selectedRoutine), "aria-hidden": nameOpen || Boolean(replySelection) || Boolean(selectedRoutine) || undefined },
      !replySelection && sendRefreshNotice,
      !replySelection && agent && draftNotice(conversationID, draft),
      error && h("div", { className: "mosaic-error", role: "alert" }, h("span", null, error), h(Button, { onClick: agent ? () => { setError(""); Promise.all([loadConversation(agent), loadTasks(), loadRoutines()]).catch(report); } : initialize }, t("retry"))),
      !agent ? loading ? h(LoadingState, { label: "twin" }) : h(EmptyState, { title: t("hostUnavailable") }) : h(ConversationView, {
        presentation: "bubbles", conversationId: conversationID, sessionId: conversationID, sessionScope: route, onQuestionReply: replyToQuestion, title: "twin", loading,
        actions: h("div", { className: "mosaic-identity" }, h("button", { ref: profileTrigger, type: "button", className: "mosaic-profile-trigger", "data-action": "toggle-personal-panel", "aria-label": `${t("profile")} · ${displayName} · ${statusText}`, "aria-hidden": panelOpen, inert: panelOpen, "data-queued-count": queuedCount, "aria-haspopup": "dialog", "aria-controls": panelID, "aria-expanded": panelOpen, onClick: () => { if (panelOpen) closePanel(); else openPanel(); } }, avatar()), h("button", { type: "button", className: "mosaic-identity-label", "data-action": "rename-personal", "aria-label": `${t("rename")} · ${displayName}`, tabIndex: panelOpen ? -1 : 0, onClick: openRename }, h("strong", null, displayName))),
        showHistorySearch: false, showJumpToLatest: !replySelection, searchLabel: t("historySearch"), searchContent: h(AgentHistorySearch, { key: conversationID, agentId: agent.agent_id, name: "twin", t }),
        composer: h("div", { hidden: Boolean(replySelection) }, h(ConversationComposer, { conversationId: conversationID, prompt: draft.prompt, placeholder: t("placeholder"), running, submitting: draft.submitting, queuedCount, stopState, disabled: loading, sendDisabled: draft.submitting || readingFiles || draft.attachmentsMissing,
          attachments: files.map((file, index) => ({ ...file, id: String(index) })), onChangePrompt: value => updateDraft(conversationID, { prompt: value }), onAddAttachments: selectFiles,
          onRemoveAttachment: id => updateDraft(conversationID, { files: files.filter((_, index) => String(index) !== id) }), onSend: send, onStop: stopReply }))
      }, records.filter(record => (record.ui_parts?.length || !liveMessages.some(message => message.seq === record.seq || (message.source_id && message.source_id === record.provider_item_id))) && ["user", "assistant"].includes(record.role) && (record.display_content || record.content || record.attachments?.length)).map(record => h(ConversationMessage, {
          className: sendMotion.className(record.receipt?.request_id), interactive: interactiveFor(record), footer: receiptFooter(record.receipt), reaction: reactionFor(record), replyPreview: record.role === "user" && record.relation?.reply_to ? h(ReplyReference, { reference: record.relation.reply_to, records, sessionID: conversationID, agentID: agent.agent_id, t, onOpen: replyMessage }) : undefined, onReply: record.role === "assistant" && !record.ui_parts?.length && !record.ui_fallback && Number.isSafeInteger(record.seq) && record.seq > 0 ? () => replyMessage(record) : undefined, replyLabel: t("replyMessage"), key: record.seq, id: `${conversationID}:${record.seq}`, timestamp: record.at, role: record.role, latest: record === records.findLast(item => item.role === "assistant"), content: record.ui_parts?.length ? record.ui_text || "" : record.display_content || record.content || "", linkPolicy: "external-only",
          attachments: record.attachments?.length > 0 ? h(React.Fragment, null, record.attachments.map(attachment => h(Attachment, { key: `${conversationID}:${record.seq}:${attachment.kind}:${attachment.index}:${attachment.sha256}`, agentId: agent.agent_id, seq: record.seq, attachment, t }))) : null
        })), pendingReceipts.map(entry => renderPendingReceipt(entry, conversationID)), liveMessages.filter(message => !records.some(record => record.ui_parts?.length && (message.seq === record.seq || (message.source_id && message.source_id === record.provider_item_id)))).map(message => h(ConversationMessage, { reaction: reactionFor({ ...message, role: "assistant" }), onReply: Number.isSafeInteger(message.seq) && message.seq > 0 ? () => replyMessage({ ...message, role: "assistant" }) : undefined, replyLabel: t("replyMessage"), key: `live:${message.turn_id}:${message.id}`, id: `personal:${conversationID}:${message.turn_id}:${message.id}`, turnId: `personal:${conversationID}:${message.turn_id}`, role: "assistant", content: message.content, streaming: message.streaming, latest: true, linkPolicy: "external-only" })), !loading && !records.length && !pendingReceipts.length && !liveMessages.length && h("div", { className: "mosaic-empty" }, h(EmptyState, { title: t("emptyChat"), description: t("emptyChatText") })),
        h(settingsPanels.ReviewConversation, { locale }), h(settingsPanels.ConnectorConversation),
        pendingCalendarOperations.length > 0 && h("section", { className: "mosaic-calendar-proposals", "data-calendar-proposals": true },
          pendingCalendarOperations.map(item => h(Button, { key: item.operation_id, variant: "ghost", "data-calendar-operation": item.operation_id, disabled: calendarBusy, onClick: () => openCalendar(item.operation_id) }, `${item.fields?.summary || t("calendarReview")} · ${t(item.status === "proposed" ? "calendarProposed" : item.status === "uncertain" ? "calendarUncertain" : item.status)}`)),
          calendarError && h("p", { role: "alert" }, calendarError)))), h("div", { className: "mosaic-panel-layer", inert: Boolean(selectedRoutine), "aria-hidden": Boolean(selectedRoutine) || undefined, "data-panel-phase": panelPhase }, sidebar), replyPanel, selectedRoutine && h(RoutineDialog, { key: selectedRoutine.routine_id, routine: selectedRoutine, locale, t, onClose: closeRoutine, onChanged: changeRoutine, onOpenTask: item => { setTasks(current => current.some(value => value.task_id === item.task_id) ? current : [...current, item]); setRoutineID(null); detailTrigger.current = `[data-routine-id="${selectedRoutine.routine_id}"]`; setSelection({ taskId: item.task_id, kind: "task" }); } }), nameOpen && h(RenameDialog, { agent, t, onSave: saveName, onClose: closeRename })));
  }
  // A cold/offline host keeps the manifest title until the normal view initialization succeeds.
  call("agents.list").then(result => { if (!api.signal.aborted) publishIdentity(result.agents?.find(value => value.kind === "main")); }).catch(() => {});
  api.registerViewType({ id: "personal.home", title: "twin", icon: "mascot", defaultRegion: "primary", persistence: "durable", layout: "user-space", render: props => h(PersonalHome, props) });
  api.registerViewType({ id: "personal.preferences", title: "twin", icon: "settings", defaultRegion: "settings", persistence: "session", render: props => h(PersonalPreferences, props) });
}
