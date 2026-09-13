import type * as React from "react";

export interface ComposerDrawerProps extends Omit<React.HTMLAttributes<HTMLElement>, "title"> {
  /** Controlled expansion; the caller retains feature state. */
  expanded: boolean;
  onExpandedChange: (expanded: boolean) => void;
  toggleLabel: string;
  icon?: React.ReactNode;
  summary: React.ReactNode;
  actions?: React.ReactNode;
  /** Persistent feedback remains visible while the drawer is collapsed. */
  notice?: React.ReactNode;
  tone?: "default" | "muted" | "warning";
  children?: React.ReactNode;
}

/** Shared by host accessories and public plugin UI, using the host React instance. */
export function createComposerDrawer(react: typeof React): React.ComponentType<ComposerDrawerProps> {
  const h = react.createElement;
  return function ComposerDrawer({ expanded, onExpandedChange, toggleLabel, icon, summary, actions, notice,
    tone = "default", children, className, onKeyDown, ...props }: ComposerDrawerProps) {
    const detailsId = react.useId();
    const trigger = react.useRef<HTMLButtonElement>(null);
    const wasExpanded = react.useRef(expanded);
    react.useEffect(() => {
      if (wasExpanded.current && !expanded && trigger.current) {
        const owner = trigger.current.ownerDocument;
        // Do not steal focus when another accessory caused this drawer to close.
        if (owner.activeElement === owner.body || trigger.current.closest("section")?.contains(owner.activeElement)) {
          trigger.current.focus();
        }
      }
      wasExpanded.current = expanded;
    }, [expanded]);
    return h("section", {
      ...props,
      className: `composer-accessory-drawer${expanded ? " expanded" : ""}${className ? ` ${className}` : ""}`,
      "data-tone": tone,
      "data-wuu-state": expanded ? "expanded" : "collapsed",
      onKeyDown: (event: React.KeyboardEvent<HTMLElement>) => {
        onKeyDown?.(event);
        if (!event.defaultPrevented && event.key === "Escape" && expanded) {
          event.stopPropagation();
          onExpandedChange(false);
        }
      },
    },
      h("div", { className: "composer-drawer-summary" },
        h("button", {
          ref: trigger, type: "button", className: "composer-drawer-summary-select",
          "aria-label": toggleLabel, "aria-expanded": expanded,
          "aria-controls": expanded ? detailsId : undefined,
          onClick: () => onExpandedChange(!expanded),
        },
          icon ? h("span", { className: "composer-drawer-icon", "aria-hidden": true }, icon) : null,
          h("span", { className: "composer-drawer-title" }, summary),
          h("svg", { className: "composer-drawer-chevron", width: 14, height: 14, viewBox: "0 0 24 24", fill: "none", stroke: "currentColor", strokeWidth: 1.7, "aria-hidden": true },
            h("path", { d: expanded ? "m6 9 6 6 6-6" : "m6 15 6-6 6 6" })),
        ),
        actions ? h("div", { className: "composer-drawer-actions" }, actions) : null,
      ),
      notice,
      expanded ? h("div", { id: detailsId, className: "composer-drawer-details" }, children) : null,
    );
  };
}
