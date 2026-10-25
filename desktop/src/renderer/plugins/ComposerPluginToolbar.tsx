import type { UISlotLayoutProps } from "../../../../packages/plugin-sdk/src/ui";
import { Ellipsis } from "../WuuIcons";
import {
  Fragment,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";

import { FloatingMenuPortal, isInsideFloatingMenu } from "../ComposerFloatingMenu";
import { useI18n } from "../i18n";

const INLINE_PLUGIN_TOOL_LIMIT = 2;

export function ComposerPluginToolbar({ items }: UISlotLayoutProps): ReactNode {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const [compact, setCompact] = useState(false);
  const toolbarRef = useRef<HTMLDivElement>(null);
  const expandedWidth = useRef(0);
  const measuredItems = useRef(items);
  const triggerRef = useRef<HTMLDivElement>(null);
  const inlineCount = compact ? 0 : INLINE_PLUGIN_TOOL_LIMIT;
  const inline = items.slice(0, inlineCount);
  const overflow = items.slice(inlineCount);

  useLayoutEffect(() => {
    const toolbar = toolbarRef.current;
    const parent = toolbar?.parentElement;
    if (!toolbar || !parent) return;
    const measure = (): void => {
      if (compact && measuredItems.current !== items) {
        setCompact(false);
        return;
      }
      if (!compact) {
        expandedWidth.current = toolbar.scrollWidth;
        measuredItems.current = items;
      }
      const available = parent.getBoundingClientRect().right - toolbar.getBoundingClientRect().left;
      setCompact(available < expandedWidth.current);
    };
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(parent);
    observer.observe(toolbar);
    return () => observer.disconnect();
  }, [compact, items]);

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
    document.addEventListener("pointerdown", handlePointerDown, true);
    document.addEventListener("keydown", handleKeyDown);
    return () => {
      document.removeEventListener("pointerdown", handlePointerDown, true);
      document.removeEventListener("keydown", handleKeyDown);
    };
  }, [open]);

  if (items.length === 0) {
    return null;
  }

  return (
    <div className="composer-plugin-toolbar" ref={toolbarRef}>
      {inline.map(item => <Fragment key={item.key}>{item.node}</Fragment>)}
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
                  const label = contribution.title;
                  return (
                    <div
                      className="composer-plugin-tools-item"
                      role="group"
                      aria-label={label}
                      key={contribution.key}
                    >
                      <span className="composer-plugin-tools-label">{label}</span>
                      {contribution.node}
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
