import { useEffect, useId, useLayoutEffect, useRef, useState, type RefObject } from "react";
import { Ellipsis, Info, SquarePen } from "lucide-react";
import { FloatingMenuPortal } from "./ComposerFloatingMenu";
import { isTouchWebShell } from "./ComposerFocus";
import { LinuxWindowControls } from "./LinuxWindowControls";
import { SidePanelToggleIcon } from "./SidePanelToggleIcon";
import { useI18n } from "./i18n";

export function CompactConversationActions({
  canStartNewThread, onStartNewThread, environmentToggleRef,
  environmentPanelVisible, onToggleEnvironmentPanel, rightPanelOpen, onToggleRightPanel,
}: {
  canStartNewThread: boolean;
  onStartNewThread: () => void;
  environmentToggleRef: RefObject<HTMLButtonElement | null>;
  environmentPanelVisible: boolean;
  onToggleEnvironmentPanel: () => void;
  rightPanelOpen: boolean;
  onToggleRightPanel: () => void;
}): JSX.Element {
  const { t } = useI18n();
  const showPanelActions = !isTouchWebShell();
  const [open, setOpen] = useState(false);
  const menuRef = useRef<HTMLDivElement>(null);
  const initialFocus = useRef(0);
  const menuID = useId();
  const items = () => Array.from(menuRef.current?.querySelectorAll<HTMLButtonElement>("[role=menuitem]:not(:disabled)") ?? []);
  useLayoutEffect(() => {
    if (open) {
      const buttons = items();
      buttons[initialFocus.current < 0 ? buttons.length - 1 : initialFocus.current]?.focus({ preventScroll: true });
    }
  }, [open]);
  useEffect(() => {
    if (!open) return;
    const dismissOutside = (event: Event) => {
      const target = event.target;
      if (target instanceof Node && !menuRef.current?.contains(target) && !environmentToggleRef.current?.contains(target)) {
        setOpen(false);
      }
    };
    document.addEventListener("pointerdown", dismissOutside);
    document.addEventListener("focusin", dismissOutside);
    return () => {
      document.removeEventListener("pointerdown", dismissOutside);
      document.removeEventListener("focusin", dismissOutside);
    };
  }, [open, environmentToggleRef]);
  const close = () => {
    setOpen(false);
    environmentToggleRef.current?.focus({ preventScroll: true });
  };
  return (
    <div className="title-actions compact-conversation-actions">
      <button type="button" className="icon-button" aria-label={t("tabs.newConversation")} title={t("tabs.newConversation")}
        disabled={!canStartNewThread} onClick={onStartNewThread}>
        <SquarePen size={18} aria-hidden="true" />
      </button>
      {showPanelActions && <button ref={environmentToggleRef} type="button" className="icon-button" aria-label={t("shell.moreActions")}
        title={t("shell.moreActions")} aria-haspopup="menu" aria-expanded={open} aria-controls={open ? menuID : undefined}
        onClick={() => { initialFocus.current = 0; setOpen(!open); }}
        onKeyDown={(event) => {
          if (event.key === "ArrowDown" || event.key === "ArrowUp") {
            event.preventDefault();
            initialFocus.current = event.key === "ArrowUp" ? -1 : 0;
            setOpen(true);
          }
        }}>
        <Ellipsis size={18} aria-hidden="true" />
      </button>}
      {showPanelActions && open && <FloatingMenuPortal anchorRef={environmentToggleRef} owner="conversation-actions" placement="below" align="right" width={224}>
        <div ref={menuRef} id={menuID} role="menu" aria-label={t("shell.moreActions")} className="conversation-actions-menu"
          onKeyDown={(event) => {
            const buttons = items();
            const index = buttons.indexOf(document.activeElement as HTMLButtonElement);
            if (["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) {
              event.preventDefault();
              const next = event.key === "Home" ? 0 : event.key === "End" ? buttons.length - 1
                : (index + (event.key === "ArrowDown" ? 1 : -1) + buttons.length) % buttons.length;
              buttons[next]?.focus();
            } else if (event.key === "Escape") {
              event.preventDefault();
              event.stopPropagation();
              close();
            } else if (event.key === "Tab") {
              // Resume native tab order at the trigger, not at the portal at the end of the document.
              close();
            }
          }}>
          <button type="button" role="menuitem" tabIndex={-1} onClick={() => { close(); onToggleEnvironmentPanel(); }}>
            <Info size={18} aria-hidden="true" />
            {t(environmentPanelVisible ? "shell.hideEnvironmentInfo" : "shell.showEnvironmentInfo")}
          </button>
          <button type="button" role="menuitem" tabIndex={-1} onClick={() => { close(); onToggleRightPanel(); }}>
            <SidePanelToggleIcon side="right" open={rightPanelOpen} />
            {t(rightPanelOpen ? "shell.closeRightSidebar" : "shell.openRightSidebar")}
          </button>
        </div>
      </FloatingMenuPortal>}
      <LinuxWindowControls />
    </div>
  );
}
