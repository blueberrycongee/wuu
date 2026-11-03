import { useSyncExternalStore } from "react";
import { useI18n } from "./i18n";
import { Modal } from "./Modal";

/**
 * In-app confirmation for irreversible or disruptive actions, replacing
 * `window.confirm`, whose native sheet cannot follow the product's type,
 * theme, or copy. Callers await the answer the way they awaited the native
 * prompt; `ConfirmDialogHost` (mounted once beside the toast viewport)
 * renders one request at a time and answers later ones in order. The shared
 * Modal returns focus to the control that asked, when it is still on the page.
 */
export type ConfirmRequest = {
  /** The question, naming the object: “删除“X”？”. */
  title: string;
  /** One sentence of consequence the title does not already state. */
  message: string;
  /** The action's verb, repeated from the title. */
  confirmLabel: string;
  /** Danger marks actions that destroy data; the default is a plain primary. */
  tone?: "danger";
  /** Open immediately but gate confirmation on fresh data. Null dismisses; errors reject the action. */
  prepare?: () => Promise<Omit<ConfirmRequest, "prepare"> | null>;
};

type PendingConfirm = ConfirmRequest & {
  id: number;
  preparing: boolean;
  reject: (error: unknown) => void;
  resolve: (confirmed: boolean) => void;
};

let nextID = 1;
let queue: readonly PendingConfirm[] = [];
const listeners = new Set<() => void>();

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

function currentRequest(): PendingConfirm | null {
  return queue[0] ?? null;
}

export function confirmAction(request: ConfirmRequest, signal?: AbortSignal): Promise<boolean> {
  if (signal?.aborted) return Promise.resolve(false);
  return new Promise((resolve, reject) => {
    const id = nextID++;
    const cancel = () => settle(id, false);
    const finish = (confirmed: boolean) => { signal?.removeEventListener("abort", cancel); resolve(confirmed); };
    queue = [...queue, { ...request, id, preparing: !!request.prepare, resolve: finish,
      reject: error => { signal?.removeEventListener("abort", cancel); reject(error); } }];
    signal?.addEventListener("abort", cancel, { once: true });
    for (const listener of listeners) listener();
    if (request.prepare) {
      void Promise.resolve().then(request.prepare).then(prepared => {
        if (!queue.some(entry => entry.id === id)) return;
        if (!prepared) { settle(id, false); return; }
        queue = queue.map(entry => entry.id === id ? { ...entry, ...prepared, preparing: false } : entry);
        for (const listener of listeners) listener();
      }, error => settle(id, false, { error }));
    }
  });
}

function settle(id: number, confirmed: boolean, failure?: { error: unknown }): void {
  const entry = queue.find((candidate) => candidate.id === id);
  if (!entry || (confirmed && entry.preparing)) return;
  queue = queue.filter((candidate) => candidate.id !== id);
  for (const listener of listeners) listener();
  if (failure) entry.reject(failure.error);
  else entry.resolve(confirmed);
}

export function ConfirmDialogHost(): JSX.Element | null {
  const { t } = useI18n();
  const request = useSyncExternalStore(subscribe, currentRequest, currentRequest);
  if (!request) return null;
  const cancel = (): void => settle(request.id, false);
  return (
    <Modal
      key={request.id}
      ariaLabel={request.title}
      title={request.title}
      onClose={cancel}
      showCloseButton={false}
      initialFocus="none"
      panelClassName="confirm-dialog"
      footer={(
        <>
          <button
            type="button"
            className="settings-button settings-button-ghost"
            data-confirm-action="cancel"
            autoFocus={request.preparing}
            onClick={cancel}
          >
            {t("common.cancel")}
          </button>
          <button
            type="button"
            className={`settings-button ${request.tone === "danger" ? "settings-button-danger" : "settings-button-primary"}`}
            data-confirm-action="confirm"
            // Ordinary prompts focus Confirm. Preparation starts on Cancel
            // and does not move focus when the authoritative data arrives.
            autoFocus={!request.preparing}
            disabled={request.preparing}
            onClick={() => settle(request.id, true)}
          >
            {request.confirmLabel}
          </button>
        </>
      )}
    >
      <p className="confirm-dialog-message" aria-busy={request.preparing || undefined}>{request.message}</p>
    </Modal>
  );
}
