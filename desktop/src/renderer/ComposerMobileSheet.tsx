import { useLayoutEffect, useRef, useState, type ReactNode, type RefObject } from "react";
import { X } from "lucide-react";
import { useI18n } from "./i18n";
import { UILayerPortal } from "./ui/layers/UILayerHost";
import type { FloatingMenuOwner } from "./ComposerTypes";

/** Touch pickers dismiss editing before taking focus and never reopen the IME on close. */
export function ComposerMobileSheet({ anchorRef, owner, label, onClose, children }: {
  anchorRef: RefObject<HTMLElement | null>;
  owner: FloatingMenuOwner;
  label: string;
  onClose: () => void;
  children: ReactNode;
}): JSX.Element {
  const { t } = useI18n();
  const dialogRef = useRef<HTMLDialogElement>(null);
  const closeRef = useRef<HTMLButtonElement>(null);
  const [ready, setReady] = useState(false);

  useLayoutEffect(() => {
    const dialog = dialogRef.current!;
    const active = document.activeElement;
    const editing = active instanceof HTMLElement && active.matches('input, textarea, [contenteditable="true"]');
    if (editing) active.blur();
    // Native dialog focus restoration must target the trigger, not the editor.
    const trigger = anchorRef.current?.querySelector<HTMLButtonElement>('button') ?? anchorRef.current;
    trigger?.focus({ preventScroll: true });
    dialog.showModal();
    closeRef.current?.focus({ preventScroll: true });

    let quiet: ReturnType<typeof setTimeout> | undefined;
    let deadline: ReturnType<typeof setTimeout> | undefined;
    const viewport = window.visualViewport;
    const reveal = () => {
      clearTimeout(quiet);
      clearTimeout(deadline);
      viewport?.removeEventListener('resize', settle);
      setReady(true);
    };
    const settle = () => {
      clearTimeout(quiet);
      quiet = setTimeout(reveal, 120);
    };
    if (editing) {
      // IMEs can emit several viewport sizes, including when changing keyboard
      // type. Keep the modal barrier active while the picker waits for layout.
      viewport?.addEventListener('resize', settle);
      settle();
      deadline = setTimeout(reveal, 700);
    } else {
      setReady(true);
    }
    return () => {
      clearTimeout(quiet);
      clearTimeout(deadline);
      viewport?.removeEventListener('resize', settle);
      dialog.close();
    };
  }, [anchorRef]);

  useLayoutEffect(() => {
    if (ready) closeRef.current?.focus({ preventScroll: true });
  }, [ready]);

  return <UILayerPortal layer="dialog">
    <dialog ref={dialogRef} className="composer-mobile-sheet" aria-label={label}
      data-floating-menu-owner={owner} data-ready={ready}
      onCancel={(event) => { event.preventDefault(); onClose(); }}
      onKeyDownCapture={(event) => {
        // Also handles Android's synthetic Escape routed to a nested menu.
        if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); onClose(); }
      }}
      onPointerDown={(event) => event.stopPropagation()}
      onClick={(event) => { if (event.target === event.currentTarget) onClose(); }}>
      <section className="composer-mobile-sheet-panel">
        <header><span>{label}</span><button ref={closeRef} type="button" aria-label={t('common.close')} onClick={onClose}><X aria-hidden="true" /></button></header>
        <div className="composer-mobile-sheet-content">{children}</div>
      </section>
    </dialog>
  </UILayerPortal>;
}
