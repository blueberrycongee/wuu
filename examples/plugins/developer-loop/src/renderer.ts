import type {
  ConversationItemSnapshotV1,
  FilePreviewSnapshotV1,
  HeaderSnapshotV1,
  PluginGenerationApi,
  PresentationHost,
  PresenterProps,
  StatusSnapshotV1,
  ToolActivitySnapshot,
  ViewHostAPI,
  ViewRenderProps,
} from "@wuu/plugin-sdk";

const VIEW_ID = "acceptance-counter";
const COMMAND_ID = "open-acceptance-counter";
const STORAGE_KEY = "counter";
let activeViewHost: ViewHostAPI | undefined;

interface AcceptanceSettings {
  enabled: boolean;
  label: string;
  step: number;
  density: "comfortable" | "compact";
}

export async function invokePresentationAction(
  host: PresentationHost,
  action: string,
  input?: unknown,
): Promise<unknown> {
  if (!host.actions.includes(action)) return undefined;
  return host.invoke(action, input);
}

function registerAcceptancePresenters(api: PluginGenerationApi): void {
  for (const owner of ["wuu.default-conversation", "wuu.default-preview"]) api.registerUIContribution({
    id: `${owner}-assistant-message`,
    target: `${owner}/conversation.message`,
    mode: "wrap",
    render({ context, children }) {
      const item = context.snapshot as ConversationItemSnapshotV1;
      if (item.kind !== "assistant-message") return children;
      return observedFallback(api, children, {
          "data-developer-loop-presenter": "conversation-item",
          "data-item-id": item.id,
          "data-item-kind": item.kind,
          "data-item-status": item.status ?? "unknown",
      });
    },
  });
  api.registerUIContribution({
    id: "composer",
    target: "wuu.default-composer/composer.above",
    render({ application }) {
      const { composer } = api.react.useSyncExternalStore(application.subscribe, application.getSnapshot, application.getSnapshot);
      return api.react.createElement("span", {
        hidden: true,
        "data-developer-loop-presenter": "composer",
        "data-thread-id": composer.threadId ?? "none",
        "data-submission-mode": composer.activeSubmissionMode ?? "send",
      });
    },
  });
  api.registerUIContribution({
    id: "primary-navigation",
    target: "wuu.default-navigation/sidebar.footer",
    render({ application }) {
      const { navigation } = api.react.useSyncExternalStore(application.subscribe, application.getSnapshot, application.getSnapshot);
      return api.react.createElement("span", {
        hidden: true,
        "data-developer-loop-presenter": "navigation",
        "data-active-node": navigation.activeNodeId ?? "none",
        "data-node-count": String(navigation.nodes.length),
      });
    },
  });
  api.registerPresenter({
    id: "markdown-preview",
    target: "content.preview",
    key: "text/markdown",
    mode: "wrap",
    render({ snapshot, fallback }) {
      const preview = snapshot as FilePreviewSnapshotV1;
      return observedFallback(api, fallback, {
          "data-developer-loop-presenter": "content-preview",
          "data-resource-id": preview.resourceId,
          "data-content-type": preview.contentType ?? "unknown",
          "data-read-only": String(preview.readOnly === true),
      });
    },
  });
  api.registerPresenter({
    id: "application-status",
    target: "app.status",
    mode: "wrap",
    render({ snapshot, fallback }) {
      const status = snapshot as StatusSnapshotV1;
      return observedFallback(api, fallback, {
        "data-developer-loop-presenter": "app-status",
        "data-status-count": String(status.items.length),
      });
    },
  });
  api.registerPresenter({
    id: "conversation-header",
    target: "header.conversation",
    mode: "wrap",
    render({ snapshot, fallback }) {
      const header = snapshot as HeaderSnapshotV1;
      return observedFallback(api, fallback, {
          "data-developer-loop-presenter": "conversation-header",
          "data-header-scope": header.scope,
          "data-active-tab": header.activeTabId ?? "none",
      });
    },
  });
}

function observedFallback(
  api: PluginGenerationApi,
  fallback: PresenterProps["fallback"],
  attributes: Readonly<Record<string, string>>,
) {
  return api.react.createElement(
    api.react.Fragment,
    null,
    fallback,
    api.react.createElement("span", { ...attributes, hidden: true, "aria-hidden": true }),
  );
}

function acceptanceView(api: PluginGenerationApi, props: ViewRenderProps) {
  const { host } = props;
  const { Button, Card, Page, Row, Section, Stack } = api.ui;
  activeViewHost = host;
  const [count, setCount] = api.react.useState<number>();
  const [settings, setSettings] = api.react.useState<AcceptanceSettings>({
    enabled: true, label: "Accepted runs", step: 1, density: "comfortable",
  });
  const [saving, setSaving] = api.react.useState(false);
  const [error, setError] = api.react.useState<string>();
  const pending = api.react.useRef(false);
  api.react.useEffect(() => {
    let active = true;
    setCount(undefined);
    setError(undefined);
    void Promise.all([
      host.getStorage(STORAGE_KEY), host.getSetting("enabled"), host.getSetting("label"),
      host.getSetting("step"), host.getSetting("density"),
    ]).then(([stored, enabled, label, step, density]) => {
      if (!active) return;
      const restored = Number.parseInt(stored ?? "0", 10);
      setCount(Number.isFinite(restored) ? restored : 0);
      setSettings({
        enabled: typeof enabled === "boolean" ? enabled : true,
        label: typeof label === "string" ? label : "Accepted runs",
        step: typeof step === "number" && Number.isFinite(step) ? step : 1,
        density: density === "compact" ? "compact" : "comfortable",
      });
    }).catch(error => { if (active) setError(String(error)); });
    return () => { active = false; };
  }, [host]);
  return api.react.createElement(
    Page,
    { density: settings.density },
    api.react.createElement(
      Stack,
      { gap: "large" },
      api.react.createElement(Section, {
        title: api.react.createElement("span", { "data-counter-label": "" }, settings.label),
        description: "The same host-owned view can appear in navigation, Settings, or the right panel.",
      },
      api.react.createElement(
        Card,
        null,
        api.react.createElement(
          Stack,
          null,
          api.react.createElement("span", null, "SDK v3 acceptance"),
          api.react.createElement("strong", { "data-counter-value": "" }, count === undefined ? "Loading…" : String(count)),
          error ? api.react.createElement("span", { role: "alert" }, error) : null,
          api.react.createElement(
            Row,
            null,
            api.react.createElement(
              Button,
              {
                variant: "primary",
                "data-counter-button": "",
                disabled: count === undefined || !settings.enabled || saving,
                onClick: async () => {
                  if (count === undefined || !settings.enabled || pending.current) return;
                  pending.current = true;
                  setSaving(true);
                  setError(undefined);
                  try {
                    const next = count + settings.step;
                    await host.setStorage(STORAGE_KEY, String(next));
                    setCount(next);
                  } catch (error) {
                    setError(String(error));
                  } finally {
                    pending.current = false;
                    setSaving(false);
                  }
                },
              },
              "Increment and save",
            ),
          ),
        ),
      )),
    ),
  );
}

export function activate(api: PluginGenerationApi): void {
  registerAcceptancePresenters(api);
  for (const owner of ["wuu.default-conversation", "wuu.default-preview"]) {
    api.registerUIContribution({
      id: `developer-loop-echo-${owner}`,
      target: `${owner}/conversation.tool-activity`,
      mode: "wrap",
      render({ context, children }) {
        const activity = context.snapshot as ToolActivitySnapshot;
        if ((activity.capability ?? activity.toolName) !== "developer-loop.tool.echo") return children;
        return api.react.createElement(
          "section",
          { "data-developer-loop-tool": activity.status, "data-tool-id": activity.id },
          api.react.createElement("strong", null, "Developer loop"),
          api.react.createElement("span", null, activity.resultText ?? activity.argumentsText ?? "Running"),
          activity.error ? api.react.createElement("span", { role: "alert" }, activity.error) : children,
        );
      },
    });
  }
  api.registerViewType({
    id: VIEW_ID,
    title: "Acceptance Counter",
    icon: "check-circle",
    defaultRegion: "auxiliary",
    persistence: "durable",
    render: props => acceptanceView(api, props),
  });
  api.registerViewPlacement({
    id: "acceptance-tools",
    region: "auxiliary",
    view: VIEW_ID,
  });
  api.registerThemeTokens({
    theme: "developer-focus",
    base: "dark",
    tokens: {
      "--wuu-color-canvas": "#111827",
      "--wuu-color-text": "#f9fafb",
      "--wuu-font-family-ui": "ui-sans-serif, system-ui, sans-serif",
      "--wuu-font-size-body": "14px",
      "--wuu-space-density": "0.875",
      "--wuu-space-unit": "4px",
      "--wuu-radius-inner": "6px",
      "--wuu-radius-control": "8px",
      "--wuu-border-subtle": "1px solid #374151",
      "--wuu-elevation-panel": "0 12px 32px rgb(0 0 0 / 0.28)",
      "--wuu-motion-duration-fast": "120ms",
      "--wuu-motion-easing-standard": "cubic-bezier(0.2, 0, 0, 1)",
      "--wuu-content-max-width": "72rem",
    },
    syntax: {
      "--wuu-syntax-keyword": "#c084fc",
      "--wuu-syntax-function": "#67e8f9",
      "--wuu-syntax-string": "#86efac",
      "--wuu-syntax-comment": "#9ca3af",
    },
  });
  api.registerCommand({
    id: COMMAND_ID,
    title: "Open Acceptance Counter",
    async execute() {
      if (!activeViewHost) {
        return { viewTypeId: VIEW_ID, region: "auxiliary", persistence: "durable" };
      }
      await activeViewHost.openView(VIEW_ID, {
        region: "auxiliary",
        persistence: "durable",
        reveal: true,
      });
    },
  });
  api.registerStatusItem({
    id: "acceptance-ready",
    label: "Acceptance ready",
    icon: "check",
    tooltip: "Open the persistent SDK acceptance counter",
    command: COMMAND_ID,
    priority: 50,
  });
  api.registerLocale({
    id: "acceptance-en",
    locale: "en",
    entries: {
      "developerLoop.counter.title": "Acceptance Counter",
      "developerLoop.counter.increment": "Increment and save",
    },
  });
  api.registerLocale({
    id: "acceptance-zh-cn",
    locale: "zh-CN",
    entries: {
      "developerLoop.counter.title": "验收计数器",
      "developerLoop.counter.increment": "增加并保存",
    },
  });
  api.registerUIContribution({
    target: "wuu.default-shell/conversation.header",
    id: "developer-loop-status",
    render() {
      return api.react.createElement("span", null, `Plugin generation ${api.generation}`);
    },
  });
}
