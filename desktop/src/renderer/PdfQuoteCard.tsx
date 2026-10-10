import { useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import { FloatingMenuPortal } from "./ComposerFloatingMenu";
import { useI18n } from "./i18n";
import { ChevronDown, CornerUpLeft, Quote, Trash2, X } from "./WuuIcons";
import { Tooltip } from "./Tooltip";
import "./PdfQuoteCards.css";

/** A shared preview keeps hover reading separate from an explicitly opened editor. */
export function usePdfQuotePreview(onClose?: () => void) {
  const [open, setOpen] = useState(false);
  const anchorRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const panelRef = useRef<HTMLDivElement>(null);
  const closeTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const pinned = useRef(false);
  const returningFocus = useRef(false);
  const panelId = useId();

  function contains(target: EventTarget | null): boolean {
    return target instanceof Node && Boolean(anchorRef.current?.contains(target) || panelRef.current?.contains(target));
  }
  function retain(): void { clearTimeout(closeTimer.current); }
  function close(restoreFocus = false): void {
    retain();
    pinned.current = false;
    setOpen(false);
    onClose?.();
    if (restoreFocus) {
      returningFocus.current = true;
      triggerRef.current?.focus({ preventScroll: true });
      returningFocus.current = false;
    }
  }
  function leave(): void {
    retain();
    if (pinned.current) return;
    closeTimer.current = setTimeout(() => {
      if (!contains(document.activeElement)) close();
    }, 100);
  }
  function reveal(): void { retain(); setOpen(true); }
  function activate(): void {
    if (pinned.current) { close(); return; }
    pinned.current = true;
    reveal();
    requestAnimationFrame(() => panelRef.current?.focus({ preventScroll: true }));
  }

  useEffect(() => () => clearTimeout(closeTimer.current), []);
  useEffect(() => {
    if (!open) return;
    const dismiss = (event: PointerEvent): void => { if (!contains(event.target)) close(); };
    const windowBlur = (): void => close();
    const escape = (event: KeyboardEvent): void => {
      if (event.key !== "Escape" || event.isComposing) return;
      event.preventDefault();
      event.stopPropagation();
      close(contains(document.activeElement));
    };
    document.addEventListener("pointerdown", dismiss, true);
    document.addEventListener("keydown", escape, true);
    window.addEventListener("blur", windowBlur);
    return () => {
      document.removeEventListener("pointerdown", dismiss, true);
      document.removeEventListener("keydown", escape, true);
      window.removeEventListener("blur", windowBlur);
    };
  }, [open]);

  return { open, anchorRef, triggerRef, panelRef, panelId, close, retain, leave, activate,
    revealOnPointer: (pointerType: string) => { if (pointerType !== "touch") reveal(); },
    revealOnFocus: () => { if (!returningFocus.current && triggerRef.current?.matches(":focus-visible")) reveal(); },
    // Selecting non-focusable excerpt text blurs an editor to null. Outside
    // presses and window blur already dismiss; keep this internal gesture alive.
    blur: (next: EventTarget | null) => { if (next !== null && !contains(next)) close(); },
    focusPanel: () => {
      const first = panelRef.current?.querySelector<HTMLElement>("button:not(:disabled), textarea:not(:disabled), summary");
      (first ?? panelRef.current)?.focus({ preventScroll: true });
    },
  };
}

/** Expand only when actual rendered content exceeds the short reading preview. */
export function PdfQuoteExcerpt({ text }: { text: string }): JSX.Element {
  const { t } = useI18n();
  const [expanded, setExpanded] = useState(false);
  const [overflows, setOverflows] = useState(false);
  const textRef = useRef<HTMLQuoteElement>(null);
  const id = useId();
  useLayoutEffect(() => {
    const element = textRef.current;
    if (!element) return;
    const measure = () => {
      const lineHeight = Number.parseFloat(getComputedStyle(element).lineHeight);
      setOverflows(element.scrollHeight > lineHeight * 4 + 1);
    };
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    return () => observer.disconnect();
  }, [text]);
  return <div className="pdf-quote-excerpt" data-wuu-component="quote-excerpt">
    <blockquote ref={textRef} id={id} className="pdf-quote-excerpt-text" data-expanded={expanded}>{text}</blockquote>
    {overflows ? <button type="button" className="pdf-quote-disclosure" aria-expanded={expanded}
      aria-controls={id} onClick={() => setExpanded(value => !value)}>
      <span>{t(expanded ? "common.collapse" : "common.showMore")}</span><ChevronDown aria-hidden="true" />
    </button> : null}
  </div>;
}

/** PDF attachment presentation; source resolution and draft changes stay with the caller. */
export function ComposerPdfQuoteCard({ text, comment = "", meta, sourceName, location, className, notice,
  showComment = true, onChangeComment, onRemove, onOpenSource }: {
  text: string;
  comment?: string;
  meta?: string;
  sourceName?: string;
  location?: string;
  className?: string;
  notice?: string | null;
  showComment?: boolean;
  onChangeComment?: (comment: string) => void;
  onRemove?: () => void;
  onOpenSource?: () => boolean | Promise<boolean>;
}): JSX.Element {
  const { t } = useI18n();
  const [openingSource, setOpeningSource] = useState(false);
  const sourceRequest = useRef(0);
  const preview = usePdfQuotePreview(() => {
    sourceRequest.current += 1;
    setOpeningSource(false);
  });
  const sourceLabel = sourceName || meta || t("responseSelection.cardMeta");
  const sourceHeading = <><span>{sourceLabel}</span>
    {location ? <span className="pdf-quote-location">{location}</span> : null}
    {onOpenSource ? <CornerUpLeft aria-hidden="true" /> : null}</>;
  return <>
    <div ref={preview.anchorRef} className={`composer-attachment-card composer-document-card pdf-quote-tile${className ? ` ${className}` : ""}`}
      data-wuu-component="quote-card" data-wuu-variant="pdf"
      onPointerEnter={event => preview.revealOnPointer(event.pointerType)} onPointerLeave={preview.leave}
      onBlur={event => preview.blur(event.relatedTarget)}>
      <button ref={preview.triggerRef} type="button" className="composer-document-card-main pdf-quote-tile-main"
        aria-label={t("responseSelection.open")} aria-expanded={preview.open}
        aria-controls={preview.open ? preview.panelId : undefined} aria-haspopup="dialog"
        onClick={preview.activate} onFocus={preview.revealOnFocus}
        onKeyDown={event => {
          if (event.key === "Tab" && !event.shiftKey && preview.open) { event.preventDefault(); preview.focusPanel(); }
        }}>
        <span className="composer-document-card-icon" aria-hidden="true"><Quote className="icon" /></span>
        <span className="composer-document-card-text">
          <span className="composer-document-card-title pdf-quote-tile-excerpt">{text.replace(/\s+/g, " ").trim()}</span>
          <span className="composer-document-card-meta pdf-quote-tile-source">
            <span>{sourceLabel}</span>{location ? <span className="pdf-quote-location">{location}</span> : null}
          </span>
        </span>
      </button>
      {onRemove ? <button type="button" className="composer-attachment-card-remove pdf-quote-tile-remove" aria-label={t("responseSelection.remove")}
        onClick={() => { preview.close(); onRemove(); }}><X aria-hidden="true" /></button> : null}
    </div>
    {preview.open ? <FloatingMenuPortal anchorRef={preview.anchorRef} owner="composer-attach"
      placement="above" align="left" width={360} offset={4} flip
      boundarySelector=".composer-frame-shell, .composer-stack, .conversation-pane, .side-thread-panel"
      mobileSheet={{ label: t("responseSelection.quote"), onClose: () => preview.close(true) }}>
      <div ref={preview.panelRef} id={preview.panelId} className={`pdf-quote-preview${className ? ` ${className}-popover` : ""}`} role="dialog" tabIndex={-1}
        data-wuu-component="quote-preview" aria-label={t("responseSelection.quote")}
        onPointerEnter={preview.retain} onPointerLeave={preview.leave}
        onFocus={preview.retain} onBlur={event => preview.blur(event.relatedTarget)}
        onKeyDown={event => event.stopPropagation()}>
        <div className="pdf-quote-preview-heading">
          {onOpenSource ? <Tooltip content={t("responseSelection.source")}>
            <button type="button" className="pdf-quote-preview-source pdf-quote-source-action" disabled={openingSource}
              aria-label={`${t("responseSelection.source")}: ${sourceLabel}${location ? ` · ${location}` : ""}`}
              onClick={async () => {
                const request = ++sourceRequest.current;
                setOpeningSource(true);
                try {
                  const opened = await onOpenSource();
                  if (request === sourceRequest.current && opened) preview.close();
                } finally { if (request === sourceRequest.current) setOpeningSource(false); }
              }}>{sourceHeading}</button>
          </Tooltip> : <div className="pdf-quote-preview-source">{sourceHeading}</div>}
          {onRemove ? <Tooltip content={t("responseSelection.remove")}>
            <button type="button" className="pdf-quote-icon-action pdf-quote-remove-action" aria-label={t("responseSelection.remove")}
              onClick={() => { preview.close(); onRemove(); }}><Trash2 aria-hidden="true" /></button>
          </Tooltip> : null}
        </div>
        <PdfQuoteExcerpt text={text} />
        {showComment && (onChangeComment || comment) ? onChangeComment ? <textarea
          className="pdf-quote-comment-input" rows={1} wrap="soft" value={comment}
          aria-label={t("responseSelection.optionalComment")} placeholder={t("responseSelection.optionalComment")}
          onChange={event => onChangeComment(event.target.value)} />
          : <p className="pdf-quote-comment">{comment}</p> : null}
        {notice ? <p className="pdf-quote-notice" role="status">{notice}</p> : null}
      </div>
    </FloatingMenuPortal> : null}
  </>;
}

