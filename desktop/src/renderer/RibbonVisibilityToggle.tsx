import { flushSync } from "react-dom";
import { PanelLeftClose, PanelLeftOpen } from "./WuuIcons";
import { readNavigationRibbonPreferences, saveNavigationRibbonPreferences } from "./NavigationRibbonPreferences";
import { showActionErrorToast } from "./Toast";
import { TruncatedText } from "./TruncatedText";
import { Tooltip } from "./Tooltip";
import { useI18n } from "./i18n";

export function RibbonVisibilityToggle({ collapsed, nativeTooltip = false, labelVisible = false }: { collapsed: boolean; nativeTooltip?: boolean; labelVisible?: boolean }): JSX.Element {
  const { t } = useI18n();
  const label = t(collapsed ? "ribbon.expand" : "ribbon.collapse");
  return <Tooltip content={label} native={nativeTooltip} disabled={labelVisible}>
    <button type="button"
      className={collapsed ? "icon-button ribbon-restore-toggle" : "sidebar-ribbon-button ribbon-collapse-toggle"}
      aria-label={label}
      onClick={event => {
        const trigger = event.currentTarget;
        try {
          // Commit the replacement control before moving keyboard focus. Pointer
          // clicks must not open a tooltip on a control across the window.
          flushSync(() => saveNavigationRibbonPreferences({ ...readNavigationRibbonPreferences(), collapsed: !collapsed }));
          if (event.detail === 0) {
            document.querySelector<HTMLButtonElement>(collapsed ? ".ribbon-collapse-toggle" : ".ribbon-restore-toggle")?.focus({ preventScroll: true });
          } else {
            trigger.blur();
          }
        } catch (error) { showActionErrorToast(error, t("ribbon.saveFailed")); }
      }}
    >
      {collapsed ? <PanelLeftOpen aria-hidden="true" /> : <PanelLeftClose aria-hidden="true" />}
      {!collapsed ? <TruncatedText className="sidebar-ribbon-label" text={label} nativeTooltip={nativeTooltip} /> : null}
    </button>
  </Tooltip>;
}
