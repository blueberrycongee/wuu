import type {
  ComposerSnapshotV1,
  ConversationItemSnapshotV1,
  FilePreviewSnapshotV1,
  HeaderSnapshotV1,
  NavigationSnapshotV1,
  PluginGenerationApi,
  PresentationHost,
  StatusSnapshotV1,
  ViewHostAPI,
} from "@wuu/plugin-sdk";

const VIEW_ID = "acceptance-counter";
const COMMAND_ID = "open-acceptance-counter";
const STORAGE_KEY = "counter";
let activeViewHost: ViewHostAPI | undefined;

type ViewElement = {
  querySelector(selector: string): ViewElement | null;
  textContent: string | null;
  disabled?: boolean;
  dataset?: Record<string, string | undefined>;
};

interface AcceptanceSettings {
  enabled: boolean;
  label: string;
  step: number;
  density: string;
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
  api.registerPresenter({
    id: "assistant-message",
    target: "conversation.item",
    key: "assistant-message",
    mode: "wrap",
    render({ snapshot, fallback }) {
      const item = snapshot as ConversationItemSnapshotV1;
      return observedFallback(api, fallback, {
          "data-developer-loop-presenter": "conversation-item",
          "data-item-id": item.id,
          "data-item-kind": item.kind,
          "data-item-status": item.status ?? "unknown",
      });
    },
  });
  api.registerPresenter({
    id: "composer",
    target: "conversation.composer",
    mode: "wrap",
    render({ snapshot, fallback }) {
      const composer = snapshot as ComposerSnapshotV1;
      return observedFallback(api, fallback, {
          "data-developer-loop-presenter": "composer",
          "data-thread-id": composer.threadId ?? "none",
          "data-submission-mode": composer.activeSubmissionMode ?? "send",
      });
    },
  });
  api.registerPresenter({
    id: "primary-navigation",
    target: "navigation.primary",
    mode: "wrap",
    render({ snapshot, fallback }) {
      const navigation = snapshot as NavigationSnapshotV1;
      return observedFallback(api, fallback, {
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
  fallback: unknown,
  attributes: Readonly<Record<string, string>>,
): unknown {
  return api.react.createElement(
    api.react.Fragment as (props: Readonly<Record<string, unknown>>) => unknown,
    null,
    fallback,
    api.react.createElement("span", { ...attributes, hidden: true, "aria-hidden": true }),
  );
}

function acceptanceView(api: PluginGenerationApi, props: Readonly<Record<string, unknown>>): unknown {
  const host = (props as unknown as { host: ViewHostAPI }).host;
  const { Button, Card, Page, Row, Section, Stack } = api.ui;
  activeViewHost = host;
  let count = 0;
  let settings: AcceptanceSettings = {
    enabled: true,
    label: "Accepted runs",
    step: 1,
    density: "comfortable",
  };

  const update = (root: ViewElement): void => {
    const value = root.querySelector("[data-counter-value]");
    const label = root.querySelector("[data-counter-label]");
    const button = root.querySelector("[data-counter-button]");
    if (value) value.textContent = String(count);
    if (label) label.textContent = settings.label;
    if (button) button.disabled = !settings.enabled;
    if (root.dataset) root.dataset.wuuDensity = settings.density;
  };

  const restore = async (root: ViewElement): Promise<void> => {
    const [stored, enabled, label, step, density] = await Promise.all([
      host.getStorage(STORAGE_KEY),
      host.getSetting("enabled"),
      host.getSetting("label"),
      host.getSetting("step"),
      host.getSetting("density"),
    ]);
    const restored = Number.parseInt(stored ?? "0", 10);
    count = Number.isFinite(restored) ? restored : 0;
    settings = {
      enabled: typeof enabled === "boolean" ? enabled : true,
      label: typeof label === "string" ? label : "Accepted runs",
      step: typeof step === "number" && Number.isFinite(step) ? step : 1,
      density: typeof density === "string" ? density : "comfortable",
    };
    update(root);
  };

  let root: ViewElement | null = null;
  return api.react.createElement(
    Page,
    {
      ref: (node: ViewElement | null) => {
        root = node;
        if (node) void restore(node);
      },
    },
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
          api.react.createElement("strong", { "data-counter-value": "" }, "0"),
          api.react.createElement(
            Row,
            null,
            api.react.createElement(
              Button,
              {
                variant: "primary",
                "data-counter-button": "",
                onClick: async () => {
                  if (!settings.enabled) return;
                  count += settings.step;
                  await host.setStorage(STORAGE_KEY, String(count));
                  if (root) update(root);
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
  api.registerToolActivityPresenter({
    id: "developer-loop-echo",
    key: "developer-loop.tool.echo",
    render({ activity, fallback }) {
      return api.react.createElement(
        "section",
        { "data-developer-loop-tool": activity.status, "data-tool-id": activity.id },
        api.react.createElement("strong", null, "Developer loop"),
        api.react.createElement("span", null, activity.resultText ?? activity.argumentsText ?? "Running"),
        activity.error ? api.react.createElement("span", { role: "alert" }, activity.error) : fallback,
      );
    },
  });
  api.registerViewType({
    id: VIEW_ID,
    title: "Acceptance Counter",
    icon: "check-circle",
    defaultRegion: "auxiliary",
    persistence: "durable",
    render: (props: Readonly<Record<string, unknown>>) => acceptanceView(api, props),
  });
  api.registerViewType({
    id: "acceptance-navigation",
    title: "Developer Loop Navigation",
    icon: "check-circle",
    persistence: "durable",
    render: () => api.react.createElement(api.ui.Page, null,
      api.react.createElement(api.ui.Section, { title: "Developer Loop" },
        "The counter stays in the main area. Switch destinations to check that both views restore together.")),
  });
  api.registerDestination({
    id: "acceptance",
    title: "Developer Loop",
    icon: "check-circle",
    order: 100,
    primaryViewType: VIEW_ID,
    sidebarViewType: "acceptance-navigation",
  });
  api.registerRibbonItem({
    id: "acceptance",
    title: "Developer Loop",
    icon: "check-circle",
    order: 100,
    target: { kind: "destination", destinationId: "acceptance" },
  });
  api.registerRibbonItem({
    id: "open-counter-panel",
    title: "Open Counter Panel",
    icon: "layout-grid",
    order: 110,
    target: { kind: "command", commandId: COMMAND_ID },
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
  api.registerSlot("conversation.header", {
    id: "developer-loop-status",
    render() {
      return api.react.createElement("span", null, `Plugin generation ${api.generation}`);
    },
  });
}
