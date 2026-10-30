import { useRef, useState } from "react";
import type { MCPDefinitionResult, MCPServerConfig } from "../../../packages/plugin-sdk/src/ui-settings-controls";
import { Modal } from "./Modal";
import { SelectMenu } from "./SelectMenu";
import { useSettingsControls } from "./SettingsControlsContext";
import { useI18n } from "./i18n";
import { toastErrorMessage } from "./Toast";
import { MCPFailure } from "./MCPDiagnostic";

export function MCPServerEditor({ name: originalName, definition, onClose, onSaved }: {
  name: string;
  definition?: MCPDefinitionResult;
  onClose(): void;
  onSaved(): void;
}): JSX.Element {
  const controls = useSettingsControls();
  const { t } = useI18n();
  const initial = definition?.config ?? {};
  const [mode, setMode] = useState(initial.command ? "command" : "url");
  const [name, setName] = useState(originalName);
  const [url, setURL] = useState(initial.url || "");
  const [command, setCommand] = useState(initial.command || "");
  const [args, setArgs] = useState(JSON.stringify(initial.args || []));
  const [enabled, setEnabled] = useState(initial.enabled !== false);
  const [advanced, setAdvanced] = useState(() => {
    const { command: _command, args: _args, url: _url, enabled: _enabled, ...extra } = initial;
    return JSON.stringify(extra, null, 2);
  });
  const [importJSON, setImportJSON] = useState("");
  const [error, setError] = useState<{ message: string; diagnostic: string } | null>(null);
  const [saving, setSaving] = useState(false);
  const inFlight = useRef(false);
  const title = t(definition ? "mcp.edit" : "mcp.add");

  async function save(): Promise<void> {
    if (inFlight.current) return;
    inFlight.current = true;
    setSaving(true);
    setError(null);
    let writing = false;
    try {
      let servers: Record<string, MCPServerConfig>;
      if (mode === "import") {
        const parsed = JSON.parse(importJSON);
        const source = parsed.mcpServers ?? parsed.mcp_servers ?? parsed;
        if (!source || typeof source !== "object" || Array.isArray(source)) throw new Error(t("mcp.invalidImport"));
        servers = Object.fromEntries(Object.entries(source).map(([key, value]) => [key, normalizeDefinition(value)]));
      } else {
        const extra = JSON.parse(advanced);
        if (!extra || typeof extra !== "object" || Array.isArray(extra)) throw new Error(t("mcp.invalidImport"));
        const next = { ...extra, enabled };
        delete next.command;
        delete next.args;
        delete next.url;
        if (mode === "url") next.url = url.trim();
        else { next.command = command.trim(); next.args = JSON.parse(args); }
        servers = { [name.trim()]: normalizeDefinition(next) };
      }
      writing = true;
      await controls.configureMCPServers({ servers, revision: definition?.revision });
      onSaved();
    } catch (failure) { setError({ message: t(writing ? "mcp.saveFailed" : "mcp.configurationInvalid"), diagnostic: toastErrorMessage(failure) }); }
    finally { inFlight.current = false; setSaving(false); }
  }

  return <Modal ariaLabel={title} title={title} onClose={onClose} closeDisabled={saving} panelClassName="mcp-editor" asForm onSubmit={() => void save()} footer={<>
    <button type="button" className="settings-button" disabled={saving} onClick={onClose}>{t("common.cancel")}</button>
    <button type="submit" className="settings-button settings-button-primary" disabled={saving}>{t(mode === "import" ? "mcp.import" : "common.save")}</button>
  </>}>
    <div className="mcp-form">
      <div className="mcp-field"><span>{t("mcp.connectionType")}</span><SelectMenu ariaLabel={t("mcp.connectionType")} value={mode} onChange={setMode} disabled={saving} options={[
        { value: "url", label: t("mcp.remoteURL") }, { value: "command", label: t("mcp.localCommand") },
        ...(!definition ? [{ value: "import", label: t("mcp.importJSON") }] : []),
      ]} /></div>
      {mode === "import" ? <>
        <label className="mcp-field">{t("mcp.importJSON")}<textarea className="settings-input mcp-code-input" value={importJSON} onChange={event => setImportJSON(event.target.value)} rows={10} spellCheck={false} placeholder={'{ "mcpServers": { "docs": { "url": "https://example.com/mcp" } } }'} /></label>
        <p className="settings-section-description">{t("mcp.importHint")}</p>
      </> : <>
        <label className="mcp-field">{t("mcp.name")}<input className="settings-input" value={name} onChange={event => setName(event.target.value)} required disabled={!!definition || saving} autoComplete="off" /></label>
        {mode === "url" ? <label className="mcp-field">{t("mcp.remoteURL")}<input className="settings-input" type="url" value={url} onChange={event => setURL(event.target.value)} required placeholder="https://example.com/mcp" autoComplete="off" /></label>
          : <>
            <label className="mcp-field">{t("mcp.localCommand")}<input className="settings-input" value={command} onChange={event => setCommand(event.target.value)} required placeholder="npx" autoComplete="off" /></label>
            <label className="mcp-field">{t("mcp.arguments")}<textarea className="settings-input mcp-code-input" value={args} onChange={event => setArgs(event.target.value)} rows={2} spellCheck={false} placeholder={'["-y", "your-mcp-server"]'} /></label>
          </>}
        <label className="mcp-checkbox"><input type="checkbox" checked={enabled} onChange={event => setEnabled(event.target.checked)} />{t("mcp.startEnabled")}</label>
        <details className="mcp-advanced"><summary>{t("mcp.advanced")}</summary>
          <label className="mcp-field">{t("mcp.advancedJSON")}<textarea className="settings-input mcp-code-input" value={advanced} onChange={event => setAdvanced(event.target.value)} rows={7} spellCheck={false} /></label>
        </details>
      </>}
      {error ? <MCPFailure message={error.message} diagnostic={error.diagnostic} /> : null}
    </div>
  </Modal>;
}

// Accept the two common configuration envelopes, but never silently discard a
// client's connection fields (for example cwd or an unsupported transport).
function normalizeDefinition(value: unknown): MCPServerConfig {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("Each MCP server must be a JSON object.");
  const definition = { ...value } as Record<string, unknown>;
  if (definition.type !== undefined) {
    if (!["stdio", "http", "streamable-http", "sse"].includes(String(definition.type))) throw new Error(`Unsupported MCP type: ${definition.type}`);
    if (definition.type !== "stdio") definition.transport = definition.type;
    delete definition.type;
  }
  const fields = ["command", "args", "url", "transport", "env", "headers", "oauth", "enabled", "tool_timeout_ms", "tool_overrides", "connector"];
  for (const key of Object.keys(definition)) if (!fields.includes(key)) throw new Error(`Unsupported MCP field: ${key}`);
  if (definition.args !== undefined && (!Array.isArray(definition.args) || definition.args.some(arg => typeof arg !== "string"))) throw new Error("MCP arguments must be a JSON array of strings.");
  return definition as MCPServerConfig;
}
