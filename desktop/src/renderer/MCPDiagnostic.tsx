import { useId, useState } from "react";
import { useI18n } from "./i18n";
import { AlertCircle, ChevronRight } from "./WuuIcons";

export function MCPDiagnostic({ message }: { message: string }): JSX.Element {
  const { t } = useI18n();
  const id = useId();
  const [expanded, setExpanded] = useState(false);
  return (
    <div className="settings-mcp-diagnostic">
      <button className="settings-button settings-button-ghost settings-mcp-details-toggle" type="button" aria-expanded={expanded} aria-controls={id} onClick={() => setExpanded(!expanded)}>
        <ChevronRight className="icon-sm settings-disclosure-chevron" aria-hidden="true" />
        {t("mcp.technicalDetails")}
      </button>
      {expanded ? <pre id={id} className="settings-mcp-diagnostic-text">{message}</pre> : null}
    </div>
  );
}

export function MCPFailure({ message, diagnostic }: { message: string; diagnostic: string }): JSX.Element {
  return <div className="mcp-failure">
    <p className="mcp-service-status" role="alert"><AlertCircle className="icon-sm" aria-hidden="true" />{message}</p>
    <MCPDiagnostic key={diagnostic} message={diagnostic} />
  </div>;
}
