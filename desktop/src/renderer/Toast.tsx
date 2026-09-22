import { CircleAlert } from "./WuuIcons";
import { useSyncExternalStore } from "react";
import { useI18n } from "./i18n";
import { TopNotice, type TopNoticeAction } from "./TopNotice";
import { UILayerPortal } from "./ui/layers/UILayerHost";

const TOAST_DEDUPE_WINDOW_MS = 30_000;
const MAX_QUEUED_TOASTS = 4;

export type ToastTone = "info" | "error";

export type ToastInput = {
  message: string;
  tone?: ToastTone;
  dedupeKey?: string;
  /** Optional action button rendered inside the toast (e.g. "go configure"). */
  action?: TopNoticeAction;
};

type ToastRecord = Required<Pick<ToastInput, "message" | "tone">> & {
  id: number;
  dedupeKey: string;
  action?: TopNoticeAction;
};

let nextToastID = 1;
let activeToast: ToastRecord | null = null;
let queuedToasts: ToastRecord[] = [];
let snapshot: ToastRecord | null = null;
const listeners = new Set<() => void>();
const recentlyShownAt = new Map<string, number>();

function emit(): void {
  snapshot = activeToast;
  for (const listener of listeners) listener();
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

function getSnapshot(): ToastRecord | null {
  return snapshot;
}

function activateNextToast(): void {
  activeToast = queuedToasts.shift() ?? null;
  emit();
}

export function dismissToast(id: number): void {
  if (activeToast?.id === id) {
    activateNextToast();
    return;
  }
  queuedToasts = queuedToasts.filter((toast) => toast.id !== id);
}

export function showToast({
  message,
  tone = "info",
  dedupeKey,
  action,
}: ToastInput): number | undefined {
  const trimmedMessage = message.trim();
  if (!trimmedMessage) return undefined;

  const key = dedupeKey?.trim() || `${tone}:${trimmedMessage}`;
  const now = Date.now();
  const lastShownAt = recentlyShownAt.get(key);
  if (lastShownAt !== undefined && now - lastShownAt < TOAST_DEDUPE_WINDOW_MS) {
    return activeToast?.dedupeKey === key ? activeToast.id : undefined;
  }
  recentlyShownAt.set(key, now);

  const toast: ToastRecord = {
    id: nextToastID++,
    message: trimmedMessage,
    tone,
    dedupeKey: key,
    action,
  };
  if (activeToast === null) {
    activeToast = toast;
    emit();
  } else {
    queuedToasts = [...queuedToasts.slice(-(MAX_QUEUED_TOASTS - 1)), toast];
  }
  return toast.id;
}

export function toastErrorMessage(error: unknown, fallback = ""): string {
  let message = error instanceof Error
    ? error.message
    : typeof error === "string"
      ? error
      : fallback;
  message = message.trim();

  const remoteMethodPrefix = /^Error invoking remote method ['"][^'"]+['"]:\s*/i;
  while (/^Error:\s*/i.test(message)) {
    message = message.replace(/^Error:\s*/i, "");
  }
  if (remoteMethodPrefix.test(message)) {
    message = message.replace(remoteMethodPrefix, "");
  }
  while (/^Error:\s*/i.test(message)) {
    message = message.replace(/^Error:\s*/i, "");
  }
  return message.trim() || fallback;
}

export function showErrorToast(error: unknown, fallback = "", dedupeKey?: string): number | undefined {
  return showToast({
    message: toastErrorMessage(error, fallback),
    tone: "error",
    dedupeKey,
  });
}

export function clearToasts(): void {
  activeToast = null;
  queuedToasts = [];
  recentlyShownAt.clear();
  emit();
}

export function ToastViewport(): JSX.Element | null {
  const { t } = useI18n();
  const toast = useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
  if (!toast) return null;

  return (
    <UILayerPortal layer="notice">
      <TopNotice
        key={toast.id}
        message={toast.message}
        icon={toast.tone === "error" ? CircleAlert : undefined}
        isError={toast.tone === "error"}
        action={toast.action}
        dismissAriaLabel={t("common.closeNotice")}
        onDismiss={() => dismissToast(toast.id)}
      />
    </UILayerPortal>
  );
}
