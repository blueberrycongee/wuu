export function activate(api) {
  const React = api.react;
  api.registerLocale({ id: "deep-field", locale: "en-US", entries: {
    "deepField.recentSessions": "Recent sessions",
    "deepField.noSessions": "No recent conversations yet.",
    "deepField.untitled": "Untitled conversation",
    "deepField.openFailed": "Could not open that conversation.",
  } });
  api.registerLocale({ id: "deep-field", locale: "zh-CN", entries: {
    "deepField.recentSessions": "最近会话",
    "deepField.noSessions": "还没有最近的对话。",
    "deepField.untitled": "未命名对话",
    "deepField.openFailed": "无法打开该对话。",
  } });
  api.registerCSSSnippet({
    id: "deep-field-welcome",
    css: `
      .deep-field-welcome { --deep-field-space: calc(var(--wuu-space-unit, 0.25rem) * var(--wuu-space-density, 1)); display: grid; gap: calc(var(--deep-field-space) * 6); justify-items: stretch; width: min(760px, 100%); min-width: 0; margin: 0 auto; padding: calc(var(--deep-field-space) * 8) calc(var(--deep-field-space) * 4); }
      .deep-field-grid { display: grid; grid-template-columns: minmax(0, 1fr); gap: calc(var(--deep-field-space) * 4); align-items: start; min-width: 0; }
      .deep-field-card { min-width: 0; border-radius: var(--wuu-radius-panel, 16px); }
      .deep-field-card-heading { margin: 0 0 calc(var(--deep-field-space) * 2); font-size: var(--wuu-font-size-ui, 15px); font-weight: 600; color: var(--wuu-color-text-muted); }
      .deep-field-sessions { display: grid; grid-template-columns: minmax(0, 1fr); gap: calc(var(--deep-field-space) * 1); min-width: 0; }
      .deep-field-session { all: unset; display: flex; justify-content: space-between; gap: calc(var(--deep-field-space) * 3); padding: calc(var(--deep-field-space) * 2) calc(var(--deep-field-space) * 2.5); min-width: 0; border-radius: var(--wuu-radius-control, 8px); cursor: pointer; color: var(--wuu-color-text); font: inherit; }
      .deep-field-session:hover { background: var(--wuu-nav-item-hover-background); }
      .deep-field-session:focus-visible { outline: 2px solid var(--wuu-color-focus); outline-offset: -2px; }
      .deep-field-session-title { flex: 1 1 auto; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
      .deep-field-session-time { flex: none; color: var(--wuu-color-text-muted); }
      .deep-field-empty { color: var(--wuu-color-text-muted); padding: calc(var(--deep-field-space) * 2) calc(var(--deep-field-space) * 2.5); }
    `,
  });
  function recentThreads(snapshot) {
    return (snapshot.navigationWorkspace?.threads ?? [])
      .filter(thread => !thread.archived && !thread.ephemeral && !thread.parent_id && thread.turn_count > 0)
      .sort((a, b) => (b.updated_at ?? "").localeCompare(a.updated_at ?? ""))
      .slice(0, 3);
  }
  function relativeTime(iso, locale) {
    const then = Date.parse(iso ?? "");
    if (!Number.isFinite(then)) return "";
    const seconds = Math.round((then - Date.now()) / 1000);
    const rtf = new Intl.RelativeTimeFormat(locale, { numeric: "auto" });
    for (const [unit, span] of [["day", 86400], ["hour", 3600], ["minute", 60]]) {
      if (Math.abs(seconds) >= span) return rtf.format(Math.round(seconds / span), unit);
    }
    return rtf.format(seconds, "second");
  }
  function WelcomeDashboard({ context, application, locale, translate }) {
    const Card = api.ui.Card;
    const threads = React.useSyncExternalStore(
      listener => application.subscribe(listener),
      () => application.getSnapshot().navigationWorkspace?.threads,
    );
    const recent = React.useMemo(
      () => recentThreads(application.getSnapshot()), [threads]);
    const openThread = id => application.invoke("thread.activate", { threadId: id })
      .catch(() => api.showNotice({ message: translate("deepField.openFailed"), tone: "error" }));
    const sessions = React.createElement(Card, {
        className: "deep-field-card",
        "aria-label": translate("deepField.recentSessions"),
      },
      React.createElement("h3", { className: "deep-field-card-heading" }, translate("deepField.recentSessions")),
      React.createElement("div", { className: "deep-field-sessions" },
        recent.length === 0
          ? React.createElement("div", { className: "deep-field-empty" }, translate("deepField.noSessions"))
          : recent.map(thread => React.createElement("button", {
              key: thread.id, type: "button", className: "deep-field-session",
              "data-deep-field-thread": thread.id,
              onClick: () => openThread(thread.id),
            },
              React.createElement("span", { className: "deep-field-session-title" },
                thread.title || thread.preview || translate("deepField.untitled")),
              React.createElement("span", { className: "deep-field-session-time" },
                relativeTime(thread.updated_at, locale))))));
    return React.createElement("section", { className: "deep-field-welcome" },
      context.greeting,
      React.createElement("div", { className: "deep-field-grid" }, context.usage ?? null, sessions));
  }
  function renderWelcome(props) {
    const { application, children } = props;
    const controls = application.settingsControls;
    // Wrapper contribution: while another skin is selected, children carries
    // the slot's native welcome and we render it unchanged. Hooks live in
    // WelcomeDashboard so the hook count never changes on a skin switch.
    const selected = React.useSyncExternalStore(
      listener => controls.subscribeShellSkins(listener),
      () => controls.getShellSkins().selected,
    );
    const active = controls.getShellSkins().skins.find(skin => skin.key === selected);
    // Skin pluginId carries the installation identity (api.pluginId); the
    // manifest id appears only inside the skin key prefix.
    if (active?.id !== "deep-field" || active.pluginId !== api.pluginId) {
      return React.createElement(React.Fragment, null, children);
    }
    return React.createElement(WelcomeDashboard, props);
  }
  // Registered as a wrapper so the native welcome stays reachable through
  // `children`; render decides per selected skin (see renderWelcome).
  api.registerUIContribution({
    id: "welcome",
    target: "wuu.default-conversation/welcome",
    mode: "wrap",
    render: renderWelcome,
  });
}
