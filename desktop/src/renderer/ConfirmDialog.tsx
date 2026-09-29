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
};

type PendingConfirm = ConfirmRequest & {
  id: number;
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

export function confirmAction(request: ConfirmRequest): Promise<boolean> {
  return new Promise((resolve) => {
    queue = [...queue, { ...request, id: nextID++, resolve }];
    for (const listener of listeners) listener();
  });
}

function settle(id: number, confirmed: boolean): void {
  const entry = queue.find((candidate) => candidate.id === id);
  if (!entry) return;
  queue = queue.filter((candidate) => candidate.id !== id);
  for (const listener of listeners) listener();
  entry.resolve(confirmed);
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
            onClick={cancel}
          >
            {t("common.cancel")}
          </button>
          <button
            type="button"
            className={`settings-button ${request.tone === "danger" ? "settings-button-danger" : "settings-button-primary"}`}
            data-confirm-action="confirm"
            // Enter answers the question the user just asked for; Escape,
            // the backdrop, and Cancel decline it.
            autoFocus
            onClick={() => settle(request.id, true)}
          >
            {request.confirmLabel}
          </button>
        </>
      )}
    >
      <p className="confirm-dialog-message">{request.message}</p>
    </Modal>
  );
}
