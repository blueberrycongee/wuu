import { useComposerServices } from "../ComposerServicesContext";
import type { ApplicationComposerContributionV1, ApplicationComposerContributionContextV1 } from "../../../../packages/plugin-sdk/src/ui-composer-presentation";
import { Ellipsis } from "../WuuIcons";
import {
  Fragment,
  useCallback,
  useEffect,
  useRef,
  useState,
  useSyncExternalStore,
  type ReactNode,
} from "react";

import { FloatingMenuPortal, isInsideFloatingMenu } from "../ComposerFloatingMenu";
import { useI18n } from "../i18n";
import type { PluginHost, PluginSlotRenderContext, RegisteredPluginSlotContribution } from "./PluginHost";
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
  const services = useComposerServices();
  const [open, setOpen] = useState(false);
  const triggerRef = useRef<HTMLDivElement>(null);
  const subscribe = useCallback(
    (listener: () => void) => services ? services.presentation.subscribe(listener) : host.subscribeSlot("composer.toolbar", listener),
    [host, services],
  );
  const getSnapshot = useCallback(
    () => services ? services.presentation.getContributions("composer.toolbar") : host.getSlotSnapshot("composer.toolbar"),
    [host, services],
  );
  const contributions = useSyncExternalStore<readonly RegisteredPluginSlotContribution[] | readonly ApplicationComposerContributionV1[]>(subscribe, getSnapshot, getSnapshot);
  const items = contributions.map(contribution => "View" in contribution
    ? { key: contribution.key, title: contribution.title || contribution.key, node: <contribution.View context={{ ownerTabId: services!.ownerTabId, variant: context.variant as ApplicationComposerContributionContextV1["variant"], mainConversation: context.mainConversation === true, running: context.running === true, readOnly: context.readOnly === true, hasDraft: context.hasDraft === true, attachmentCount: context.attachmentCount as number }} /> }
    : { key: `${contribution.pluginId}:${contribution.generation}:${contribution.id}`, title: contribution.title || `${contribution.pluginId}: ${contribution.id}`, node: <PluginSlotContribution host={host} id="composer.toolbar" context={context} contribution={contribution} /> });
  const inline = items.slice(0, INLINE_PLUGIN_TOOL_LIMIT);
  const overflow = items.slice(INLINE_PLUGIN_TOOL_LIMIT);

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
