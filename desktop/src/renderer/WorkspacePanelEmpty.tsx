import type { ReactNode } from "react";

/**
 * The workspace panel's one empty state: what is missing, a line only when
 * there is something the title does not say, and at most one next action.
 */
export function WorkspacePanelEmpty({
  title,
  description,
  action,
  className,
}: {
  title: string;
  description?: string;
  action?: ReactNode;
  className?: string;
}): JSX.Element {
  return (
    <div
      className={className ? `workspace-panel-empty ${className}` : "workspace-panel-empty"}
      data-wuu-component="workspace-empty-state"
    >
      <strong>{title}</strong>
      {description ? <span>{description}</span> : null}
      {action}
    </div>
  );
}
