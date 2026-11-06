import type { ReactNode } from "react";
import { EmptyStateIllustration, type EmptyStateIllustrationName } from "./EmptyStateIllustration";

/**
 * The workspace panel's one empty state. Illustrated states show the artwork
 * alone; the title and description stay available to assistive technology.
 * Text-only states keep the original title, description, and action rhythm.
 */
export function WorkspacePanelEmpty({
  title,
  description,
  action,
  className,
  illustration,
}: {
  title: string;
  description?: string;
  action?: ReactNode;
  className?: string;
  illustration?: EmptyStateIllustrationName;
}): JSX.Element {
  return (
    <div
      className={className ? `workspace-panel-empty ${className}` : "workspace-panel-empty"}
      data-wuu-component="workspace-empty-state"
    >
      {illustration ? (
        <>
          <EmptyStateIllustration name={illustration} />
          <span className="sr-only" role="status">
            {title}{description ? `. ${description}` : ""}
          </span>
        </>
      ) : (
        <>
          <strong>{title}</strong>
          {description ? <span>{description}</span> : null}
        </>
      )}
      {action}
    </div>
  );
}
