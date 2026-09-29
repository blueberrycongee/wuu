import { useState } from "react";
import type { ExecutionEnvironmentProfile, ExecutionEnvironmentSettings as EnvironmentSettings, RuntimeGeneralSettingsUpdate } from "../../../packages/protocol/src";
import { SettingsGroup, SettingsSection } from "./SettingsSection";
import { SettingsInputUnit, SettingsRow } from "./SettingsRow";
import { SelectMenu } from "./SelectMenu";
import { Pencil, Plus, Trash2 } from "./WuuIcons";
import { useI18n } from "./i18n";

const backends: ExecutionEnvironmentProfile["backend"][] = ["docker", "ssh", "singularity", "modal", "daytona", "vercel_sandbox", "command"];
// Product names stay as their vendors write them; only the generic adapter is translated.
const backendNames: Record<Exclude<ExecutionEnvironmentProfile["backend"], "command">, string> = {
  docker: "Docker", ssh: "SSH", singularity: "Singularity", modal: "Modal", daytona: "Daytona", vercel_sandbox: "Vercel Sandbox",
};
// The core treats an empty lifetime as this many seconds.
const DEFAULT_LIFETIME_SECONDS = "600";

export function ExecutionEnvironmentSettings({ value, disabled, onSave }: {
  value: EnvironmentSettings;
  disabled: boolean;
  onSave: (value: RuntimeGeneralSettingsUpdate) => Promise<void>;
}): JSX.Element {
  const { t } = useI18n();
  const [editing, setEditing] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [profile, setProfile] = useState<ExecutionEnvironmentProfile>({ backend: "docker", workspace: "/workspace" });
  const [command, setCommand] = useState("[]");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const locked = disabled || busy;
  const profiles = value.profiles ?? {};
  const backendLabel = (backend: ExecutionEnvironmentProfile["backend"]): string =>
    backend === "command" ? t("execution.backendCommand") : backendNames[backend];
  async function save(next: EnvironmentSettings, close = false): Promise<void> {
    setBusy(true); setError("");
    try { await onSave({ execution_environments: next }); if (close) setEditing(null); }
    catch (cause) { setError(cause instanceof Error ? cause.message : t("settings.saveFailed")); }
    finally { setBusy(false); }
  }
  function edit(id: string): void {
    const next = profiles[id] ?? { backend: "docker" as const, workspace: "/workspace" };
    setEditing(id); setName(id); setProfile({ ...next }); setCommand(JSON.stringify(next.command ?? [])); setError("");
  }
  function field(key: "image" | "host" | "identity_file" | "workspace" | "worker" | "known_hosts_file" | "python" | "host_workspace" | "user", label: string): JSX.Element {
    return <SettingsRow title={label}><input className="settings-input" aria-label={label} disabled={locked}
      value={profile[key] ?? ""} onChange={(event) => setProfile({ ...profile, [key]: event.target.value })} /></SettingsRow>;
  }
  function toggle(key: "shared" | "persistent" | "mount_read_only", label: string, hint: string): JSX.Element {
    return <SettingsRow title={label} description={hint}><button className="settings-switch" type="button" role="switch"
      aria-label={label} aria-checked={!!profile[key]} disabled={locked} onClick={() => setProfile({ ...profile, [key]: !profile[key] })}>
      <span className="settings-switch-thumb" aria-hidden="true" /></button></SettingsRow>;
  }
  function number(key: "port" | "cpus" | "memory_mb" | "lifetime_seconds", label: string, options: { min: number; max?: number; placeholder?: string; unit?: string }): JSX.Element {
    const input = <input className="settings-input settings-input-num" aria-label={label} type="number" min={options.min} max={options.max}
      placeholder={options.placeholder} disabled={locked} value={profile[key] || ""}
      onChange={(event) => setProfile({ ...profile, [key]: Number(event.target.value) || undefined })} />;
    return <SettingsRow title={label}>
      {options.unit ? <SettingsInputUnit unit={options.unit} placeholder={options.placeholder}>{input}</SettingsInputUnit> : input}
    </SettingsRow>;
  }
  function submit(): void {
    try {
      if (!/^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,63}$/.test(name) || name === "local") throw new Error(t("execution.invalidName"));
      if (editing !== name && profiles[name]) throw new Error(t("execution.duplicateName"));
      const args: unknown = JSON.parse(command);
      if (!Array.isArray(args) || !args.every((item) => typeof item === "string")) throw new Error(t("execution.invalidCommand"));
      const next = { ...profiles };
      if (editing && editing !== name) delete next[editing];
      next[name] = { ...profile, command: args.length ? args : undefined };
      void save({ default: value.default === editing ? name : value.default, profiles: next }, true);
    } catch (cause) { setError(cause instanceof Error ? cause.message : t("settings.saveFailed")); }
  }
  const errorLine = error ? <p className="settings-error" role="alert">{error}</p> : null;
  return <SettingsSection title={t("execution.title")} description={t("execution.description")} testID="settings-execution-environments"
    actions={<button className="settings-button settings-button-ghost" type="button" disabled={locked || editing !== null} onClick={() => edit("")}>
      <Plus className="icon" aria-hidden="true" />{t("execution.add")}</button>}>
    <SettingsGroup>
      <SettingsRow title={t("execution.default")} description={t("execution.defaultHint")}>
        <SelectMenu triggerClassName="settings-select-trigger" ariaLabel={t("execution.default")} value={value.default || "local"} disabled={locked}
          onChange={(selected) => void save({ ...value, default: selected })}
          options={[{ value: "local", label: t("execution.local") }, ...Object.keys(profiles).map((id) => ({ value: id, label: id }))]} />
      </SettingsRow>
      {Object.entries(profiles).map(([id, entry]) => <SettingsRow key={id} title={id} description={`${backendLabel(entry.backend)} · ${entry.workspace || "/workspace"}`}>
        <div className="settings-row-actions">
          <button className="settings-button settings-button-ghost settings-icon-button" type="button" disabled={locked}
            aria-label={t("execution.editNamed", { name: id })} title={t("execution.edit")} onClick={() => edit(id)}>
            <Pencil className="icon" aria-hidden="true" />
          </button>
          <button className="settings-button settings-button-ghost settings-icon-button" type="button" disabled={locked}
            aria-label={t("execution.removeNamed", { name: id })} title={t("execution.remove")} onClick={() => {
              const next = { ...profiles }; delete next[id];
              void save({ default: value.default === id ? "local" : value.default, profiles: next });
              if (editing === id) setEditing(null);
            }}>
            <Trash2 className="icon" aria-hidden="true" />
          </button>
        </div>
      </SettingsRow>)}
    </SettingsGroup>
    {editing !== null ? <form className="settings-form" aria-label={editing ? t("execution.editNamed", { name: editing }) : t("execution.add")}
      onSubmit={(event) => { event.preventDefault(); submit(); }}>
      <SettingsGroup>
        <SettingsRow title={t("execution.name")}><input className="settings-input" aria-label={t("execution.name")} value={name} disabled={locked} spellCheck={false} onChange={(event) => setName(event.target.value)} /></SettingsRow>
        <SettingsRow title={t("execution.backend")}><SelectMenu triggerClassName="settings-select-trigger" ariaLabel={t("execution.backend")}
          value={profile.backend} disabled={locked} options={backends.map((backend) => ({ value: backend, label: backendLabel(backend) }))}
          onChange={(backend) => { setProfile({ backend: backend as ExecutionEnvironmentProfile["backend"], workspace: "/workspace" }); setCommand("[]"); }} /></SettingsRow>
        {profile.backend === "docker" || profile.backend === "singularity" || profile.backend === "modal" || profile.backend === "daytona" || profile.backend === "vercel_sandbox" ? field("image", t("execution.image")) : null}
        {profile.backend === "ssh" ? <>{field("host", t("execution.host"))}{field("identity_file", t("execution.identity"))}{field("known_hosts_file", t("execution.knownHosts"))}
          {number("port", t("execution.port"), { min: 1, max: 65535 })}</> : null}
        {profile.backend === "docker" || profile.backend === "singularity" ? <>{field("host_workspace", t("execution.mount"))}{toggle("mount_read_only", t("execution.mountReadOnly"), t("execution.mountHint"))}</> : null}
        {profile.backend === "modal" || profile.backend === "daytona" || profile.backend === "vercel_sandbox" ? field("python", t("execution.python")) : null}
        {profile.backend === "docker" ? field("user", t("execution.user")) : null}
        {field("workspace", t("execution.workspace"))}
        {field("worker", t("execution.worker"))}
        {toggle("shared", t("execution.shared"), t("execution.sharedHint"))}
        {toggle("persistent", t("execution.persistent"), t("execution.persistentHint"))}
        {profile.backend !== "ssh" ? <SettingsRow title={t("execution.network")}><SelectMenu triggerClassName="settings-select-trigger" ariaLabel={t("execution.network")}
          value={profile.network || "enabled"} disabled={locked} onChange={(network) => setProfile({ ...profile, network: network as "enabled" | "none" })}
          options={[{ value: "enabled", label: t("execution.networkEnabled") }, { value: "none", label: t("execution.networkNone") }]} /></SettingsRow> : null}
        {profile.backend !== "ssh" ? number("cpus", t("execution.cpus"), { min: 0 }) : null}
        {profile.backend !== "ssh" && profile.backend !== "vercel_sandbox" ? number("memory_mb", t("execution.memoryMB"), { min: 0, unit: "MiB" }) : null}
        {number("lifetime_seconds", t("execution.lifetimeSeconds"), { min: 0, placeholder: DEFAULT_LIFETIME_SECONDS, unit: t("execution.unitSeconds") })}
        <SettingsRow title={t("execution.env")} description={t("execution.envHint")}><input className="settings-input" aria-label={t("execution.env")} disabled={locked} spellCheck={false}
          value={(profile.forward_env ?? []).join(", ")} onChange={(event) => setProfile({ ...profile, forward_env: event.target.value.split(",").map((item) => item.trim()).filter(Boolean) })} /></SettingsRow>
        {profile.backend === "command" ? <SettingsRow title={t("execution.command")} description={t("execution.commandHint")}>
          <input className="settings-input" aria-label={t("execution.command")} disabled={locked} spellCheck={false} value={command} onChange={(event) => setCommand(event.target.value)} />
        </SettingsRow> : null}
      </SettingsGroup>
      <div className="settings-form-actions">
        {errorLine}
        <button className="settings-button settings-button-ghost" type="button" disabled={locked} onClick={() => { setEditing(null); setError(""); }}>{t("execution.cancel")}</button>
        <button className="settings-button settings-button-primary" type="submit" disabled={locked}>{t("execution.save")}</button>
      </div>
    </form> : errorLine}
  </SettingsSection>;
}
