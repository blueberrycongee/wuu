import { hostSupports } from "./HostCapabilities";
import {
  AlertCircle,
  AlertTriangle,
  ChevronRight,
  MoreHorizontal,
  PackagePlus,
  RefreshCw,
  Wrench,
} from "./WuuIcons";
import { PluginBlocksIcon } from "./PluginBlocksIcon";
import { useEffect, useId, useMemo, useRef, useState, type ReactNode } from "react";
import type {
  AppLocale,
  ExtensionInventoryRecord,
  ExtensionPackageAction,
  ExtensionPackageUpdateParams,
  PluginPackageInstallResult,
  PluginPackageRemoveResult,
  RuntimeContext,
  SkillSummary,
} from "../shared/protocol";
import { CatalogSearchField } from "./CatalogSearchField";
import { CapabilityMark, skillCapability } from "./CapabilityMark";
import { translateCurrent, useI18n } from "./i18n";
import type { TranslationKey } from "./i18n/resources/zh-CN";
import { Modal } from "./Modal";
import { PluginIcon } from "./PublicIcon";
import { PluginSettingsEditor } from "./PluginSettingsEditor";
import { RichContent } from "./RichContent";
import { SettingsGroup, SettingsSection } from "./SettingsSection";
import { ThreadContextMenu, type ThreadContextMenuItem } from "./ThreadContextMenu";
import { showErrorToast, showToast, toastErrorMessage } from "./Toast";

type LoadState = {
  loading: boolean;
  error: string;
  skills: SkillSummary[];
};

type SkillContentState = {
  loading: boolean;
  error: string;
  content: string;
};

const initialLoadState: LoadState = {
  loading: true,
  error: "",
  skills: [],
};

function showExtensionMutationError(error: unknown, fallback: string): void {
  // These admission errors currently cross IPC as text, without a typed code.
  // Keep their translation here, shared by refresh and package actions.
  const message = toastErrorMessage(error);
  const busyExecution = /^cannot .+ plugin packages while (?:a turn is running or background work remains on thread .+|another app-server is running a turn or background work)$/.test(message);
  const busyMutation = /^cannot .+ plugin packages while (?:another plugin change is running|another app-server is changing the plugin catalog)$/.test(message);
  if (busyExecution || busyMutation) {
    showToast({
      message: translateCurrent(busyExecution ? "skills.executionBusy" : "skills.changeBusy"),
      tone: "info",
      dedupeKey: busyExecution ? "extensions-execution-busy" : "extensions-change-busy",
    });
    return;
  }
  showErrorToast(error, fallback);
}

export function SkillsCatalog({
  activeContext,
  extensionInventory = [],
  onTrySkill,
  onRefreshCatalog,
  onUpdateExtensionPackage,
  onInstallPluginPackage,
  onRemovePluginPackage,
}: {
  activeContext?: RuntimeContext;
  extensionInventory?: ExtensionInventoryRecord[];
  onTrySkill?: (skill: SkillSummary) => void;
  onRefreshCatalog?: () => Promise<SkillSummary[] | undefined>;
  onUpdateExtensionPackage?: (
    update: ExtensionPackageUpdateParams,
  ) => Promise<void>;
  onInstallPluginPackage?: () => Promise<PluginPackageInstallResult | undefined>;
  onRemovePluginPackage?: (
    id: string,
  ) => Promise<PluginPackageRemoveResult | undefined>;
}): JSX.Element {
  const { locale, t } = useI18n();
  const [state, setState] = useState<LoadState>(initialLoadState);
  const [filter, setFilter] = useState("");
  const [previewSkill, setPreviewSkill] = useState<SkillSummary | null>(null);
  const [selectedPluginID, setSelectedPluginID] = useState("");
  const [packageMutation, setPackageMutation] = useState("");
  // State alone cannot stop a second click that lands before the re-render.
  const mutationInFlight = useRef(false);
  const [packageActionMenu, setPackageActionMenu] = useState<{
    record: ExtensionInventoryRecord;
    x: number;
    y: number;
  } | null>(null);
  const contextKey = activeContext ? runtimeContextKey(activeContext) : "";
  const contextKeyRef = useRef(contextKey);
  contextKeyRef.current = contextKey;

  useEffect(() => {
    let cancelled = false;
    void loadCatalog(cancelled);
    return () => {
      cancelled = true;
    };

    async function loadCatalog(alreadyCancelled: boolean): Promise<void> {
      if (alreadyCancelled) {
        return;
      }
      setState((current) => ({ ...current, loading: true, error: "" }));
      try {
        const [skillsResult] = await Promise.all([window.wuu.listSkills()]);
        if (cancelled) {
          return;
        }
        setState({
          loading: false,
          error: "",
          skills: skillsResult.skills,
        });
      } catch (error) {
        if (cancelled) {
          return;
        }
        setState({
          loading: false,
          error:
            error instanceof Error
              ? error.message
              : translateCurrent("skills.loadFailed"),
          skills: [],
        });
      }
    }
  }, [contextKey, locale]);

  const visibleSkills = useMemo(() => {
    const query = filter.trim().toLowerCase();
    const items = [...state.skills].sort((left, right) =>
      compareSkills(left, right, locale),
    );
    if (!query) {
      return items;
    }
    return items.filter((skill) =>
      [skill.name, skill.description, skill.when_to_use, skill.source, skill.argument_hint]
        .filter(Boolean)
        .some((value) => value?.toLowerCase().includes(query)),
    );
  }, [filter, locale, state.skills]);

  const officialSkills = useMemo(
    () => visibleSkills.filter((skill) => isBundledSkill(skill.source)),
    [visibleSkills],
  );
  const personalSkills = useMemo(
    () => visibleSkills.filter((skill) => !isBundledSkill(skill.source)),
    [visibleSkills],
  );

  const plugins = useMemo(
    () => extensionInventory.filter((record) => record.kind === "plugin"),
    [extensionInventory],
  );

  const visiblePlugins = useMemo(() => {
    const query = filter.trim().toLowerCase();
    const items = [...plugins].sort((left, right) =>
      left.name.localeCompare(right.name, locale),
    );
    if (!query) {
      return items;
    }
    return items.filter((record) =>
      [record.name, record.description, ...(record.requested_permissions ?? [])]
        .filter(Boolean)
        .some((value) => value?.toLowerCase().includes(query)),
    );
  }, [filter, locale, plugins]);
  const selectedPlugin = plugins.find((record) => record.id === selectedPluginID);

  async function refreshSkills(): Promise<void> {
    if (state.loading || packageMutation) return;
    const requestedContextKey = contextKey;
    setState((current) => ({ ...current, loading: true, error: "" }));
    try {
      const skills = onRefreshCatalog
        ? await onRefreshCatalog()
        : (await window.wuu.listSkills()).skills;
      if (!skills || contextKeyRef.current !== requestedContextKey) {
        return;
      }
      setState({
        loading: false,
        error: "",
        skills,
      });
    } catch (error) {
      if (contextKeyRef.current !== requestedContextKey) {
        return;
      }
      showExtensionMutationError(error, translateCurrent("skills.refreshFailed"));
    } finally {
      if (contextKeyRef.current === requestedContextKey) {
        setState((current) => ({ ...current, loading: false }));
      }
    }
  }

  async function updateExtensionPackage(record: ExtensionInventoryRecord, action: ExtensionPackageAction): Promise<void> {
    if (!onUpdateExtensionPackage || mutationInFlight.current) {
      return;
    }
    mutationInFlight.current = true;
    setPackageMutation(`${record.id}:${action}`);
    try {
      const fingerprint =
        action === "promote_update" || action === "reject_update"
          ? record.pending_update?.fingerprint
          : record.fingerprint;
      await onUpdateExtensionPackage({ id: record.id, fingerprint, action });
    } catch (error) {
      showExtensionMutationError(error, translateCurrent("skills.pluginUpdateFailed"));
    } finally {
      mutationInFlight.current = false;
      setPackageMutation("");
    }
  }

  // A plugin that asks for permissions shows them before it first runs;
  // every other switch applies in place.
  function togglePlugin(record: ExtensionInventoryRecord): void {
    const toggle = pluginToggle(record);
    if (toggle.review) {
      setSelectedPluginID(record.id);
      return;
    }
    void updateExtensionPackage(record, toggle.action);
  }

  async function installPluginPackage(): Promise<void> {
    if (!onInstallPluginPackage || mutationInFlight.current) {
      return;
    }
    const requestedContextKey = contextKey;
    mutationInFlight.current = true;
    setPackageMutation("install");
    try {
      const result = await onInstallPluginPackage();
      if (!result || contextKeyRef.current !== requestedContextKey) {
        return;
      }
      setState({ loading: false, error: "", skills: result.skills });
      const installedID = result.package?.id?.trim();
      if (installedID) {
        const record = (result.extension_inventory ?? []).find(
          (candidate) =>
            candidate.kind === "plugin" &&
            candidate.provenance?.plugin_id === installedID,
        );
        if (record) {
          setSelectedPluginID(record.id);
        }
      }
    } catch (error) {
      if (contextKeyRef.current === requestedContextKey) {
        showExtensionMutationError(error, translateCurrent("skills.pluginInstallFailed"));
      }
    } finally {
      mutationInFlight.current = false;
      setPackageMutation("");
    }
  }

  async function removePluginPackage(record: ExtensionInventoryRecord): Promise<void> {
    const pluginID = record.provenance.plugin_id;
    if (!onRemovePluginPackage || !pluginID || mutationInFlight.current) {
      return;
    }
    if (!window.confirm(t("skills.pluginRemoveConfirm", { name: record.name }))) {
      return;
    }
    const requestedContextKey = contextKey;
    mutationInFlight.current = true;
    setPackageMutation(`${record.id}:remove`);
    try {
      const result = await onRemovePluginPackage(pluginID);
      if (!result || contextKeyRef.current !== requestedContextKey) {
        return;
      }
      setState({ loading: false, error: "", skills: result.skills });
    } catch (error) {
      if (contextKeyRef.current === requestedContextKey) {
        showExtensionMutationError(error, translateCurrent("skills.pluginRemoveFailed"));
      }
    } finally {
      mutationInFlight.current = false;
      setPackageMutation("");
    }
  }

  function extensionPackageMenuItems(record: ExtensionInventoryRecord): ThreadContextMenuItem[] {
    const secondaryAction = extensionPackageSecondaryAction(record);
    const items: ThreadContextMenuItem[] = [];
    if (onUpdateExtensionPackage && secondaryAction) {
      items.push({
        label: extensionPackageActionLabel(record, secondaryAction, t),
        disabled: Boolean(packageMutation),
        onSelect: () => updateExtensionPackage(record, secondaryAction),
      });
    }
    if (onRemovePluginPackage && isRemovableUserPlugin(record)) {
      if (items.length > 0) items.push({ separator: true });
      items.push({
        label: packageMutation === `${record.id}:remove`
          ? t("skills.pluginRemoving")
          : t("skills.pluginRemove"),
        disabled: Boolean(packageMutation),
        onSelect: () => removePluginPackage(record),
      });
    }
    return items;
  }

  return (
    <section
      className="settings-page skills-catalog"
      aria-label={t("skills.catalogLabel")}
      data-wuu-component="skills-catalog"
    >
      {/* The titlebar already names the page, so it opens on its tools. */}
      <div className="catalog-toolbar">
        <CatalogSearchField
          value={filter}
          placeholder={t("skills.searchPlaceholder")}
          onValueChange={setFilter}
        />
        <button
          className="settings-button settings-button-ghost settings-icon-button catalog-refresh"
          type="button"
          aria-label={t("skills.refresh")}
          title={t("skills.refresh")}
          disabled={state.loading || Boolean(packageMutation)}
          aria-busy={state.loading}
          onClick={() => void refreshSkills()}
        >
          <RefreshCw className={`icon${state.loading ? " settings-spin" : ""}`} aria-hidden="true" />
        </button>
        <button
          className="settings-button"
          type="button"
          disabled={Boolean(packageMutation) || !hostSupports("installPluginPackage")}
          onClick={() => void installPluginPackage()}
        >
          <PackagePlus className="icon" aria-hidden="true" />
          <span>
            {packageMutation === "install"
              ? t("skills.pluginInstalling")
              : t("skills.pluginInstall")}
          </span>
        </button>
      </div>

      {state.error ? <div className="skills-catalog-error">{state.error}</div> : null}

      {/* Plugins lead: they switch on and off here and may need a decision,
        * while skills are only previewed. The page title names them. */}
      {visiblePlugins.length > 0 ? (
        <SettingsSection>
          <SettingsGroup>
            {visiblePlugins.map((record) => (
              <PluginRow
                key={record.id}
                record={record}
                toggleDisabled={Boolean(packageMutation)}
                onOpen={() => setSelectedPluginID(record.id)}
                onToggle={onUpdateExtensionPackage ? () => togglePlugin(record) : undefined}
              />
            ))}
          </SettingsGroup>
        </SettingsSection>
      ) : null}

      {officialSkills.length > 0 ? (
        <SettingsSection title={t("skills.sectionOfficial")}>
          <SkillsList skills={officialSkills} onPreview={setPreviewSkill} />
        </SettingsSection>
      ) : null}

      {personalSkills.length > 0 ? (
        <SettingsSection title={t("skills.sectionPersonal")}>
          <SkillsList skills={personalSkills} onPreview={setPreviewSkill} />
        </SettingsSection>
      ) : null}

      {previewSkill ? (
        <SkillPreviewDialog
          skill={previewSkill}
          onClose={() => setPreviewSkill(null)}
          onTry={() => {
            const skill = previewSkill;
            setPreviewSkill(null);
            onTrySkill?.(skill);
          }}
        />
      ) : null}

      {selectedPlugin ? (
        <PluginDetailDialog
          record={selectedPlugin}
          packageMutation={packageMutation}
          canUpdate={Boolean(onUpdateExtensionPackage)}
          canRemove={Boolean(onRemovePluginPackage)}
          onClose={() => setSelectedPluginID("")}
          onPrimaryAction={(action) => void updateExtensionPackage(selectedPlugin, action)}
          onMoreActions={(button) => {
            const bounds = button.getBoundingClientRect();
            setPackageActionMenu({
              record: selectedPlugin,
              x: bounds.right,
              y: bounds.bottom + 4,
            });
          }}
        />
      ) : null}

      {!state.loading && visibleSkills.length === 0 && visiblePlugins.length === 0 ? (
        filter.trim() ? (
          <p className="settings-group-empty">{t("skills.noMatches")}</p>
        ) : (
          <div className="skills-empty">
            <Wrench className="icon-xl" />
            <strong>{t("skills.empty")}</strong>
            <span>{t("skills.noneInRuntime")}</span>
          </div>
        )
      ) : null}

      {packageActionMenu ? (
        <ThreadContextMenu
          x={packageActionMenu.x}
          y={packageActionMenu.y}
          items={extensionPackageMenuItems(packageActionMenu.record)}
          onClose={() => setPackageActionMenu(null)}
        />
      ) : null}
    </section>
  );
}

function isRemovableUserPlugin(record: ExtensionInventoryRecord): boolean {
  return (
    record.provenance.official !== true &&
    (record.package_source === "user" ||
      (record.package_source === undefined && record.provenance.scope === "user")) &&
    Boolean(record.provenance.plugin_id)
  );
}

function extensionPackageApproval(record: ExtensionInventoryRecord): NonNullable<ExtensionInventoryRecord["approval_state"]> {
  if (record.approval_state) {
    return record.approval_state;
  }
  return record.provenance.official ? "official" : "pending";
}

function extensionPackagePrimaryAction(record: ExtensionInventoryRecord): ExtensionPackageAction {
  if (record.pending_update) {
    return "promote_update";
  }
  const approval = extensionPackageApproval(record);
  if (approval === "pending" || approval === "changed" || approval === "rejected") {
    return "grant";
  }
  return record.enabled === false ? "enable" : "disable";
}

function extensionPackageSecondaryAction(record: ExtensionInventoryRecord): ExtensionPackageAction | undefined {
  if (record.pending_update) {
    return "reject_update";
  }
  const approval = extensionPackageApproval(record);
  if (approval === "pending" || approval === "changed") {
    return "reject";
  }
  if (approval === "granted") {
    return "revoke";
  }
  return undefined;
}

// The switch shows whether a plugin runs. Turning on an unapproved plugin
// is its approval, reviewed first when it asks for permissions.
function pluginToggle(record: ExtensionInventoryRecord): { on: boolean; action: ExtensionPackageAction; review: boolean } {
  const approval = extensionPackageApproval(record);
  const trusted = approval === "official" || approval === "granted";
  if (trusted) {
    const on = record.enabled !== false;
    return { on, action: on ? "disable" : "enable", review: false };
  }
  return { on: false, action: "grant", review: (record.requested_permissions?.length ?? 0) > 0 };
}

type PluginAttention = { tone: "warning" | "danger"; label: string };

// One mark for everything that needs a look, most serious first; the detail
// dialog lists every notice.
function pluginAttention(record: ExtensionInventoryRecord, t: ReturnType<typeof useI18n>["t"]): PluginAttention | undefined {
  if (record.runtime_state === "failed") {
    return { tone: "danger", label: record.last_error?.trim() || t("skills.pluginStatusFailed") };
  }
  const missing = record.activation_issues?.find((issue) => issue.kind === "missing_requirement");
  if (missing) {
    return { tone: "danger", label: t("skills.pluginDependencyMissing", { plugin: missing.related_plugin_id }) };
  }
  if (record.pending_update) {
    return { tone: "warning", label: t("skills.pluginStatusUpdatePending") };
  }
  const approval = extensionPackageApproval(record);
  if (approval === "changed") return { tone: "warning", label: t("skills.pluginChangedNotice") };
  if (approval === "pending") return { tone: "warning", label: t("skills.pluginNeedsGrant") };
  const conflict = record.activation_issues?.find((issue) => issue.kind === "conflict");
  if (conflict) {
    return { tone: "warning", label: t("skills.pluginConflictWarning", { plugin: conflict.related_plugin_id }) };
  }
  return undefined;
}

function approvalNotice(record: ExtensionInventoryRecord, t: ReturnType<typeof useI18n>["t"]): string | undefined {
  switch (extensionPackageApproval(record)) {
    case "pending": return t("skills.pluginNeedsGrant");
    case "changed": return t("skills.pluginChangedNotice");
    case "rejected": return t("skills.pluginRejectedNotice");
    default: return undefined;
  }
}

function extensionPackageActionLabel(
  record: ExtensionInventoryRecord,
  action: ExtensionPackageAction,
  t: ReturnType<typeof useI18n>["t"],
): string {
  if (action === "grant") {
    return extensionPackageApproval(record) === "changed" ? t("skills.pluginReauthorize") : t("skills.pluginGrant");
  }
  if (action === "reject") return t("skills.pluginReject");
  if (action === "revoke") return t("skills.pluginRevoke");
  if (action === "enable") return t("skills.pluginEnable");
  if (action === "promote_update") return t("skills.pluginPromoteUpdate");
  if (action === "reject_update") return t("skills.pluginRejectUpdate");
  return t("skills.pluginDisable");
}

type PluginPermissionGroup = {
  key: string;
  labelKey: TranslationKey;
  permissions: { code: string; labelKey: TranslationKey }[];
};

// Mirrors the closed permission catalog owned by the runtime. Groups turn
// raw manifest codes into scannable, human-language rows; unknown codes fall
// back to a raw "other" group so the detail view never hides a grant.
const PLUGIN_PERMISSION_GROUPS: readonly PluginPermissionGroup[] = [
  {
    key: "files",
    labelKey: "skills.permissionGroupFiles",
    permissions: [
      { code: "files.read", labelKey: "skills.permissionFilesRead" },
      { code: "files.write", labelKey: "skills.permissionFilesWrite" },
    ],
  },
  {
    key: "network",
    labelKey: "skills.permissionGroupNetwork",
    permissions: [
      { code: "network.connect", labelKey: "skills.permissionNetworkConnect" },
    ],
  },
  {
    key: "session",
    labelKey: "skills.permissionGroupSession",
    permissions: [
      { code: "session.read", labelKey: "skills.permissionSessionRead" },
      { code: "session.write", labelKey: "skills.permissionSessionWrite" },
    ],
  },
  {
    key: "tools",
    labelKey: "skills.permissionGroupTools",
    permissions: [
      { code: "tools.define", labelKey: "skills.permissionToolsDefine" },
      { code: "tools.intercept", labelKey: "skills.permissionToolsIntercept" },
      { code: "commands.execute", labelKey: "skills.permissionCommandsExecute" },
    ],
  },
  {
    key: "system",
    labelKey: "skills.permissionGroupSystem",
    permissions: [
      { code: "process.spawn", labelKey: "skills.permissionProcessSpawn" },
      { code: "shell.env", labelKey: "skills.permissionShellEnv" },
    ],
  },
  {
    key: "desktop",
    labelKey: "skills.permissionGroupDesktop",
    permissions: [
      { code: "accessibility.read", labelKey: "skills.permissionAccessibilityRead" },
      { code: "accessibility.control", labelKey: "skills.permissionAccessibilityControl" },
      { code: "screen.capture", labelKey: "skills.permissionScreenCapture" },
      { code: "app.activate", labelKey: "skills.permissionAppActivate" },
      { code: "input.synthesize", labelKey: "skills.permissionInputSynthesize" },
    ],
  },
];

// Trust follows source identity, so the origin is the one provenance fact
// worth surfacing in the summary line — as readable language, not a raw enum.
function pluginSourceLabel(record: ExtensionInventoryRecord, t: ReturnType<typeof useI18n>["t"]): string {
  if (record.provenance.official) return t("skills.pluginSourceOfficial");
  switch (record.package_source) {
    case "user": return t("skills.pluginSourceUser");
    case "project": return t("skills.pluginSourceWorkspace");
    case "dev": return t("skills.pluginSourceDev");
    case "bundled": return t("skills.pluginSourceBundled");
    default: return t("skills.pluginSourceOther");
  }
}

function pluginGrantScopeLabel(scope: string | undefined, t: ReturnType<typeof useI18n>["t"]): string | undefined {
  switch (scope) {
    case "action": return t("skills.pluginGrantScopeAction");
    case "session": return t("skills.pluginGrantScopeSession");
    case "project": return t("skills.pluginGrantScopeWorkspace");
    case "user": return t("skills.pluginGrantScopeUser");
    default: return undefined;
  }
}

function PluginDetailDialog({
  record,
  packageMutation,
  canUpdate,
  canRemove,
  onClose,
  onPrimaryAction,
  onMoreActions,
}: {
  record: ExtensionInventoryRecord;
  packageMutation: string;
  canUpdate: boolean;
  canRemove: boolean;
  onClose: () => void;
  onPrimaryAction: (action: ExtensionPackageAction) => void;
  onMoreActions: (button: HTMLButtonElement) => void;
}): JSX.Element {
  const { t } = useI18n();
  const primaryAction = extensionPackagePrimaryAction(record);
  const secondaryAction = extensionPackageSecondaryAction(record);
  const grantScope = pluginGrantScopeLabel(record.grant_scope, t);
  const approval = approvalNotice(record, t);
  const mutating = packageMutation.startsWith(`${record.id}:`);
  const grantUnavailable =
    (primaryAction === "grant" || primaryAction === "promote_update") &&
    !(primaryAction === "promote_update"
      ? record.pending_update?.fingerprint
      : record.fingerprint);
  const hasMoreActions =
    (canUpdate && Boolean(secondaryAction)) ||
    (canRemove && isRemovableUserPlugin(record));
  const requestedPermissions = record.requested_permissions ?? [];
  const groupedPermissions = PLUGIN_PERMISSION_GROUPS
    .map((group) => ({
      group,
      present: group.permissions.filter((permission) => requestedPermissions.includes(permission.code)),
    }))
    .filter(({ present }) => present.length > 0);
  const unknownPermissions = requestedPermissions.filter(
    (permission) => !PLUGIN_PERMISSION_GROUPS.some((group) =>
      group.permissions.some((candidate) => candidate.code === permission),
    ),
  );

  return (
    <Modal
      ariaLabel={t("skills.pluginDetailLabel", { name: record.name })}
      icon={<PluginArtwork record={record} />}
      title={record.name}
      subtitle={record.description}
      panelClassName="skill-preview-dialog plugin-detail-dialog"
      closeDisabled={Boolean(packageMutation)}
      onClose={onClose}
      footer={canUpdate || hasMoreActions ? (
        <>
          {hasMoreActions ? (
            <button
              type="button"
              className="icon-button extension-package-more"
              aria-label={t("skills.pluginMoreActions", { name: record.name })}
              aria-haspopup="menu"
              disabled={Boolean(packageMutation) || !hostSupports("installPluginPackage")}
              onClick={(event) => onMoreActions(event.currentTarget)}
            >
              <MoreHorizontal className="icon" aria-hidden="true" />
            </button>
          ) : null}
          {canUpdate ? (
            <button
              type="button"
              // Turning a running plugin off is not the dialog's main path.
              className={primaryAction === "disable" ? "settings-button" : "settings-button settings-button-primary"}
              disabled={Boolean(packageMutation) || grantUnavailable}
              onClick={() => onPrimaryAction(primaryAction)}
            >
              {mutating
                ? t("skills.pluginUpdating")
                : extensionPackageActionLabel(record, primaryAction, t)}
            </button>
          ) : null}
        </>
      ) : undefined}
    >
      <div className="plugin-detail-body">
        {/* Trust follows the source, so it stays in view; the grant scope is a
          * detail of that decision and lives with the technical facts. */}
        <p className="plugin-detail-meta">{pluginSourceLabel(record, t)}</p>

        {approval || record.pending_update || (record.runtime_state === "failed" && record.last_error) || (record.activation_issues?.length ?? 0) > 0 ? (
          <div className="plugin-detail-notices">
            {approval ? (
              <div className="plugin-detail-notice is-warning" role="status">
                <AlertTriangle className="icon-sm" aria-hidden="true" />
                <span>{approval}</span>
              </div>
            ) : null}
            {record.pending_update ? (
              <div className="plugin-detail-notice is-warning" role="status">
                <AlertTriangle className="icon-sm" aria-hidden="true" />
                <span>{t("skills.pluginUpdateReady", { version: record.pending_update.version ?? "" })}</span>
              </div>
            ) : null}
            {record.runtime_state === "failed" && record.last_error ? (
              <div className="plugin-detail-notice is-error" role="alert">
                <AlertCircle className="icon-sm" aria-hidden="true" />
                <span>{record.last_error}</span>
              </div>
            ) : null}
            {record.activation_issues?.map((issue) => (
              <div
                className={`plugin-detail-notice ${issue.kind === "missing_requirement" ? "is-error" : "is-warning"}`}
                role="status"
                key={`${issue.kind}:${issue.related_plugin_id}`}
              >
                {issue.kind === "missing_requirement" ? (
                  <AlertCircle className="icon-sm" aria-hidden="true" />
                ) : (
                  <AlertTriangle className="icon-sm" aria-hidden="true" />
                )}
                <span>
                  {issue.kind === "missing_requirement"
                    ? t("skills.pluginDependencyMissing", { plugin: issue.related_plugin_id })
                    : t("skills.pluginConflictWarning", { plugin: issue.related_plugin_id })}
                </span>
              </div>
            ))}
          </div>
        ) : null}

        <section className="plugin-detail-section">
          <h3 className="plugin-detail-section-title">{t("skills.pluginPermissions")}</h3>
          {requestedPermissions.length > 0 ? (
            <div className="plugin-permission-groups">
              {groupedPermissions.map(({ group, present }) => (
                <div className="plugin-permission-group" key={group.key}>
                  <span className="plugin-permission-group-label">{t(group.labelKey)}</span>
                  <span className="plugin-permission-chips">
                    {present.map((permission) => (
                      <code
                        className="plugin-permission-chip"
                        key={permission.code}
                        title={permission.code}
                      >
                        {t(permission.labelKey)}
                      </code>
                    ))}
                  </span>
                </div>
              ))}
              {unknownPermissions.length > 0 ? (
                <div className="plugin-permission-group">
                  <span className="plugin-permission-group-label">{t("skills.permissionGroupOther")}</span>
                  <span className="plugin-permission-chips">
                    {unknownPermissions.map((permission) => (
                      <code className="plugin-permission-chip" key={permission}>{permission}</code>
                    ))}
                  </span>
                </div>
              ) : null}
            </div>
          ) : (
            <span className="plugin-detail-empty">{t("skills.pluginNoPermissions")}</span>
          )}
        </section>

        <PluginSettingsEditor plugin={record} title={t("skills.pluginSettingsLabel")} />

        {record.provenance.path || record.fingerprint || grantScope ? (
          <details className="plugin-detail-technical">
            <summary>{t("skills.pluginTechnicalInfo")}</summary>
            <dl className="plugin-detail-provenance">
              {grantScope ? (
                <div className="plugin-detail-provenance-row">
                  <dt>{t("skills.pluginGrantScope")}</dt>
                  <dd>{grantScope}</dd>
                </div>
              ) : null}
              {record.provenance.path ? (
                <div className="plugin-detail-provenance-row">
                  <dt>{t("skills.pluginActivePath")}</dt>
                  <dd><code>{record.provenance.path}</code></dd>
                </div>
              ) : null}
              {record.fingerprint ? (
                <div className="plugin-detail-provenance-row">
                  <dt>{t("skills.pluginFingerprint")}</dt>
                  <dd title={record.fingerprint}><code>{abbreviateFingerprint(record.fingerprint)}</code></dd>
                </div>
              ) : null}
            </dl>
          </details>
        ) : null}
      </div>
    </Modal>
  );
}

function abbreviateFingerprint(fingerprint: string): string {
  return fingerprint.length > 22
    ? `${fingerprint.slice(0, 12)}…${fingerprint.slice(-6)}`
    : fingerprint;
}

function PluginArtwork({ record }: { record: ExtensionInventoryRecord }): JSX.Element {
  return (
    <span className="skill-artwork skill-artwork-plugin-brand" aria-hidden="true">
      <PluginIcon icon={record.icon} pluginId={record.id} fingerprint={record.fingerprint ?? ""} />
    </span>
  );
}

// A plugin row opens its detail from the mark and name, and switches in
// place. One mark flags anything that needs a look; its label is the reason.
function PluginRow({
  record,
  toggleDisabled,
  onOpen,
  onToggle,
}: {
  record: ExtensionInventoryRecord;
  toggleDisabled: boolean;
  onOpen: () => void;
  /** Omitted when the catalog cannot change packages. */
  onToggle?: () => void;
}): JSX.Element {
  const { t } = useI18n();
  const attentionID = useId();
  const attention = pluginAttention(record, t);
  const toggle = pluginToggle(record);
  // A grant or change needs the fingerprint it approves.
  const toggleUnavailable = toggle.action === "grant" && !toggle.review && !record.fingerprint;
  return (
    <div className="catalog-row catalog-plugin-row">
      <button className="catalog-row-main" type="button" aria-label={t("skills.pluginDetailLabel", { name: record.name })}
        aria-describedby={attention ? attentionID : undefined} onClick={onOpen}>
        <PluginArtwork record={record} />
        <span className="catalog-row-copy">
          <span className="catalog-row-title">{record.name}</span>
          {record.description ? <span className="catalog-row-description">{record.description}</span> : null}
        </span>
        {attention ? (
          <span id={attentionID} className="catalog-row-attention" data-tone={attention.tone} role="img" aria-label={attention.label} title={attention.label}>
            {attention.tone === "danger" ? <AlertCircle className="icon" aria-hidden="true" /> : <AlertTriangle className="icon" aria-hidden="true" />}
          </span>
        ) : null}
      </button>
      {onToggle ? (
        <button
          className="settings-switch"
          type="button"
          role="switch"
          aria-checked={toggle.on}
          aria-label={t(toggle.on ? "skills.pluginDisableNamed" : "skills.pluginEnableNamed", { name: record.name })}
          disabled={toggleDisabled || toggleUnavailable}
          onClick={onToggle}
        >
          <span className="settings-switch-thumb" aria-hidden="true" />
        </button>
      ) : null}
    </div>
  );
}

// Skill rows: a mark, the name over a one-line description, the owning
// plugin when there is one, and a chevron that opens the preview.
function CatalogRow({
  label,
  artwork,
  title,
  description,
  trailing,
  onOpen,
}: {
  label: string;
  artwork: ReactNode;
  title: string;
  description?: string;
  trailing?: ReactNode;
  onOpen: () => void;
}): JSX.Element {
  return (
    <button className="catalog-row" type="button" aria-label={label} onClick={onOpen}>
      {artwork}
      <span className="catalog-row-copy">
        <span className="catalog-row-title">{title}</span>
        {description ? <span className="catalog-row-description">{description}</span> : null}
      </span>
      {trailing}
      <ChevronRight className="icon settings-disclosure-chevron" aria-hidden="true" />
    </button>
  );
}

function SkillsList({
  skills,
  onPreview,
}: {
  skills: SkillSummary[];
  onPreview: (skill: SkillSummary) => void;
}): JSX.Element {
  const { t } = useI18n();

  return (
    <SettingsGroup>
      {skills.map((skill) => {
        const pluginID = pluginSkillID(skill.source);
        return (
          <CatalogRow
            key={`${skill.source}:${skill.name}`}
            label={t("skills.previewSkill", { name: skill.name })}
            artwork={
              <span className="skill-artwork" aria-hidden="true">
                <CapabilityMark motif={skillCapability(skill.name)} />
              </span>
            }
            title={skill.name}
            description={catalogSkillDescription(skill)}
            trailing={pluginID ? (
              <span className="catalog-row-meta" title={t("skills.pluginSkillTitle")}>
                <PluginBlocksIcon className="icon-sm" aria-hidden="true" />
                {pluginID}
              </span>
            ) : undefined}
            onOpen={() => onPreview(skill)}
          />
        );
      })}
    </SettingsGroup>
  );
}

function catalogSkillDescription(skill: SkillSummary): string {
  const description = (skill.description || skill.when_to_use || "").trim();
  if (!description) {
    return "";
  }
  const firstSentence = description.match(/^.*?[。！？.!?](?=\s|$)/u)?.[0];
  return firstSentence?.trim() || description;
}

function SkillPreviewDialog({
  skill,
  onClose,
  onTry,
}: {
  skill: SkillSummary;
  onClose: () => void;
  onTry: () => void;
}): JSX.Element {
  const { t } = useI18n();
  const [contentState, setContentState] = useState<SkillContentState>({
    loading: true,
    error: "",
    content: "",
  });

  useEffect(() => {
    let cancelled = false;
    setContentState({ loading: true, error: "", content: "" });
    void window.wuu.readSkillContent({ name: skill.name, source: skill.source })
      .then((result) => {
        if (!cancelled) {
          setContentState({ loading: false, error: "", content: stripFrontmatter(result.content) });
        }
      })
      .catch((error) => {
        if (!cancelled) {
          setContentState({
            loading: false,
            error: error instanceof Error ? error.message : translateCurrent("skills.contentUnavailable"),
            content: fallbackSkillContent(skill),
          });
        }
      });
    return () => {
      cancelled = true;
    };
  }, [skill.name, skill.source]);

  return (
    <Modal
      ariaLabel={t("skills.previewLabel", { name: skill.name })}
      icon={
        <span className="skill-preview-icon-title">
          <CapabilityMark motif={skillCapability(skill.name)} className="icon" />
          <span>{t("skills.skillLabel")}</span>
        </span>
      }
      title={skill.name}
      subtitle={skill.description || skill.when_to_use || skill.trigger_condition}
      panelClassName="skill-preview-dialog"
      onClose={onClose}
      footer={skill.user_invocable ? (
        <button className="settings-button settings-button-primary" type="button" onClick={onTry}>
          {t("skills.tryNow")}
        </button>
      ) : undefined}
    >
      <div className="skill-preview-body">
        {contentState.loading ? (
          <p className="skill-preview-loading">{t("skills.loadingContent")}</p>
        ) : null}
        {contentState.error ? (
          <p className="skill-preview-error">{t("skills.contentFallback")}</p>
        ) : null}
        {contentState.content ? <RichContent text={contentState.content} /> : null}
      </div>
    </Modal>
  );
}

function stripFrontmatter(content: string): string {
  return content.replace(/^---\r?\n[\s\S]*?\r?\n---\r?\n?/, "").trim();
}

function fallbackSkillContent(skill: SkillSummary): string {
  return [
    skill.description,
    skill.when_to_use ? `## When to use\n\n${skill.when_to_use}` : "",
    skill.trigger_condition ? `## Trigger condition\n\n${skill.trigger_condition}` : "",
    skill.examples?.length ? `## Examples\n\n${skill.examples.map((item) => `- ${item}`).join("\n")}` : "",
    skill.verification_checklist?.length
      ? `## Verification checklist\n\n${skill.verification_checklist.map((item) => `- ${item}`).join("\n")}`
      : "",
  ].filter(Boolean).join("\n\n");
}

// Skills compiled into the Wuu binary carry source "bundled"; these are the
// first-party skills we ship and curate, so the catalog flags them.
function isBundledSkill(source: string): boolean {
  return source === "bundled";
}

// Skills discovered from a plugin's skills/ directory carry source
// "plugin:<id>" (plugin.SourceLabel); surface the owning plugin on the row.
function pluginSkillID(source: string): string {
  return source.startsWith("plugin:") ? source.slice("plugin:".length) : "";
}

function compareSkills(left: SkillSummary, right: SkillSummary, locale: AppLocale): number {
  const sourceDelta = sourceRank(left.source) - sourceRank(right.source);
  if (sourceDelta !== 0) {
    return sourceDelta;
  }
  return left.name.localeCompare(right.name, locale);
}

function sourceRank(source: string): number {
  switch (source) {
    case "bundled":
      return 0;
    case "project":
      return 1;
    case "user":
      return 2;
    default:
      return 3;
  }
}

function runtimeContextKey(context: RuntimeContext): string {
  return context.kind === "project" ? `project:${context.project_id}` : `no_project:${context.cwd}`;
}
