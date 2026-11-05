import automationDark from "./assets/empty-states/automation-dark.svg";
import automationLight from "./assets/empty-states/automation-light.svg";
import connectionDark from "./assets/empty-states/connection-dark.svg";
import connectionLight from "./assets/empty-states/connection-light.svg";
import filesDark from "./assets/empty-states/files-dark.svg";
import filesLight from "./assets/empty-states/files-light.svg";
import pluginsDark from "./assets/empty-states/plugins-dark.svg";
import pluginsLight from "./assets/empty-states/plugins-light.svg";

const EMPTY_STATE_ART = {
  automation: { light: automationLight, dark: automationDark },
  files: { light: filesLight, dark: filesDark },
  plugins: { light: pluginsLight, dark: pluginsDark },
  connection: { light: connectionLight, dark: connectionDark },
} as const;

export type EmptyStateIllustrationName = keyof typeof EMPTY_STATE_ART;

/**
 * A decorative empty-state illustration. Both theme variants stay in the DOM
 * so a theme change never waits on an image load; CSS shows the one matching
 * the document theme. The title beside it carries the accessible meaning.
 */
export function EmptyStateIllustration({
  name,
  className,
}: {
  name: EmptyStateIllustrationName;
  className?: string;
}): JSX.Element {
  const art = EMPTY_STATE_ART[name];
  return (
    <div
      className={className ? `empty-state-illustration ${className}` : "empty-state-illustration"}
      data-illustration={name}
      aria-hidden="true"
    >
      <img className="empty-state-illustration-light" src={art.light} alt="" draggable={false} />
      <img className="empty-state-illustration-dark" src={art.dark} alt="" draggable={false} />
    </div>
  );
}
