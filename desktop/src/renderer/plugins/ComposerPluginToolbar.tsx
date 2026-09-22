import { Ellipsis } from "../WuuIcons";
import {
  useCallback,
  useEffect,
  useRef,
  useState,
  useSyncExternalStore,
  type ReactNode,
} from "react";

import { FloatingMenuPortal, isInsideFloatingMenu } from "../ComposerFloatingMenu";
import { useI18n } from "../i18n";
import type { PluginHost, PluginSlotRenderContext } from "./PluginHost";
import { PluginSlotContribution } from "./PluginSlot";

const INLINE_PLUGIN_TOOL_LIMIT = 2;

export function ComposerPluginToolbar({
  host,
  context,
}: {
  host: PluginHost;
  context: PluginSlotRenderContext;
}): ReactNode {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const triggerRef = useRef<HTMLDivElement>(null);
  const subscribe = useCallback(
    (listener: () => void) => host.subscribeSlot("composer.toolbar", listener),
    [host],
  );
  const getSnapshot = useCallback(
    () => host.getSlotSnapshot("composer.toolbar"),
    [host],
  );
  const contributions = useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
  const inline = contributions.slice(0, INLINE_PLUGIN_TOOL_LIMIT);
  const overflow = contributions.slice(INLINE_PLUGIN_TOOL_LIMIT);

  useEffect(() => {
    if (overflow.length === 0) {
      setOpen(false);
    }
  }, [overflow.length]);

  useEffect(() => {
    if (!open) {
      return;
    }
    const handlePointerDown = (event: PointerEvent): void => {
      const target = event.target;
      if (!(target instanceof Node)) {
        return;
      }
      if (triggerRef.current?.contains(target) || isInsideFloatingMenu(target, "composer-plugin-tools")) {
        return;
      }
      setOpen(false);
    };
    const handleKeyDown = (event: KeyboardEvent): void => {
      if (event.key === "Escape") {
        setOpen(false);
      }
    };
    document.addEventListener("pointerdown", handlePointerDown);
    document.addEventListener("keydown", handleKeyDown);
    return () => {
      document.removeEventListener("pointerdown", handlePointerDown);
      document.removeEventListener("keydown", handleKeyDown);
    };
  }, [open]);

  if (contributions.length === 0) {
    return null;
  }

  return (
    <div className="composer-plugin-toolbar">
      {inline.map((contribution) => (
        <PluginSlotContribution
          key={`${contribution.pluginId}:${contribution.generation}:${contribution.id}`}
          host={host}
          id="composer.toolbar"
          context={context}
          contribution={contribution}
        />
      ))}
      {overflow.length > 0 ? (
        <div className="composer-plugin-tools-anchor" ref={triggerRef}>
          <button
            className="composer-tool-button composer-plugin-tools-trigger"
            type="button"
            aria-haspopup="dialog"
            aria-expanded={open}
            aria-label={t("composer.pluginToolsMore")}
            title={t("composer.pluginToolsMore")}
            onClick={() => setOpen((current) => !current)}
          >
            <Ellipsis aria-hidden="true" />
          </button>
          {open ? (
            <FloatingMenuPortal
              anchorRef={triggerRef}
              owner="composer-plugin-tools"
              placement="above"
              align="left"
              width={240}
            >
              <div
                className="composer-context-menu composer-plugin-tools-menu"
                role="dialog"
                aria-label={t("composer.pluginToolsMore")}
                onClickCapture={(event) => {
                  if (event.target instanceof Element && event.target.closest("button")) {
                    setOpen(false);
                  }
                }}
              >
                {overflow.map((contribution) => {
                  const label = contribution.title || `${contribution.pluginId}: ${contribution.id}`;
                  return (
                    <div
                      className="composer-plugin-tools-item"
                      role="group"
                      aria-label={label}
                      key={`${contribution.pluginId}:${contribution.generation}:${contribution.id}`}
                    >
                      <span className="composer-plugin-tools-label">{label}</span>
                      <PluginSlotContribution
                        host={host}
                        id="composer.toolbar"
                        context={context}
                        contribution={contribution}
                      />
                    </div>
                  );
                })}
              </div>
            </FloatingMenuPortal>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
