import type { ComposerConfiguration } from "../ComposerTypes";
import type { ReactNode, RefObject } from "react";
import type { InitializeResult } from "../../shared/protocol";
import { COMPOSER_ACTIONS, type AttachmentDescriptorV1, type ComposerSnapshotV1, type ComposerSubmissionModeV1 } from "../../shared/workbench";
import type { TurnContextUsage } from "../AppState";
import type { ComposerFile, ComposerImage, QueuedComposerMessage } from "../ComposerMessages";
import { permissionModeFromSummary, permissionModeOption } from "../ComposerRuntimeMenus";
import type { ComposerVariant } from "../ComposerTypes";
import { desktopPluginHost, desktopWorkbenchController } from "./DesktopPluginRuntime";
import { PluginPresentation } from "./PluginPresentation";
import type { PluginHost } from "./PluginHost";
import type { WorkbenchController } from "./Workbench";

interface ComposerPresentationProps {
  enabled: boolean; fallback: ReactNode; draftText: string;
  files: readonly ComposerFile[]; images: readonly ComposerImage[];
  queuedMessages: readonly QueuedComposerMessage[]; pendingMessages: readonly QueuedComposerMessage[];
  running: boolean; readOnly: boolean; sendDisabled: boolean; variant: ComposerVariant;
  threadId?: string; initialized?: ComposerConfiguration | InitializeResult; contextUsage?: TurnContextUsage | null;
  disabledReason?: string;
  activeSubmissionMode: ComposerSubmissionModeV1; availableSubmissionModes: readonly ComposerSubmissionModeV1[];
  attachmentInputRef: RefObject<HTMLInputElement | null>; attachmentsEnabled: boolean;
  onSetDraft: (value: string) => void; onRemoveFile: (id: string) => void; onRemoveImage: (id: string) => void;
  onSubmit: () => void; onStop: () => void; host?: PluginHost; controller?: WorkbenchController;
}

export function ComposerPresentation(props: ComposerPresentationProps): JSX.Element {
  const { host = desktopPluginHost, controller = desktopWorkbenchController } = props;
  const actions: string[] = [COMPOSER_ACTIONS.setDraft, COMPOSER_ACTIONS.submit];
  if (props.attachmentsEnabled) actions.push(COMPOSER_ACTIONS.addAttachment, COMPOSER_ACTIONS.removeAttachment);
  if (props.running) actions.push(COMPOSER_ACTIONS.stop);

  function dispatchAction(action: string, input?: unknown): void {
    switch (action) {
      case COMPOSER_ACTIONS.setDraft:
        requireWritable();
        if (typeof input !== "string") throw new Error("Composer draft must be a string");
        props.onSetDraft(input); return;
      case COMPOSER_ACTIONS.addAttachment:
        requireWritable(); requireNoInput(input);
        if (!props.attachmentsEnabled) throw new Error("Composer attachments are unavailable");
        props.attachmentInputRef.current?.click(); return;
      case COMPOSER_ACTIONS.removeAttachment:
        requireWritable();
        if (typeof input !== "string" || input.length === 0) throw new Error("Attachment id must be a non-empty string");
        if (props.files.some((file) => file.id === input)) return props.onRemoveFile(input);
        if (props.images.some((image) => image.id === input)) return props.onRemoveImage(input);
        throw new Error("Attachment does not exist");
      case COMPOSER_ACTIONS.submit:
        requireWritable(); requireNoInput(input);
        if (props.sendDisabled || (props.draftText.trim().length === 0 && props.files.length === 0 && props.images.length === 0)) {
          throw new Error("Composer submission is disabled");
        }
        props.onSubmit(); return;
      case COMPOSER_ACTIONS.stop:
        requireNoInput(input);
        if (!props.running) throw new Error("Composer is not running");
        props.onStop(); return;
      default: throw new Error(`Unsupported composer action: ${action}`);
    }
  }
  function requireWritable(): void { if (props.readOnly) throw new Error("Composer is read-only"); }

  return <PluginPresentation enabled={props.enabled} host={host} controller={controller}
    target="conversation.composer" snapshot={buildComposerSnapshot(props)} fallback={props.fallback}
    actions={Object.freeze(actions)} dispatchAction={dispatchAction} />;
}

type SnapshotInput = Pick<ComposerPresentationProps, "draftText" | "files" | "images" | "queuedMessages" |
  "pendingMessages" | "running" | "readOnly" | "variant" | "threadId" | "initialized" |
  "contextUsage" | "disabledReason" | "activeSubmissionMode" | "availableSubmissionModes">;

export function buildComposerSnapshot(input: SnapshotInput): ComposerSnapshotV1 {
  const permissionMode = permissionModeFromSummary(input.initialized?.permissions);
  const permission = input.initialized?.permissions === undefined ? undefined : permissionModeOption(permissionMode);
  const runtimeHost = input.initialized?.runtime_host;
  const contextWindowTokens = input.contextUsage?.window ?? input.initialized?.advanced_settings?.context_window_tokens;
  return Object.freeze({
    contractVersion: 1, draftText: input.draftText,
    attachments: Object.freeze([
      ...input.files.map((file) => attachmentDescriptor(file.id, file.filename ?? file.media_type, file.media_type)),
      ...input.images.map((image) => attachmentDescriptor(image.id, image.media_type, image.media_type)),
    ]),
    queued: queueSummaries(input.queuedMessages, "queued"), pending: queueSummaries(input.pendingMessages, "pending"),
    running: input.running, readOnly: input.readOnly, variant: input.variant,
    ...(input.threadId === undefined ? {} : { threadId: input.threadId }),
    availableSubmissionModes: Object.freeze([...input.availableSubmissionModes]), activeSubmissionMode: input.activeSubmissionMode,
    ...(input.initialized === undefined ? {} : { model: Object.freeze({ id: input.initialized.model, label: input.initialized.model,
      providerId: input.initialized.provider, ...(contextWindowTokens === undefined ? {} : { contextWindowTokens }) }) }),
    ...(runtimeHost === undefined ? {} : { runtime: Object.freeze({ id: runtimeHost.instance_id ?? runtimeHost.kind,
      label: runtimeHost.kind, status: input.running ? "running" as const : "available" as const }) }),
    ...(permission === undefined ? {} : { permission: Object.freeze({ id: permissionMode, label: permission.label }) }),
    ...(input.contextUsage == null ? {} : { contextUsage: Object.freeze({ usedTokens: input.contextUsage.used,
      limitTokens: input.contextUsage.window,
      ...(input.contextUsage.window > 0 ? { percent: input.contextUsage.used / input.contextUsage.window * 100 } : {}) }) }),
    ...(input.disabledReason === undefined ? {} : { disabledReason: input.disabledReason }),
  });
}

function attachmentDescriptor(id: string, name: string, mimeType: string): AttachmentDescriptorV1 {
  return Object.freeze({ id, name, mimeType });
}
function queueSummaries(messages: readonly QueuedComposerMessage[], status: "queued" | "pending") {
  return Object.freeze(messages.map((message) => Object.freeze({ id: message.id, text: message.text,
    attachmentCount: message.files.length + message.images.length, status })));
}
function requireNoInput(input: unknown): void { if (input !== undefined) throw new Error("Composer action does not accept input"); }
