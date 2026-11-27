import { useContext, useEffect, useId, useRef, useState } from "react";
import type { ResponseSelection } from "../shared/protocol";
import { FloatingMenuPortal, composerMenuWidth } from "./ComposerFloatingMenu";
import { isComposerTextComposing } from "./ComposerSlashCommands";
import { Tooltip } from "./Tooltip";
import { navigateToResponseSelection, ResponseSelectionHistoryContext } from "./ResponseSelection";
import { useI18n } from "./i18n";
import { Pencil, Quote, Trash2 } from "./WuuIcons";
import "./ComposerResponseSelectionCard.css";
import "./ResponseSelectionList.css";

export function ResponseSelectionList({ selections, history = false, onChange, onRemove }: {
  selections: readonly ResponseSelection[];
  history?: boolean;
  onChange?: (selection: ResponseSelection) => void;
  onRemove?: (id: string) => void;
}): JSX.Element | null {
  const { t } = useI18n();
  const selectionHistory = useContext(ResponseSelectionHistoryContext);
  const panelId = useId();
  const anchor = useRef<HTMLButtonElement>(null);
  const panel = useRef<HTMLDivElement>(null);
  const editor = useRef<HTMLTextAreaElement>(null);
  const navigation = useRef<AbortController | undefined>(undefined);
  const [open, setOpen] = useState(false);
  const [editingId, setEditingId] = useState<string>();
  const label = t(selections.length === 1 ? "responseSelection.countOne" : "responseSelection.count", { count: selections.length });

  function close(restoreFocus = false): void {
    navigation.current?.abort();
    setOpen(false);
    setEditingId(undefined);
    if (restoreFocus) anchor.current?.focus({ preventScroll: true });
  }

  useEffect(() => () => navigation.current?.abort(), []);
  useEffect(() => {
    if (!open) return;
    const frame = requestAnimationFrame(() => {
      (editor.current ?? panel.current?.querySelector<HTMLElement>(".response-selection-list-source"))?.focus({ preventScroll: true });
    });
    return () => cancelAnimationFrame(frame);
  }, [open, editingId]);

  if (!selections.length) return null;
  return <div className={`response-selection-list${history ? " response-selection-list--history" : ""}`}>
    <button ref={anchor} type="button" className="response-selection-list-trigger"
      aria-expanded={open} aria-haspopup="dialog" aria-controls={open ? panelId : undefined}
      onClick={() => { if (open) close(true); else setOpen(true); }}>
      <Quote className="icon" aria-hidden="true" />
      {label}
    </button>
    {open ? <FloatingMenuPortal anchorRef={anchor} owner="composer-attach"
      placement="above" align={history ? "right" : "left"} width={composerMenuWidth(420)} flip={!history}
      boundarySelector=".user-message-edit, .composer-stack, .conversation-width, .side-thread-panel, .conversation-pane"
      mobileSheet={{ label, onClose: () => close(true) }} onDismiss={() => close()}>
      <div ref={panel} id={panelId} className={`response-selection-list-panel${history ? " response-selection-list-panel--history" : ""}`} role="dialog" tabIndex={-1} aria-label={label}
        onKeyDown={event => {
          event.stopPropagation();
          if (event.key === "Escape" && !isComposerTextComposing(event)) {
            event.preventDefault();
            close(true);
          }
        }}>
        <ol role={history ? "list" : undefined}>
          {selections.map(selection => <li key={selection.id}>
            <div className="response-selection-list-row">
              <button type="button" className="response-selection-list-source"
                onClick={async () => {
                  navigation.current?.abort();
                  const request = new AbortController();
                  navigation.current = request;
                  const found = await navigateToResponseSelection(selection, selectionHistory, request.signal);
                  if (navigation.current !== request || request.signal.aborted) return;
                  if (found) close();
                }}>
                <span className="sr-only">{t("responseSelection.source")}</span>
                <span className="response-selection-list-field">
                  {history ? <span className="response-selection-list-label">{t("responseSelection.selectedText")}</span> : null}
                  <span className="composer-response-selection-quote">{selection.text}</span>
                </span>
                {editingId !== selection.id && selection.comment ? <span className="response-selection-list-field">
                  {history ? <span className="response-selection-list-label">{t("responseSelection.commentLabel")}</span> : null}
                  <span className="response-selection-list-comment">{selection.comment}</span>
                </span> : null}
              </button>
              {onChange || onRemove ? <div className="response-selection-list-actions">
                {onChange ? <Tooltip asChild content={t("responseSelection.editComment")}><button type="button"
                  className="response-selection-list-edit" aria-label={t("responseSelection.editComment")}
                  aria-expanded={editingId === selection.id}
                  onClick={() => setEditingId(id => id === selection.id ? undefined : selection.id)}>
                  <Pencil className="icon" aria-hidden="true" />
                </button></Tooltip> : null}
                {onRemove ? <Tooltip asChild content={t("responseSelection.remove")}><button type="button"
                  className="composer-response-selection-remove" aria-label={t("responseSelection.remove")}
                  onClick={event => {
                    if (selections.length === 1) close();
                    else {
                      if (editingId === selection.id) setEditingId(undefined);
                      const row = event.currentTarget.closest("li");
                      const next = row?.nextElementSibling ?? row?.previousElementSibling;
                      next?.querySelector<HTMLElement>(".response-selection-list-source")?.focus({ preventScroll: true });
                    }
                    onRemove(selection.id);
                  }}>
                  <Trash2 className="icon" aria-hidden="true" />
                </button></Tooltip> : null}
              </div> : null}
              {onChange && editingId === selection.id ? <textarea ref={editor} className="composer-response-selection-comment"
                rows={1} wrap="soft" value={selection.comment ?? ""}
                aria-label={t("responseSelection.optionalComment")} placeholder={t("responseSelection.optionalComment")}
                onChange={event => onChange({ ...selection, comment: event.target.value })} /> : null}
            </div>
          </li>)}
        </ol>
      </div>
    </FloatingMenuPortal> : null}
  </div>;
}
