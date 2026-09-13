/**
 * Workbench API — Phase C
 *
 * Stable contract for desktop workbench customization. Plugins use these
 * types to contribute views, placements, renderers, theme tokens, CSS
 * snippets, settings, and namespaced storage without importing Wuu
 * private source.
 *
 * This module defines the public surface. The PluginHost consumes these
 * types to wire contributions into the renderer; plugins receive a
 * PluginGenerationApi that exposes registration methods for each category.
 */

import type * as React from "react";
import { createComposerDrawer, type ComposerDrawerProps } from "./ComposerDrawer";
import type {
  PublicIconName,
  PublicSyntaxTokenName,
  PublicThemeTokenName,
} from "./themeContract.generated";

export {
  LEGACY_THEME_TOKEN_ALIASES,
  PUBLIC_ICON_NAMES,
  PUBLIC_SYNTAX_TOKEN_NAMES,
  PUBLIC_THEME_TOKEN_NAMES,
  canonicalThemeTokenName,
  isPublicIconName,
  isPublicSyntaxTokenName,
  isPublicThemeTokenName,
  type PublicIconName,
  type PublicSyntaxTokenName,
  type PublicThemeTokenName,
} from "./themeContract.generated";

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

/** Unique identifier for a view type registered by a plugin. */
export type ViewTypeId = string;

/** Where a view instance appears in the workbench. */
/** Stable host-owned semantic regions available for View placement. */
export const VIEW_PLACEMENT_REGIONS = [
  "navigation",
  "primary",
  "auxiliary",
  "inspector",
  "settings",
  "overlay",
] as const;
export type ViewPlacementRegion = (typeof VIEW_PLACEMENT_REGIONS)[number];

/** Persistence policy for a view instance. */
export type ViewPersistence = "session" | "durable";

/**
 * A plugin-registered view type. Each view type defines a React component
 * plus metadata. The host opens instances of this type in a pane.
 */
export interface ViewTypeDefinition {
  /** Stable dotted identifier, e.g. "my-plugin.dashboard". */
  id: ViewTypeId;
  /** User-facing title for the view tab or header. */
  title: string;
  /** Semantic icon identifier from the public host icon set. */
  icon?: PublicIconName;
  /** Default semantic region when the host opens this view. */
  defaultRegion?: ViewPlacementRegion;
  /** Whether this view's state survives a session restart. */
  persistence?: ViewPersistence;
  /** React component that receives host context and renders the view. */
  render: React.ComponentType<ViewRenderProps>;
}

/** Context the host passes to every view component on render. */
export interface ViewRenderProps {
  /** Opaque host API for controlled interactions. */
  host: ViewHostAPI;
  /** Immutable context snapshot for this view instance. */
  context: Readonly<Record<string, unknown>>;
  /** Active Wuu locale, such as zh-CN or en-US. */
  locale: string;
  /** Resolve a namespaced entry contributed through registerLocale(). */
  translate(key: string, values?: Readonly<Record<string, string | number>>): string;
}

export interface SettingsModelAliasV1 {
  readonly provider: string;
  readonly model: string;
  readonly effort?: string;
  readonly variant?: string;
}

export interface SettingsValueMapV1 {
  readonly "runtime.modelAliases": Readonly<Record<string, SettingsModelAliasV1>>;
}

export type SettingsValueKeyV1 = keyof SettingsValueMapV1;

/** Narrow settings service exposed only to plugin views mounted as Settings pages. */
export interface SettingsPageHostAPI {
  readonly contractVersion: 1;
  getValue<Key extends SettingsValueKeyV1>(key: Key): SettingsValueMapV1[Key];
  updateValue<Key extends SettingsValueKeyV1>(key: Key, value: SettingsValueMapV1[Key]): Promise<void>;
}

/** Controlled API surface the host exposes to view components. */
export interface ViewHostAPI {
  /** Read plugin-scoped namespaced storage. */
  getStorage(key: string, scope?: "user" | "workspace"): Promise<string | null>;
  /** Write plugin-scoped namespaced storage. */
  setStorage(key: string, value: string, scope?: "user" | "workspace"): Promise<void>;
  /** Read a plugin setting value. */
  getSetting(key: string): Promise<unknown>;
  /** Execute a registered command. */
  executeCommand(commandId: string, input?: unknown): Promise<unknown>;
  /** Request the host to open a view instance. */
  openView(viewTypeId: ViewTypeId, options?: OpenViewOptions): Promise<void>;
  /** Request the host to close this view instance. */
  closeView(): Promise<void>;
  /** Available only when the view is mounted as a Settings page. */
  readonly settings?: SettingsPageHostAPI;
}

// ---------------------------------------------------------------------------
// Plugin UI Kit
// ---------------------------------------------------------------------------

export interface PluginUIContainerProps extends React.HTMLAttributes<HTMLElement> {
  children?: React.ReactNode;
}

export interface PluginUIPageProps extends PluginUIContainerProps {
  density?: "comfortable" | "compact";
}

export interface PluginUISectionProps extends Omit<PluginUIContainerProps, "title"> {
  title?: React.ReactNode;
  description?: React.ReactNode;
}

export interface PluginUIStackProps extends React.HTMLAttributes<HTMLDivElement> {
  children?: React.ReactNode;
  gap?: "small" | "medium" | "large";
}

export interface PluginUIButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: "primary" | "secondary" | "ghost" | "danger";
}

export interface PluginUIToolbarToggleProps extends Omit<React.ButtonHTMLAttributes<HTMLButtonElement>, "aria-pressed"> {
  pressed: boolean;
  "aria-label": string;
}

export interface PluginUITextInputProps extends Omit<React.InputHTMLAttributes<HTMLInputElement>, "children"> {
  label: React.ReactNode;
  description?: React.ReactNode;
}

export interface PluginUITextAreaProps extends Omit<React.TextareaHTMLAttributes<HTMLTextAreaElement>, "children"> {
  label: React.ReactNode;
  description?: React.ReactNode;
}

export interface PluginUICheckboxProps extends Omit<React.InputHTMLAttributes<HTMLInputElement>, "children" | "type"> {
  label: React.ReactNode;
  description?: React.ReactNode;
}

export interface PluginUISelectProps extends React.SelectHTMLAttributes<HTMLSelectElement> {
  label: React.ReactNode;
  description?: React.ReactNode;
}

export interface PluginUIEmptyStateProps extends Omit<React.HTMLAttributes<HTMLElement>, "title"> {
  title: React.ReactNode;
  description?: React.ReactNode;
  actions?: React.ReactNode;
}

export interface PluginUILoadingStateProps extends Omit<React.HTMLAttributes<HTMLElement>, "title"> {
  title?: React.ReactNode;
  description?: React.ReactNode;
}

export interface PluginUIErrorStateProps extends Omit<React.HTMLAttributes<HTMLElement>, "title"> {
  title: React.ReactNode;
  description?: React.ReactNode;
  actions?: React.ReactNode;
}

export interface PluginUILiveDurationProps extends Omit<React.HTMLAttributes<HTMLSpanElement>, "children"> {
  elapsedMs: number;
  runningSinceMs?: number;
  active?: boolean;
}

/**
 * Small host-owned component set for plugin surfaces. These components own the
 * common visual rhythm while plugins retain freedom inside their contribution.
 */
export interface PluginUIKit {
  readonly ComposerDrawer: React.ComponentType<ComposerDrawerProps>;
  readonly Page: React.ComponentType<PluginUIPageProps>;
  readonly Panel: React.ComponentType<PluginUIContainerProps>;
  readonly Card: React.ComponentType<PluginUIContainerProps>;
  readonly Section: React.ComponentType<PluginUISectionProps>;
  readonly Stack: React.ComponentType<PluginUIStackProps>;
  readonly Row: React.ComponentType<PluginUIContainerProps>;
  readonly Button: React.ComponentType<PluginUIButtonProps>;
  readonly ToolbarToggle: React.ComponentType<PluginUIToolbarToggleProps>;
  readonly TextInput: React.ComponentType<PluginUITextInputProps>;
  readonly TextArea: React.ComponentType<PluginUITextAreaProps>;
  readonly Select: React.ComponentType<PluginUISelectProps>;
  readonly Checkbox: React.ComponentType<PluginUICheckboxProps>;
  readonly EmptyState: React.ComponentType<PluginUIEmptyStateProps>;
  readonly LoadingState: React.ComponentType<PluginUILoadingStateProps>;
  readonly ErrorState: React.ComponentType<PluginUIErrorStateProps>;
  readonly LiveDuration: React.ComponentType<PluginUILiveDurationProps>;
}

export function createPluginUIKit(react: typeof React): PluginUIKit {
  const container = (
    tag: "section" | "article" | "div",
    name: string,
  ): React.ComponentType<PluginUIContainerProps> => function PluginUIContainer({
    className,
    children,
    ...props
  }: PluginUIContainerProps): React.ReactNode {
    return react.createElement(tag, {
      ...props,
      className: joinPluginUIClass(name, className),
      "data-wuu-component": name,
    }, children);
  };

  function Page({ className, density = "comfortable", children, ...props }: PluginUIPageProps): React.ReactNode {
    return react.createElement("section", {
      ...props,
      className: joinPluginUIClass("plugin-ui-page", className),
      "data-wuu-component": "plugin-ui-page",
      "data-wuu-density": density,
    }, children);
  }
  const Panel = container("section", "plugin-ui-panel");
  const Card = container("article", "plugin-ui-card");
  const Row = container("div", "plugin-ui-row");

  function Section({
    className,
    title,
    description,
    children,
    ...props
  }: PluginUISectionProps): React.ReactNode {
    return react.createElement("section", {
      ...props,
      className: joinPluginUIClass("plugin-ui-section", className),
      "data-wuu-component": "plugin-ui-section",
    },
    title !== undefined || description !== undefined
      ? react.createElement("header", { className: "plugin-ui-section-header" },
        title !== undefined ? react.createElement("h2", null, title) : null,
        description !== undefined ? react.createElement("p", null, description) : null,
      )
      : null,
    children);
  }

  function Stack({ className, gap = "medium", children, ...props }: PluginUIStackProps): React.ReactNode {
    return react.createElement("div", {
      ...props,
      className: joinPluginUIClass("plugin-ui-stack", className),
      "data-wuu-component": "plugin-ui-stack",
      "data-wuu-gap": gap,
    }, children);
  }

  function Button({ className, variant = "secondary", type = "button", children, ...props }: PluginUIButtonProps): React.ReactNode {
    return react.createElement("button", {
      ...props,
      type,
      className: joinPluginUIClass("plugin-ui-button", className),
      "data-wuu-component": "plugin-ui-button",
      "data-wuu-variant": variant,
    }, children);
  }

  function ToolbarToggle({
    className,
    pressed,
    type = "button",
    children,
    ...props
  }: PluginUIToolbarToggleProps): React.ReactNode {
    return react.createElement("button", {
      ...props,
      type,
      className: joinPluginUIClass("plugin-ui-toolbar-toggle", className),
      "data-wuu-component": "plugin-ui-toolbar-toggle",
      "aria-pressed": pressed,
    }, children);
  }

  function TextInput({ className, label, description, ...props }: PluginUITextInputProps): React.ReactNode {
    return react.createElement("label", {
      className: "plugin-ui-field",
      "data-wuu-component": "plugin-ui-field",
    },
    react.createElement("span", { className: "plugin-ui-field-label" }, label),
    description !== undefined
      ? react.createElement("span", { className: "plugin-ui-field-description" }, description)
      : null,
    react.createElement("input", {
      ...props,
      className: joinPluginUIClass("plugin-ui-input", className),
      "data-wuu-component": "plugin-ui-input",
    }));
  }

  function TextArea({ className, label, description, ...props }: PluginUITextAreaProps): React.ReactNode {
    return react.createElement("label", {
      className: "plugin-ui-field",
      "data-wuu-component": "plugin-ui-field",
    },
    react.createElement("span", { className: "plugin-ui-field-label" }, label),
    description !== undefined
      ? react.createElement("span", { className: "plugin-ui-field-description" }, description)
      : null,
    react.createElement("textarea", {
      ...props,
      className: joinPluginUIClass("plugin-ui-textarea", className),
      "data-wuu-component": "plugin-ui-textarea",
    }));
  }

  function Select({ className, label, description, children, ...props }: PluginUISelectProps): React.ReactNode {
    return react.createElement("label", {
      className: "plugin-ui-field",
      "data-wuu-component": "plugin-ui-field",
    },
    react.createElement("span", { className: "plugin-ui-field-label" }, label),
    description !== undefined
      ? react.createElement("span", { className: "plugin-ui-field-description" }, description)
      : null,
    react.createElement("select", {
      ...props,
      className: joinPluginUIClass("plugin-ui-input plugin-ui-select", className),
      "data-wuu-component": "plugin-ui-select",
    }, children));
  }

  function Checkbox({ className, label, description, ...props }: PluginUICheckboxProps): React.ReactNode {
    return react.createElement("label", {
      className: joinPluginUIClass("plugin-ui-checkbox", className),
      "data-wuu-component": "plugin-ui-checkbox",
    },
    react.createElement("input", { ...props, type: "checkbox" }),
    react.createElement("span", { className: "plugin-ui-checkbox-copy" },
      react.createElement("span", { className: "plugin-ui-field-label" }, label),
      description !== undefined
        ? react.createElement("span", { className: "plugin-ui-field-description" }, description)
        : null));
  }

  const renderState = (
    kind: "empty" | "loading" | "error",
    className: string | undefined,
    title: React.ReactNode,
    description: React.ReactNode,
    actions: React.ReactNode,
    props: React.HTMLAttributes<HTMLElement>,
  ): React.ReactNode => {
    const component = {
      empty: "plugin-ui-empty-state",
      loading: "plugin-ui-loading-state",
      error: "plugin-ui-error-state",
    }[kind];
    return react.createElement("section", {
      ...props,
      className: joinPluginUIClass(`plugin-ui-state ${component}`, className),
      "data-wuu-component": component,
      "data-wuu-state": kind,
      role: kind === "error" ? "alert" : "status",
      "aria-busy": kind === "loading" ? true : undefined,
    },
    kind === "loading" ? react.createElement("span", { className: "plugin-ui-state-spinner", "aria-hidden": true }) : null,
    title !== undefined ? react.createElement("strong", {
      className: kind === "loading" ? "wuu-live-text-wave" : undefined,
    }, title) : null,
    description !== undefined ? react.createElement("p", null, description) : null,
    actions !== undefined ? react.createElement("div", { className: "plugin-ui-state-actions" }, actions) : null);
  };

  function EmptyState({ className, title, description, actions, ...props }: PluginUIEmptyStateProps): React.ReactNode {
    return renderState("empty", className, title, description, actions, props);
  }

  function LoadingState({ className, title, description, ...props }: PluginUILoadingStateProps): React.ReactNode {
    return renderState("loading", className, title, description, undefined, props);
  }

  function ErrorState({ className, title, description, actions, ...props }: PluginUIErrorStateProps): React.ReactNode {
    return renderState("error", className, title, description, actions, props);
  }

  function LiveDuration({
    className,
    elapsedMs,
    runningSinceMs,
    active = false,
    ...props
  }: PluginUILiveDurationProps): React.ReactNode {
    const nodeRef = react.useRef<HTMLSpanElement | null>(null);
    const value = (): string => formatCompactDuration(
      Math.max(0, elapsedMs) + (
        active && Number.isFinite(runningSinceMs)
          ? Math.max(0, Date.now() - Number(runningSinceMs))
          : 0
      ),
    );
    react.useEffect(() => {
      const update = (): void => {
        if (nodeRef.current) nodeRef.current.textContent = value();
      };
      update();
      if (!active || !Number.isFinite(runningSinceMs)) return undefined;
      const timer = window.setInterval(update, 1_000);
      return () => window.clearInterval(timer);
    }, [active, elapsedMs, runningSinceMs]);
    return react.createElement("span", {
      ...props,
      ref: nodeRef,
      className: joinPluginUIClass("plugin-ui-live-duration", className),
      "data-wuu-component": "plugin-ui-live-duration",
    }, value());
  }

  return Object.freeze({
    ComposerDrawer: createComposerDrawer(react),
    Page,
    Panel,
    Card,
    Section,
    Stack,
    Row,
    Button,
    ToolbarToggle,
    TextInput,
    TextArea,
    Select,
    Checkbox,
    EmptyState,
    LoadingState,
    ErrorState,
    LiveDuration,
  });
}

export function formatCompactDuration(ms: number): string {
  const totalSeconds = Math.max(0, Math.floor(ms / 1_000));
  const hours = Math.floor(totalSeconds / 3_600);
  const minutes = Math.floor((totalSeconds % 3_600) / 60);
  const seconds = totalSeconds % 60;
  if (hours > 0) return `${hours}h ${minutes}m ${seconds}s`;
  if (minutes > 0) return `${minutes}m ${seconds}s`;
  return `${seconds}s`;
}

function joinPluginUIClass(base: string, className?: string): string {
  return className ? `${base} ${className}` : base;
}

export interface OpenViewOptions {
  region?: ViewPlacementRegion;
  context?: Readonly<Record<string, unknown>>;
  persistence?: ViewPersistence;
  reveal?: boolean;
}

export interface InspectorSessionSnapshotV1 {
  readonly id?: string;
  readonly status: "idle" | "running";
  readonly turnId?: string;
  readonly turnStatus?: "in_progress" | "completed" | "failed" | "interrupted";
}

export interface InspectorWorkspaceSnapshotV1 {
  readonly kind: "project" | "no_project";
  readonly cwd: string;
  readonly projectId?: string;
  readonly projectName?: string;
  readonly branch?: string;
  readonly dirtyFileCount?: number;
}

export interface InspectorTodoSnapshotV1 {
  readonly completed: number;
  readonly total: number;
  readonly activeContent?: string;
  readonly items: readonly InspectorTodoItemSnapshotV1[];
}

export interface InspectorTodoItemSnapshotV1 {
  readonly content: string;
  readonly status: "pending" | "in_progress" | "completed";
}

/** Immutable public context for short, scan-friendly Inspector contributions. */
export interface InspectorSnapshotV1 {
  readonly contractVersion: 1;
  readonly session: InspectorSessionSnapshotV1;
  readonly workspace?: InspectorWorkspaceSnapshotV1;
  readonly todo?: InspectorTodoSnapshotV1;
}

export interface InspectorSectionHostAPI {
  executeCommand(commandId: string, input?: unknown): Promise<unknown>;
  openView(viewTypeId: ViewTypeId, options?: OpenViewOptions): Promise<void>;
}

export interface InspectorSectionRenderProps {
  readonly snapshot: InspectorSnapshotV1;
  readonly host: InspectorSectionHostAPI;
}

export interface InspectorSectionDefinition {
  readonly id: string;
  readonly title: string;
  readonly priority?: number;
  /** Return false when this section has no meaningful content for the snapshot. */
  readonly when?: (snapshot: InspectorSnapshotV1) => boolean;
  readonly render: React.ComponentType<InspectorSectionRenderProps>;
}

export const PRESENTATION_TARGETS = [
  "conversation.item", "conversation.process", "conversation.tool-activity",
  "conversation.composer", "header.conversation", "header.workspace",
  "navigation.primary", "app.status", "content.preview", "settings",
] as const;
export type BuiltInPresentationTarget = (typeof PRESENTATION_TARGETS)[number];
export type PresentationTarget = BuiltInPresentationTarget | (string & {});
export type PresentationMode = "replace" | "wrap";

/** Sanitized attachment metadata. The opaque id is resolved only through host actions. */
export interface AttachmentDescriptorV1 {
  readonly id: string;
  readonly name: string;
  readonly mimeType?: string;
  readonly sizeBytes?: number;
  readonly width?: number;
  readonly height?: number;
}

export interface ConversationContentV1 {
  readonly type: "plain-text" | "markdown" | "code";
  readonly text: string;
  readonly language?: string;
}

export interface ConversationToolReferenceV1 {
  readonly id: string;
  readonly name?: string;
  readonly capability?: string;
  readonly status?: "pending" | ToolActivityStatus | "cancelled";
}

export interface ConversationProcessReferenceV1 {
  readonly id: string;
  readonly label?: string;
  readonly phase?: string;
  readonly status?: "pending" | "running" | "completed" | "failed" | "cancelled";
}

/** Public conversation item data, independent of renderer and thread storage types. */
export interface ConversationItemSnapshotV1 {
  readonly contractVersion: 1;
  readonly id: string;
  readonly kind: "user-message" | "assistant-message" | "reasoning" | "notice" | "attachment" | "tool-reference" | "process-reference";
  readonly status?: "pending" | "streaming" | "completed" | "failed" | "cancelled";
  readonly phase?: string;
  readonly text?: string;
  readonly contentType?: "plain-text" | "markdown" | "code";
  readonly content?: readonly ConversationContentV1[];
  readonly attachments?: readonly AttachmentDescriptorV1[];
  readonly toolReferences?: readonly ConversationToolReferenceV1[];
  readonly processReferences?: readonly ConversationProcessReferenceV1[];
  readonly createdAt?: string;
  /** Replacement-context body of a completed context-compaction notice. */
  readonly summary?: string;
  /** Raw user input when it differs from the displayed bubble text. */
  readonly inputText?: string;
}

export type ConversationProcessKindV1 = "reasoning" | "tool-group" | "mixed";
export type ConversationProcessStatusV1 = "running" | "completed" | "failed";

/** One ordered, sanitized entry in the process surface. */
export interface ConversationProcessItemV1 {
  readonly id: string;
  readonly kind: "reasoning" | "tool-activity";
  readonly status: ConversationProcessStatusV1;
  /** Reasoning text only. Tool arguments and results are intentionally excluded. */
  readonly text?: string;
  readonly toolName?: string;
  readonly displayLabel?: string;
  readonly capability?: string;
  readonly toolKind?: string;
  readonly error?: string;
}

/** Public data for the complete reasoning/tool process region of one turn. */
export interface ConversationProcessSnapshotV1 {
  readonly contractVersion: 1;
  readonly kind: ConversationProcessKindV1;
  readonly status: ConversationProcessStatusV1;
  readonly streaming: boolean;
  readonly active: boolean;
  /** Items preserve host display order and never contain private thread records. */
  readonly items: readonly ConversationProcessItemV1[];
}

export interface ComposerQueueSummaryV1 {
  readonly id: string;
  readonly text?: string;
  readonly attachmentCount?: number;
  readonly status?: "queued" | "pending";
}

export interface ModelSummaryV1 {
  readonly id: string;
  readonly label: string;
  readonly providerId?: string;
  readonly contextWindowTokens?: number;
}

export interface RuntimeSummaryV1 {
  readonly id: string;
  readonly label: string;
  readonly status?: "available" | "unavailable" | "starting" | "running" | "error";
}

export interface PermissionSummaryV1 {
  readonly id: string;
  readonly label: string;
  readonly description?: string;
}

export interface ContextUsageSummaryV1 {
  readonly usedTokens?: number;
  readonly limitTokens?: number;
  readonly percent?: number;
}

export type ComposerSubmissionModeV1 = "send" | "queue" | "steer";

export interface ComposerSnapshotV1 {
  readonly contractVersion: 1;
  readonly draftText?: string;
  readonly attachments?: readonly AttachmentDescriptorV1[];
  readonly queued?: readonly ComposerQueueSummaryV1[];
  readonly pending?: readonly ComposerQueueSummaryV1[];
  readonly running?: boolean;
  readonly readOnly?: boolean;
  readonly threadId?: string;
  readonly variant?: string;
  readonly availableSubmissionModes?: readonly ComposerSubmissionModeV1[];
  readonly activeSubmissionMode?: ComposerSubmissionModeV1;
  readonly model?: ModelSummaryV1;
  readonly runtime?: RuntimeSummaryV1;
  readonly permission?: PermissionSummaryV1;
  readonly contextUsage?: ContextUsageSummaryV1;
  readonly disabledReason?: string;
}

export interface HeaderTabDescriptorV1 {
  readonly id: string;
  readonly title: string;
  readonly subtitle?: string;
  readonly kind?: string;
  readonly busy?: boolean;
  readonly dirty?: boolean;
  readonly disabled?: boolean;
}

export interface HeaderSnapshotV1 {
  readonly contractVersion: 1;
  readonly scope: "conversation" | "workspace";
  readonly title?: string;
  readonly subtitle?: string;
  readonly tabs?: readonly HeaderTabDescriptorV1[];
  readonly activeTabId?: string;
  readonly busy?: boolean;
  readonly dirty?: boolean;
  readonly canNavigateBack?: boolean;
  readonly canNavigateForward?: boolean;
}

export interface NavigationNodeV1 {
  readonly id: string;
  readonly kind: "section" | "project" | "thread" | "room" | "command";
  readonly label: string;
  readonly parentId?: string;
  readonly depth?: number;
  readonly description?: string;
  readonly icon?: string;
  readonly active?: boolean;
  readonly pinned?: boolean;
  readonly unread?: boolean;
  readonly running?: boolean;
  readonly disabled?: boolean;
}

export interface NavigationSnapshotV1 {
  readonly contractVersion: 1;
  /** Nodes are in host display order; parentId and depth describe hierarchy. */
  readonly nodes: readonly NavigationNodeV1[];
  readonly activeNodeId?: string;
}

export interface StatusItemV1 {
  readonly id: string;
  readonly label: string;
  readonly kind?: "info" | "progress" | "success" | "warning" | "error";
  readonly detail?: string;
  readonly icon?: string;
  readonly progress?: number;
  readonly busy?: boolean;
  readonly disabled?: boolean;
  readonly actionId?: string;
}

export interface StatusSnapshotV1 {
  readonly contractVersion: 1;
  readonly items: readonly StatusItemV1[];
}

export interface FileSelectionDescriptorV1 {
  readonly startOffset?: number;
  readonly endOffset?: number;
  readonly startLine?: number;
  readonly startColumn?: number;
  readonly endLine?: number;
  readonly endColumn?: number;
}

/** File data safe for presentation; it never contains filesystem or window handles. */
export interface FilePreviewSnapshotV1 {
  readonly contractVersion: 1;
  readonly resourceId: string;
  readonly workspaceRelativePath: string;
  readonly contentType?: string;
  readonly text?: string;
  readonly safeHostUrl?: string;
  readonly sizeBytes?: number;
  readonly binary?: boolean;
  readonly readOnly?: boolean;
  readonly dirty?: boolean;
  readonly loading?: boolean;
  readonly error?: string;
  readonly selection?: FileSelectionDescriptorV1;
}

export interface SettingsPageSummaryV1 {
  readonly id: string;
  readonly label: string;
  readonly description?: string;
  readonly disabled?: boolean;
}

export interface SettingsPluginSummaryV1 {
  readonly id: string;
  readonly name: string;
  readonly version?: string;
  readonly enabled?: boolean;
  readonly status?: string;
}

export interface SettingsRuntimeSummaryV1 {
  readonly id: string;
  readonly label: string;
  readonly version?: string;
  readonly status?: string;
}

export interface SettingsProviderSummaryV1 {
  readonly id: string;
  readonly label: string;
  readonly configured?: boolean;
  readonly status?: string;
}

/** Sanitized settings navigation and service summaries; provider credentials are excluded. */
export interface SettingsSnapshotV1 {
  readonly contractVersion: 1;
  readonly activePageId?: string;
  readonly availablePages?: readonly SettingsPageSummaryV1[];
  readonly plugins?: readonly SettingsPluginSummaryV1[];
  readonly runtimes?: readonly SettingsRuntimeSummaryV1[];
  readonly providers?: readonly SettingsProviderSummaryV1[];
  readonly busy?: boolean;
  readonly error?: string;
}

export const CONVERSATION_ITEM_ACTIONS = {
  copy: "conversation.item.copy",
  edit: "conversation.item.edit",
  retry: "conversation.item.retry",
  openAttachment: "conversation.item.open-attachment",
  openTool: "conversation.item.open-tool",
  cancelProcess: "conversation.item.cancel-process",
} as const;
export type ConversationItemActionId = (typeof CONVERSATION_ITEM_ACTIONS)[keyof typeof CONVERSATION_ITEM_ACTIONS];

export const COMPOSER_ACTIONS = {
  setDraft: "conversation.composer.set-draft",
  addAttachment: "conversation.composer.add-attachment",
  removeAttachment: "conversation.composer.remove-attachment",
  setSubmissionMode: "conversation.composer.set-submission-mode",
  submit: "conversation.composer.submit",
  stop: "conversation.composer.stop",
} as const;
export type ComposerActionId = (typeof COMPOSER_ACTIONS)[keyof typeof COMPOSER_ACTIONS];

export const HEADER_ACTIONS = {
  selectTab: "header.select-tab",
  closeTab: "header.close-tab",
  navigateBack: "header.navigate-back",
  navigateForward: "header.navigate-forward",
} as const;
export type HeaderActionId = (typeof HEADER_ACTIONS)[keyof typeof HEADER_ACTIONS];

export const NAVIGATION_ACTIONS = {
  activateNode: "navigation.activate-node",
  pinNode: "navigation.pin-node",
  unpinNode: "navigation.unpin-node",
} as const;
export type NavigationActionId = (typeof NAVIGATION_ACTIONS)[keyof typeof NAVIGATION_ACTIONS];

export const STATUS_ACTIONS = { activateItem: "status.activate-item" } as const;
export type StatusActionId = (typeof STATUS_ACTIONS)[keyof typeof STATUS_ACTIONS];

export const FILE_PREVIEW_ACTIONS = {
  open: "file-preview.open",
  reveal: "file-preview.reveal",
  select: "file-preview.select",
  save: "file-preview.save",
  reload: "file-preview.reload",
} as const;
export type FilePreviewActionId = (typeof FILE_PREVIEW_ACTIONS)[keyof typeof FILE_PREVIEW_ACTIONS];

export const SETTINGS_ACTIONS = {
  openPage: "settings.open-page",
  updateValue: "settings.update-value",
  refresh: "settings.refresh",
} as const;
export type SettingsActionId = (typeof SETTINGS_ACTIONS)[keyof typeof SETTINGS_ACTIONS];

export interface PresentationHost extends ViewHostAPI {
  readonly actions: readonly string[];
  invoke(action: string, input?: unknown): Promise<unknown>;
}

export interface PresenterProps {
  readonly contractVersion: 1;
  readonly target: PresentationTarget;
  readonly key?: string;
  readonly snapshot: unknown;
  readonly host: PresentationHost;
  readonly fallback: React.ReactNode;
}

export interface PresenterDefinition {
  readonly id: string;
  readonly target: PresentationTarget;
  readonly key?: string;
  readonly mode?: PresentationMode;
  readonly priority?: number;
  readonly render: (props: PresenterProps) => React.ReactNode;
}

export type ToolActivityStatus = "running" | "completed" | "failed";

export interface ToolActivityResultContentPart {
  readonly type: string;
  readonly text?: string;
  readonly data?: string;
  readonly mimeType?: string;
  readonly uri?: string;
  readonly name?: string;
  readonly resource?: unknown;
  readonly artifact?: Readonly<{
    placement?: "inline" | "turn_end";
    ref?: string;
    sha256?: string;
    sizeBytes?: number;
  }>;
}

export interface ToolActivityStructuredResult {
  readonly content?: readonly ToolActivityResultContentPart[];
  readonly structuredContent?: unknown;
  readonly metadata?: unknown;
  readonly isError?: boolean;
  readonly activity?: Readonly<{
    id: string;
    kind: string;
    state?: string;
    threadId?: string;
    previewUri?: string;
  }>;
}

export interface ToolActivitySnapshot {
  readonly contractVersion: 1;
  readonly id: string;
  readonly toolName: string;
  readonly displayLabel?: string;
  readonly capability?: string;
  readonly kind?: string;
  readonly status: ToolActivityStatus;
  readonly argumentsText?: string;
  readonly resultText?: string;
  readonly structuredResult?: ToolActivityStructuredResult;
  readonly error?: string;
}

export interface ToolActivityPresenterProps {
  readonly activity: ToolActivitySnapshot;
  readonly host: ViewHostAPI;
  readonly fallback: React.ReactNode;
}

export interface ToolActivityPresenterDefinition {
  readonly id: string;
  readonly key: string;
  readonly render: React.ComponentType<ToolActivityPresenterProps>;
}

/** Current on-disk workbench state schema. */
export const WORKBENCH_LAYOUT_STATE_VERSION = 2 as const;

/** A host-owned view instance. Plugins never receive the mutable instance. */
export interface WorkbenchViewState {
  id: string;
  pluginId: string;
  generation: string;
  viewTypeId: ViewTypeId;
  region: ViewPlacementRegion;
  persistence: ViewPersistence;
  context: Readonly<Record<string, unknown>>;
  sourcePlacementId?: string;
}

/** Versioned, shell-independent state persisted by the desktop workbench. */
export interface WorkbenchLayoutState {
  version: typeof WORKBENCH_LAYOUT_STATE_VERSION;
  views: readonly WorkbenchViewState[];
  activeViewByRegion: Readonly<Partial<Record<ViewPlacementRegion, string>>>;
  dismissedPlacementIds: readonly string[];
}

// ---------------------------------------------------------------------------
// View placement
// ---------------------------------------------------------------------------

/**
 * Requests that one registered View be opened in a stable host-owned region.
 * This does not grant control over the shell's DOM, split tree, dimensions,
 * protected chrome, or recovery UI. User dismissal and activation win over
 * plugin defaults.
 */
export interface ViewPlacementContribution {
  /** Stable placement identifier. */
  id: string;
  /** View type registered by the same plugin. */
  view: ViewTypeId;
  /** Host-owned region where the View should initially appear. */
  region: ViewPlacementRegion;
  /** Higher priority becomes the initial active View when a region is empty. */
  priority?: number;
}

// ---------------------------------------------------------------------------
// Renderer
// ---------------------------------------------------------------------------

/** Categories of content that plugins can provide custom renderers for. */
export type RendererCategory =
  | "message"
  | "tool-result"
  | "document"
  | "file-preview";

/** Plugin-registered content renderer. */
export interface RendererDefinition {
  /** Stable identifier. */
  id: string;
  /** Category this renderer handles. */
  category: RendererCategory;
  /** MIME type or content-type pattern this renderer matches. */
  match: string | RegExp;
  /** Priority — higher values override lower-priority renderers. */
  priority?: number;
  /** React component that renders the content. */
  render: React.ComponentType<RendererProps>;
}

/** Props passed to custom renderer components. */
export interface RendererProps {
  /** The raw content to render. Structure depends on category. */
  content: unknown;
  /** Additional metadata from the host. */
  metadata: Readonly<Record<string, unknown>>;
  /** Host API for controlled interactions. */
  host: ViewHostAPI;
}

// ---------------------------------------------------------------------------
// Theme tokens
// ---------------------------------------------------------------------------

/**
 * Complete design-language token categories. Every token is a CSS custom
 * property that the host defines and theme plugins override.
 *
 * Tokens are organized by category for the Phase C contract. The host
 * owns the CSS variable namespace; plugins contribute overrides per
 * theme (light/dark).
 */
export type ThemeTokenCategory =
  | "color"
  | "typography"
  | "spacing"
  | "density"
  | "radius"
  | "border"
  | "elevation"
  | "motion"
  | "syntax"
  | "content";

/**
 * Theme token contribution from a plugin. Tokens are CSS custom property
 * key-value pairs. The host applies them in priority order on top of the
 * built-in theme.
 */
export interface ThemeTokens {
  /** Theme identifier this contribution targets (e.g. "dark", "light"). */
  theme: string;
  /** Base theme to inherit from ("light" or "dark"). */
  base: string;
  /** CSS custom property overrides: "--wuu-color-bg": "#1a1a2e". */
  tokens: Readonly<Partial<Record<PublicThemeTokenName, string>>>;
  /** Syntax highlighting overrides. */
  syntax?: Readonly<Partial<Record<PublicSyntaxTokenName, string>>>;
}

/**
 * CSS snippet contributed by a plugin. Snippets are injected into the shared
 * document without selector rewriting. They should use CSS custom properties
 * for theming and stable data attributes for targeting host elements.
 *
 * Snippets are a high-trust feature: they run in the same document as the
 * host UI. Plugins must not depend on internal React state or private
 * class names, which are not part of the compatibility contract.
 */
export interface CSSSnippet {
  /** Stable snippet identifier. */
  id: string;
  /** Raw high-trust CSS. The host does not rewrite or isolate selectors. */
  css: string;
  /** Priority — higher values are injected later (higher specificity). */
  priority?: number;
}

// ---------------------------------------------------------------------------
// Settings & storage
// ---------------------------------------------------------------------------

/**
 * Plugin setting definition. Mirrors the Go-side SettingDefinition in the
 * plugin manifest but typed for the TypeScript workbench API.
 */
export interface PluginSettingDefinition {
  type: "boolean" | "string" | "number" | "enum";
  title: string;
  description?: string;
  default: unknown;
  enum?: string[];
  scope: "user" | "workspace";
  apply: "live" | "restart";
}

/**
 * Namespaced storage API. Plugins read and write key-value pairs scoped
 * to their plugin ID. Storage is durable and survives restarts.
 */
export interface PluginStorageAPI {
  get(key: string): Promise<string | null>;
  set(key: string, value: string): Promise<void>;
  delete(key: string): Promise<void>;
  keys(): Promise<string[]>;
}

/**
 * Plugin settings API. Plugins read their own setting values as defined
 * in the manifest settings section.
 */
export interface PluginSettingsAPI {
  get(key: string): Promise<unknown>;
  getAll(): Promise<Record<string, unknown>>;
}

// ---------------------------------------------------------------------------
// Commands (extended)
// ---------------------------------------------------------------------------

/**
 * Extended command registration with keyboard shortcut and status item
 * support. Complements the existing PluginCommandRegistration.
 */
export interface CommandDefinition {
  id: string;
  title: string;
  description?: string;
  /** Keyboard shortcut, e.g. "Ctrl+Shift+P". */
  shortcut?: string;
  /** Target context where this command is available. */
  contexts?: string[];
  /** Execute the command. */
  execute(input?: unknown): unknown | Promise<unknown>;
}

/**
 * Status bar item contributed by a plugin.
 */
export interface StatusItemDefinition {
  id: string;
  /** Text label shown in the status bar. */
  label: string;
  /** Optional icon. */
  icon?: string;
  /** Tooltip text. */
  tooltip?: string;
  /** Command to execute on click. */
  command?: string;
  /** Priority — higher values appear further right. */
  priority?: number;
}

// ---------------------------------------------------------------------------
// Locale (extended)
// ---------------------------------------------------------------------------

/**
 * Locale contribution with fallback chain support.
 */
export interface LocaleDefinition {
  /** BCP 47 locale tag, e.g. "en", "zh-CN". */
  locale: string;
  /** Fallback locale chain, e.g. ["en"]. */
  fallback?: string[];
  /** Translation entries: key → translated string. */
  entries: Record<string, string>;
}
