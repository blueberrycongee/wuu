import { MessageSquare, Pencil, Trash2 } from "lucide-react";
import { useEffect, useId, useRef, useState } from "react";
import type { FileSelectionSource, ResponseSelection } from "../shared/protocol";
import { FloatingMenuPortal } from "./ComposerFloatingMenu";
import { fileSelectionNavigation } from "./FileSelectionNavigation";
import type { FileSelectionPart } from "./FileSelectionCards";
import { useI18n } from "./i18n";
import { navigateToResponseSelection } from "./ResponseSelection";
import "./ComposerSelectionChip.css";

export type ComposerSelectionItem =
  | { type: "response"; selection: ResponseSelection }
  | { type: "file"; part: FileSelectionPart }
  | { type: "side-file"; source: FileSelectionSource };

export function ComposerSelectionChip({ items, onChangeResponse, onRemoveResponse, onEditFile, onRemoveFile, onRemoveSideFile, onOpenFile }: {
  items: ComposerSelectionItem[];
  onChangeResponse?: (selection: ResponseSelection) => void;
  onRemoveResponse?: (id: string) => void;
  onEditFile?: (part: FileSelectionPart, comment: string) => void;
  onRemoveFile?: (id: string) => void;
  onRemoveSideFile?: () => void;
  onOpenFile?: (path: string) => void;
}): JSX.Element | null {
  const { locale, t } = useI18n();
  const [open, setOpen] = useState(false);
  const [editingID, setEditingID] = useState<string | null>(null);
  const [locationNotice, setLocationNotice] = useState<string | null>(null);
  const anchorRef = useRef<HTMLButtonElement>(null);
  const panelRef = useRef<HTMLDivElement>(null);
  const closeTimerRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const panelID = useId();
  const allAnnotated = items.length > 0 && items.every((item) => item.type === "file"
    ? item.part.intent === "comment" || item.part.intent === "edit"
    : item.type === "response" && Boolean(item.selection.comment));
  const label = locale === "zh-CN"
    ? `${items.length} ${allAnnotated ? "条注释" : "个已选文本片段"}`
    : `${items.length} ${allAnnotated ? items.length === 1 ? "annotation" : "annotations"
      : items.length === 1 ? "selected text fragment" : "selected text fragments"}`;

  function cancelClose(): void { clearTimeout(closeTimerRef.current); }
  function close(): void {
    cancelClose();
    setOpen(false);
    setEditingID(null);
  }
  function contains(target: EventTarget | null): boolean {
    return target instanceof Node && Boolean(anchorRef.current?.contains(target) || panelRef.current?.contains(target));
  }
  function scheduleClose(): void {
    cancelClose();
    closeTimerRef.current = setTimeout(() => {
      if (!contains(document.activeElement)) close();
    }, 180);
  }

  useEffect(() => () => clearTimeout(closeTimerRef.current), []);
  useEffect(() => {
    if (!open) return;
    const dismiss = (event: PointerEvent) => { if (!contains(event.target)) close(); };
    const escape = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.preventDefault();
      close();
      anchorRef.current?.focus({ preventScroll: true });
    };
    document.addEventListener("pointerdown", dismiss, true);
    document.addEventListener("keydown", escape, true);
    return () => {
      document.removeEventListener("pointerdown", dismiss, true);
      document.removeEventListener("keydown", escape, true);
    };
  }, [open]);

  async function navigateToFile(source: FileSelectionSource): Promise<void> {
    let currentText: string | undefined;
    try {
      const file = await window.wuu.readWorkspaceFile(source.path, source.workspace);
      currentText = file.binary || file.truncated ? undefined : file.text;
    } catch {
      // The file can still be opened when its original span cannot be checked.
    }
    const target = fileSelectionNavigation(source, currentText);
    setLocationNotice(target.locationChanged
      ? currentText === undefined ? t("selectionChip.locationUnavailable") : t("selectionChip.locationChanged")
      : null);
    onOpenFile?.(target.path);
  }

  if (items.length === 0) return null;
  return <div className="composer-selection-row">
    <button ref={anchorRef} type="button" className="composer-selection-chip"
      aria-label={label} aria-expanded={open} aria-controls={open ? panelID : undefined} aria-haspopup="dialog"
      onPointerEnter={(event) => { if (event.pointerType !== "touch") { cancelClose(); setOpen(true); } }}
      onPointerLeave={(event) => { if (event.pointerType !== "touch") scheduleClose(); }}
      onFocus={() => { cancelClose(); setOpen(true); }}
      onBlur={(event) => { if (!contains(event.relatedTarget)) scheduleClose(); }}
      onClick={() => { cancelClose(); setOpen(true); }}>
      <MessageSquare aria-hidden="true" /><span>{label}</span>
    </button>
    {open ? <FloatingMenuPortal anchorRef={anchorRef} owner="composer-attach" placement="above" align="left" width={420} flip
      mobileSheet={{ label, onClose: close }}>
      <div ref={panelRef} id={panelID} className="composer-selection-panel" role="dialog" aria-label={label}
        onPointerEnter={cancelClose} onPointerLeave={scheduleClose}
        onFocus={cancelClose} onBlur={(event) => { if (!contains(event.relatedTarget)) scheduleClose(); }}>
        {items.map((item, index) => {
          const id = item.type === "response" ? item.selection.id : item.type === "file" ? item.part.id : "side-file";
          const quote = item.type === "response" ? item.selection.text : item.type === "file" ? item.part.source.quote : item.source.quote;
          const comment = item.type === "response" ? item.selection.comment ?? "" : item.type === "file" ? item.part.comment ?? "" : "";
          const source = item.type === "response" ? null : item.type === "file" ? item.part.source : item.source;
          const editable = item.type === "response" ? Boolean(onChangeResponse)
            : item.type === "file" && item.part.intent !== "quote" && Boolean(onEditFile);
          const remove = item.type === "response" && onRemoveResponse ? () => onRemoveResponse(item.selection.id)
            : item.type === "file" && onRemoveFile ? () => onRemoveFile(item.part.id)
              : item.type === "side-file" ? onRemoveSideFile : undefined;
          return <section className="composer-selection-entry" key={id}>
            <span className="composer-selection-number" aria-hidden="true">{index + 1}.</span>
            <div className="composer-selection-content">
              <div className="composer-selection-heading">
                <span>{locale === "zh-CN" ? "所选文本：" : "Selected text:"}</span>
                <div className="composer-selection-actions">
                  {editable ? <button type="button" aria-label={t("common.edit")}
                    onClick={() => setEditingID(editingID === id ? null : id)}><Pencil aria-hidden="true" /></button> : null}
                  {remove ? <button type="button" aria-label={t("common.remove")}
                    onClick={() => { remove(); if (items.length === 1) close(); }}><Trash2 aria-hidden="true" /></button> : null}
                </div>
              </div>
              <button type="button" className="composer-selection-quote" title={t("responseSelection.source")}
                onClick={() => {
                  if (item.type === "response") { if (navigateToResponseSelection(item.selection)) close(); }
                  else if (source && onOpenFile) void navigateToFile(source);
                }}>{quote.replace(/\s+/g, " ").trim()}</button>
              {editingID === id && editable ? <textarea autoFocus className="composer-selection-comment-input" wrap="soft"
                aria-label={t("responseSelection.optionalComment")}
                placeholder={t("responseSelection.optionalComment")}
                value={comment}
                onChange={(event) => {
                  if (item.type === "response") onChangeResponse?.({ ...item.selection, comment: event.target.value });
                  else if (item.type === "file") onEditFile?.(item.part, event.target.value);
                }}
                onKeyDown={(event) => { if (event.key === "Escape") { event.stopPropagation(); setEditingID(null); anchorRef.current?.focus(); } }} />
                : comment ? <div className="composer-selection-comment"><span>{locale === "zh-CN" ? "用户评论：" : "Comment:"}</span><p>{comment}</p></div> : null}
            </div>
          </section>;
        })}
        {locationNotice ? <p className="composer-selection-location-notice" role="status">{locationNotice}</p> : null}
      </div>
    </FloatingMenuPortal> : null}
  </div>;
}
