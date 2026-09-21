import { useEffect, useId, useLayoutEffect, useRef, useState, type ReactNode, type RefObject } from "react";
import { Ellipsis } from "lucide-react";
import { FloatingMenuPortal } from "./ComposerFloatingMenu";
import type { FloatingMenuOwner } from "./ComposerTypes";

/** Secondary header actions share keyboard navigation and focus restoration. */
export function HeaderActionMenu({ label, owner, triggerRef, items }: {
  label: string;
  owner: FloatingMenuOwner;
  triggerRef: RefObject<HTMLButtonElement | null>;
  items: readonly { label: string; icon: ReactNode; onSelect: () => void }[];
}): JSX.Element {
  const [open, setOpen] = useState(false);
  const menuRef = useRef<HTMLDivElement>(null);
  const initialFocus = useRef(0);
  const menuID = useId();
  const buttons = () => Array.from(menuRef.current?.querySelectorAll<HTMLButtonElement>("[role=menuitem]") ?? []);
  useLayoutEffect(() => {
    if (open) {
      const entries = buttons();
      const target = entries[initialFocus.current < 0 ? entries.length - 1 : 0];
      target?.focus({ preventScroll: true });
      // FloatingMenuPortal starts hidden until its positioning commit. Chromium
      // ignores focus on that hidden subtree, unlike DOM-only test runtimes.
      if (target && document.activeElement !== target) {
        const frame = requestAnimationFrame(() => target.focus({ preventScroll: true }));
        return () => cancelAnimationFrame(frame);
      }
    }
  }, [open]);
  useEffect(() => {
    if (!open) return;
    const dismissOutside = (event: Event) => {
      const target = event.target;
      if (target instanceof Node && !menuRef.current?.contains(target) && !triggerRef.current?.contains(target)) setOpen(false);
    };
    document.addEventListener("pointerdown", dismissOutside);
    document.addEventListener("focusin", dismissOutside);
    return () => {
      document.removeEventListener("pointerdown", dismissOutside);
      document.removeEventListener("focusin", dismissOutside);
    };
  }, [open, triggerRef]);
  const close = () => {
    setOpen(false);
    triggerRef.current?.focus({ preventScroll: true });
  };
  return <>
    <button ref={triggerRef} type="button" className="icon-button" aria-label={label} title={label}
      aria-haspopup="menu" aria-expanded={open} aria-controls={open ? menuID : undefined}
      onClick={() => { initialFocus.current = 0; setOpen(!open); }}
      onKeyDown={(event) => {
        if (event.key === "ArrowDown" || event.key === "ArrowUp") {
          event.preventDefault();
          initialFocus.current = event.key === "ArrowUp" ? -1 : 0;
          setOpen(true);
        }
      }}><Ellipsis aria-hidden="true" /></button>
    {open && <FloatingMenuPortal anchorRef={triggerRef} owner={owner} placement="below" align="right" width={224}>
      <div ref={menuRef} id={menuID} role="menu" aria-label={label} className="conversation-actions-menu"
        onKeyDown={(event) => {
          const entries = buttons();
          const index = entries.indexOf(document.activeElement as HTMLButtonElement);
          if (["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) {
            event.preventDefault();
            const next = event.key === "Home" ? 0 : event.key === "End" ? entries.length - 1
              : (index + (event.key === "ArrowDown" ? 1 : -1) + entries.length) % entries.length;
            entries[next]?.focus();
          } else if (event.key === "Escape") {
            event.preventDefault();
            event.stopPropagation();
            close();
          } else if (event.key === "Tab") {
            // Resume native tab order at the trigger instead of the portal.
            close();
          }
        }}>
        {items.map((item) => <button key={item.label} type="button" role="menuitem" tabIndex={-1}
          onClick={() => { close(); item.onSelect(); }}>{item.icon}{item.label}</button>)}
      </div>
    </FloatingMenuPortal>}
  </>;
}
