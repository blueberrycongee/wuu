import { hostSupports } from "./HostCapabilities";
import {
  AlertCircle,
  AlertTriangle,
  ChevronLeft,
  ChevronRight,
  Monitor,
  MoreHorizontal,
  PackagePlus,
  PanelLeft,
  PanelRight,
  RefreshCw,
  Settings,
  Sparkles,
  Terminal,
} from "./WuuIcons";
import { PluginBlocksIcon } from "./PluginBlocksIcon";
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
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
import { confirmAction } from "./ConfirmDialog";
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
  const [chosenTab, setActiveTab] = useState<"plugins" | "skills">("plugins");
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
  // A plugin keeps the group it was listed in until the catalog is entered
  // again, so its switch turns it on or off without moving the row away
  // from the pointer.
  const [listedPlacement, setListedPlacement] = useState<ReadonlyMap<string, boolean>>(() => new Map());
  useEffect(() => {
    const unlisted = plugins.filter((record) => !listedPlacement.has(record.id));
    if (unlisted.length === 0) return;
    setListedPlacement((current) => new Map([
      ...current,
      ...unlisted.map((record) => [record.id, pluginToggle(record).on] as const),
    ]));
  }, [listedPlacement, plugins]);

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
  // Notices and skill rows name related plugins the way their cards do.
  const pluginName = (id: string) =>
    plugins.find((record) => (record.provenance.plugin_id ?? record.id) === id)?.name ?? id;

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
    const confirmed = await confirmAction({
      title: t("skills.pluginRemoveTitle", { name: record.name }),
      message: t("skills.pluginRemoveConfirm"),
      confirmLabel: t("skills.pluginRemove"),
      tone: "danger",
    });
    if (!confirmed || mutationInFlight.current) {
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
        danger: true,
        disabled: Boolean(packageMutation),
        onSelect: () => removePluginPackage(record),
      });
    }
    return items;
  }

  const attentionPlugins = visiblePlugins.flatMap((record) => {
    const attention = pluginAttention(record, t, pluginName);
    return attention ? [{ record, attention }] : [];
  });
  const quietPlugins = visiblePlugins.filter((record) => !attentionPlugins.some((entry) => entry.record === record));
  const listedOn = (record: ExtensionInventoryRecord) => listedPlacement.get(record.id) ?? pluginToggle(record).on;
  const pluginGroups = [
    { key: "enabled", title: t("skills.groupEnabled"), records: quietPlugins.filter(listedOn) },
    { key: "disabled", title: t("skills.groupDisabled"), records: quietPlugins.filter((record) => !listedOn(record)) },
  ].filter((group) => group.records.length > 0);
  const activeTab = plugins.length === 0 ? "skills" : chosenTab;
  const pluginSkills = (record: ExtensionInventoryRecord) =>
    state.skills.filter((skill) => pluginSkillID(skill.source) === record.provenance.plugin_id);

  if (selectedPlugin) {
    return (
      <section className="settings-page skills-catalog plugin-page" aria-label={selectedPlugin.name} data-wuu-component="plugin-detail">
        <PluginDetailPage
          record={selectedPlugin}
          skills={pluginSkills(selectedPlugin)}
          pluginName={pluginName}
          packageMutation={packageMutation}
          reloading={state.loading}
          canUpdate={Boolean(onUpdateExtensionPackage)}
          canRemove={Boolean(onRemovePluginPackage)}
          onBack={() => {
            // Coming back is entering the catalog again: rows regroup by state.
            setListedPlacement(new Map());
            setSelectedPluginID("");
          }}
          onToggle={() => void updateExtensionPackage(selectedPlugin, pluginToggle(selectedPlugin).action)}
          onPrimaryAction={(action) => void updateExtensionPackage(selectedPlugin, action)}
          onReload={() => void refreshSkills()}
          onPreviewSkill={setPreviewSkill}
          onMoreActions={(button) => {
            const bounds = button.getBoundingClientRect();
            setPackageActionMenu({ record: selectedPlugin, x: bounds.right, y: bounds.bottom + 4 });
          }}
        />
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

      <div className="theme-segmented catalog-tabs" role="tablist" aria-label={t("skills.catalogLabel")}>
        {(["plugins", "skills"] as const).map((tab) => (
          <button
            key={tab}
            type="button"
            role="tab"
            className="catalog-tab"
            data-tab={tab}
            aria-selected={activeTab === tab}
            onClick={() => setActiveTab(tab)}
          >
            {tab === "plugins" ? t("skills.tabPlugins") : t("skills.tabSkills")}
            <span className="catalog-tab-count">{tab === "plugins" ? visiblePlugins.length : visibleSkills.length}</span>
          </button>
        ))}
      </div>

      {state.error ? <div className="skills-catalog-error">{state.error}</div> : null}

      {/* A plugin that needs a decision shows the reason instead of its
       * tagline; the decision is made on its page. */}
      {activeTab === "plugins" && attentionPlugins.length > 0 ? (
        <SettingsSection title={t("skills.groupAttention")}>
          <SettingsGroup>
            {attentionPlugins.map(({ record, attention }) => (
              <PluginRow
                key={record.id}
                record={record}
                attention={attention}
                onOpen={() => setSelectedPluginID(record.id)}
              />
            ))}
          </SettingsGroup>
        </SettingsSection>
      ) : null}

      {activeTab === "plugins" ? pluginGroups.map((group) => (
        <SettingsSection key={group.key} title={group.title}>
          <SettingsGroup>
            {group.records.map((record) => (
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
      )) : null}

      {activeTab === "skills" && officialSkills.length > 0 ? (
        <SettingsSection title={t("skills.sectionOfficial")}>
          <SkillsList skills={officialSkills} pluginName={pluginName} onPreview={setPreviewSkill} />
        </SettingsSection>
      ) : null}

      {activeTab === "skills" && personalSkills.length > 0 ? (
        <SettingsSection title={t("skills.sectionPersonal")}>
          <SkillsList skills={personalSkills} pluginName={pluginName} onPreview={setPreviewSkill} />
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

      {!state.loading && (activeTab === "plugins" ? visiblePlugins.length === 0 : visibleSkills.length === 0) ? (
        <p className="settings-group-empty">
          {filter.trim() ? t("skills.noMatches") : activeTab === "plugins" ? t("skills.noPlugins") : t("skills.empty")}
        </p>
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

// The one reason a plugin needs a look, most serious first; its page lists
// every notice with the details.
function pluginAttention(
  record: ExtensionInventoryRecord,
  t: ReturnType<typeof useI18n>["t"],
  pluginName: (id: string) => string,
): PluginAttention | undefined {
  if (record.runtime_state === "failed") {
    return { tone: "danger", label: t("skills.pluginStatusFailed") };
  }
  const missing = record.activation_issues?.find((issue) => issue.kind === "missing_requirement");
  if (missing) {
    return { tone: "danger", label: t("skills.pluginDependencyMissing", { plugin: pluginName(missing.related_plugin_id) }) };
  }
  if (record.pending_update) {
    return { tone: "warning", label: t("skills.pluginStatusUpdatePending") };
  }
  const approval = extensionPackageApproval(record);
  if (approval === "changed") return { tone: "warning", label: t("skills.pluginChangedNotice") };
  if (approval === "pending") return { tone: "warning", label: t("skills.pluginNeedsGrant") };
  const conflict = record.activation_issues?.find((issue) => issue.kind === "conflict");
  if (conflict) {
    return { tone: "warning", label: t("skills.pluginConflictWarning", { plugin: pluginName(conflict.related_plugin_id) }) };
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

// Mirrors the closed permission catalog owned by the runtime, in an order
// that keeps related grants together. Each label names its own object, so
// no category column is needed; unknown codes show as the raw code so the
// page never hides a grant.
const PLUGIN_PERMISSIONS: readonly { code: string; labelKey: TranslationKey }[] = [
  { code: "files.read", labelKey: "skills.permissionFilesRead" },
  { code: "files.write", labelKey: "skills.permissionFilesWrite" },
  { code: "network.connect", labelKey: "skills.permissionNetworkConnect" },
  { code: "session.read", labelKey: "skills.permissionSessionRead" },
  { code: "session.write", labelKey: "skills.permissionSessionWrite" },
  { code: "tools.define", labelKey: "skills.permissionToolsDefine" },
  { code: "tools.intercept", labelKey: "skills.permissionToolsIntercept" },
  { code: "commands.execute", labelKey: "skills.permissionCommandsExecute" },
  { code: "process.spawn", labelKey: "skills.permissionProcessSpawn" },
  { code: "shell.env", labelKey: "skills.permissionShellEnv" },
  { code: "accessibility.read", labelKey: "skills.permissionAccessibilityRead" },
  { code: "accessibility.control", labelKey: "skills.permissionAccessibilityControl" },
  { code: "screen.capture", labelKey: "skills.permissionScreenCapture" },
  { code: "app.activate", labelKey: "skills.permissionAppActivate" },
  { code: "input.synthesize", labelKey: "skills.permissionInputSynthesize" },
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

type PluginContribution = { key: string; icon: ReactNode; title: string; kind: string; onOpen?: () => void };

// What a plugin adds that people meet in Wuu: pages, commands, skills, and
// themes. Agent tools and host wiring are the plugin's own business.
function pluginContributions(
  record: ExtensionInventoryRecord,
  skills: readonly SkillSummary[],
  t: ReturnType<typeof useI18n>["t"],
  onPreviewSkill: (skill: SkillSummary) => void,
): PluginContribution[] {
  const contributions = record.contributions;
  const views = (entries: readonly { id: string; title: string }[] | undefined, kind: string, icon: ReactNode) =>
    (entries ?? []).map((entry) => ({ key: `${kind}:${entry.id}`, icon, title: entry.title, kind }));
  return [
    ...views(contributions?.navigation, t("skills.addsSidebarPage"), <PanelLeft className="icon" aria-hidden="true" />),
    ...views(contributions?.workspace_tools, t("skills.addsWorkspaceTool"), <PanelRight className="icon" aria-hidden="true" />),
    ...views(contributions?.settings_pages, t("skills.addsSettingsPage"), <Settings className="icon" aria-hidden="true" />),
    ...(contributions?.commands ?? []).map((command) => ({
      key: `command:${command.id}`,
      icon: <Terminal className="icon" aria-hidden="true" />,
      title: `/${command.id}`,
      kind: command.title || t("skills.addsCommand"),
    })),
    ...skills.map((skill) => ({
      key: `skill:${skill.name}`,
      icon: <Sparkles className="icon" aria-hidden="true" />,
      title: skill.name,
      kind: t("skills.addsSkill"),
      onOpen: () => onPreviewSkill(skill),
    })),
    ...(contributions?.themes ?? []).map((theme) => ({
      key: `theme:${theme.id}`,
      icon: <Monitor className="icon" aria-hidden="true" />,
      title: theme.name,
      kind: t("skills.addsTheme"),
    })),
  ];
}

// The plugin page answers what it does, whether it runs, and what it adds.
// Permissions appear only while someone is deciding whether to trust it,
// right under the notice that asks for that decision.
function PluginDetailPage({
  record,
  skills,
  pluginName,
  packageMutation,
  reloading,
  canUpdate,
  canRemove,
  onBack,
  onToggle,
  onPrimaryAction,
  onReload,
  onPreviewSkill,
  onMoreActions,
}: {
  record: ExtensionInventoryRecord;
  skills: readonly SkillSummary[];
  pluginName: (id: string) => string;
  packageMutation: string;
  reloading: boolean;
  canUpdate: boolean;
  canRemove: boolean;
  onBack: () => void;
  onToggle: () => void;
  onPrimaryAction: (action: ExtensionPackageAction) => void;
  onReload: () => void;
  onPreviewSkill: (skill: SkillSummary) => void;
  onMoreActions: (button: HTMLButtonElement) => void;
}): JSX.Element {
  const { t } = useI18n();
  const primaryAction = extensionPackagePrimaryAction(record);
  const secondaryAction = extensionPackageSecondaryAction(record);
  const approval = approvalNotice(record, t);
  const deciding = primaryAction === "grant" || primaryAction === "promote_update";
  const mutating = packageMutation.startsWith(`${record.id}:`);
  const toggle = pluginToggle(record);
  const grantUnavailable = deciding && !(primaryAction === "promote_update" ? record.pending_update?.fingerprint : record.fingerprint);
  const hasMoreActions = (canUpdate && Boolean(secondaryAction)) || (canRemove && isRemovableUserPlugin(record));
  const contributions = pluginContributions(record, skills, t, onPreviewSkill);
  const requestedPermissions = (primaryAction === "promote_update"
    ? record.pending_update?.requested_permissions
    : record.requested_permissions) ?? [];
  const permissionRows = [
    ...PLUGIN_PERMISSIONS
      .filter((permission) => requestedPermissions.includes(permission.code))
      .map((permission) => ({ code: permission.code, label: t(permission.labelKey) })),
    ...requestedPermissions
      .filter((code) => !PLUGIN_PERMISSIONS.some((permission) => permission.code === code))
      .map((code) => ({ code, label: code })),
  ];
  const notices: { key: string; tone: "warning" | "error"; text: string; detail?: string; action?: ReactNode }[] = [
    ...(approval ? [{ key: "approval", tone: "warning" as const, text: record.provenance.official ? approval : `${pluginSourceLabel(record, t)} · ${approval}` }] : []),
    ...(record.pending_update ? [{ key: "update", tone: "warning" as const, text: t("skills.pluginUpdateReady") }] : []),
    // A start failure says what happened in words, keeps the process's own
    // message as the detail, and offers the reload that starts it again.
    ...(record.runtime_state === "failed" ? [{
      key: "failed",
      tone: "error" as const,
      text: t("skills.pluginStatusFailed"),
      detail: record.last_error,
      action: (
        <button
          type="button"
          className="settings-button"
          disabled={reloading || Boolean(packageMutation)}
          onClick={onReload}
        >
          {t("skills.pluginRetryStart")}
        </button>
      ),
    }] : []),
    ...(record.activation_issues ?? []).map((issue) => ({
      key: `${issue.kind}:${issue.related_plugin_id}`,
      tone: issue.kind === "missing_requirement" ? "error" as const : "warning" as const,
      text: issue.kind === "missing_requirement"
        ? t("skills.pluginDependencyMissing", { plugin: pluginName(issue.related_plugin_id) })
        : t("skills.pluginConflictWarning", { plugin: pluginName(issue.related_plugin_id) }),
    })),
  ];

  return (
    <>
      <nav className="settings-page-back" aria-label={t("skills.tabPlugins")}>
        <button type="button" onClick={onBack} data-testid="plugin-page-back">
          <ChevronLeft className="icon" aria-hidden="true" />
          {t("skills.tabPlugins")}
        </button>
      </nav>

      <header className="plugin-page-header">
        <PluginMark record={record} className="plugin-page-mark" />
        <h1 className="plugin-page-title">{record.name}</h1>
        {record.description ? <p className="plugin-page-tagline">{record.description}</p> : null}
        <div className="settings-detail-actions">
          {hasMoreActions ? (
            <button
              type="button"
              className="settings-button settings-button-ghost settings-icon-button extension-package-more"
              aria-label={t("skills.pluginMoreActions", { name: record.name })}
              aria-haspopup="menu"
              disabled={Boolean(packageMutation) || !hostSupports("installPluginPackage")}
              onClick={(event) => onMoreActions(event.currentTarget)}
            >
              <MoreHorizontal className="icon" aria-hidden="true" />
            </button>
          ) : null}
          {!canUpdate ? null : deciding ? (
            <button
              type="button"
              className="settings-button settings-button-primary"
              disabled={Boolean(packageMutation) || grantUnavailable}
              onClick={() => onPrimaryAction(primaryAction)}
            >
              {mutating ? t("skills.pluginUpdating") : extensionPackageActionLabel(record, primaryAction, t)}
            </button>
          ) : (
            <button
              className="settings-switch"
              type="button"
              role="switch"
              aria-checked={toggle.on}
              aria-label={t(toggle.on ? "skills.pluginDisableNamed" : "skills.pluginEnableNamed", { name: record.name })}
              disabled={Boolean(packageMutation)}
              onClick={onToggle}
            >
              <span className="settings-switch-thumb" aria-hidden="true" />
            </button>
          )}
        </div>
      </header>

      {notices.length > 0 ? (
        <div className="plugin-detail-notices">
          {notices.map((notice) => (
            <div key={notice.key} className={`plugin-detail-notice is-${notice.tone}`} role={notice.tone === "error" ? "alert" : "status"}>
              {notice.tone === "error" ? <AlertCircle className="icon-sm" aria-hidden="true" /> : <AlertTriangle className="icon-sm" aria-hidden="true" />}
              <span className="plugin-detail-notice-text">{notice.text}</span>
              {notice.detail ? <span className="plugin-detail-notice-detail">{notice.detail}</span> : null}
              {notice.action}
            </div>
          ))}
        </div>
      ) : null}

      {deciding && permissionRows.length > 0 ? (
        <SettingsSection title={t("skills.pluginPermissions")}>
          <SettingsGroup>
            {permissionRows.map((permission) => (
              <div className="catalog-row plugin-permission-row" key={permission.code} title={permission.code}>
                <span className="catalog-row-title">{permission.label}</span>
              </div>
            ))}
          </SettingsGroup>
        </SettingsSection>
      ) : null}

      {record.long_description ? <p className="plugin-page-about">{record.long_description}</p> : null}

      {contributions.length > 0 ? (
        <SettingsSection title={t("skills.pluginAdds")}>
          <SettingsGroup>
            {contributions.map((item) => {
              const content = (
                <>
                  <span className="catalog-row-mark">{item.icon}</span>
                  <span className="catalog-row-title">{item.title}</span>
                  <span className="catalog-row-meta">{item.kind}</span>
                  {item.onOpen ? <ChevronRight className="icon settings-disclosure-chevron" aria-hidden="true" /> : null}
                </>
              );
              return item.onOpen ? (
                <button key={item.key} type="button" className="catalog-row" onClick={item.onOpen}>{content}</button>
              ) : (
                <div key={item.key} className="catalog-row plugin-contribution">{content}</div>
              );
            })}
          </SettingsGroup>
        </SettingsSection>
      ) : null}

      <PluginSettingsEditor plugin={record} title={t("skills.pluginSettingsLabel")} />

      {record.developer ? <p className="plugin-page-footnote">{t("skills.pluginDeveloper", { name: record.developer })}</p> : null}
    </>
  );
}

function PluginMark({ record, className = "catalog-row-mark" }: {
  record: ExtensionInventoryRecord;
  className?: string;
}): JSX.Element {
  return (
    <span className={className} aria-hidden="true">
      <PluginIcon icon={record.icon} pluginId={record.id} fingerprint={record.fingerprint ?? ""} />
    </span>
  );
}

// One plugin per row: its mark, the name over the tagline or the one reason
// it needs a look, and a chevron to its page. The whole row opens the page;
// a trusted plugin's switch sits over it in a column of its own and turns
// the plugin on or off in place.
function PluginRow({
  record,
  attention,
  toggleDisabled = false,
  onOpen,
  onToggle,
}: {
  record: ExtensionInventoryRecord;
  attention?: PluginAttention;
  toggleDisabled?: boolean;
  onOpen: () => void;
  /** Omitted when the catalog cannot change packages. */
  onToggle?: () => void;
}): JSX.Element {
  const { t } = useI18n();
  const toggle = pluginToggle(record);
  // A grant or change needs the fingerprint it approves.
  const toggleUnavailable = toggle.action === "grant" && !toggle.review && !record.fingerprint;
  const showSwitch = !attention && Boolean(onToggle);
  return (
    <div
      className="catalog-row plugin-row"
      data-plugin={record.provenance.plugin_id ?? record.id}
      data-on={toggle.on ? "" : undefined}
      data-switch={showSwitch ? "" : undefined}
    >
      <button
        className="catalog-row-open"
        type="button"
        aria-label={t("skills.pluginDetailLabel", { name: record.name })}
        onClick={onOpen}
      >
        <PluginMark record={record} />
        <span className="catalog-row-title">{record.name}</span>
        {attention ? (
          <span className="catalog-row-description plugin-attention-reason" data-tone={attention.tone}>
            {attention.tone === "danger" ? <AlertCircle className="icon-sm" aria-hidden="true" /> : <AlertTriangle className="icon-sm" aria-hidden="true" />}
            {attention.label}
          </span>
        ) : record.description ? (
          <span className="catalog-row-description" title={record.description}>{record.description}</span>
        ) : null}
        <ChevronRight className="icon settings-disclosure-chevron" aria-hidden="true" />
      </button>
      {showSwitch ? (
        <button
          className="settings-switch catalog-row-switch"
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

// Skill rows: a mark, the name over its first sentence, the owning plugin
// when there is one, and a chevron that opens the preview.
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
      <span className="catalog-row-title">{title}</span>
      {description ? <span className="catalog-row-description" title={description}>{description}</span> : null}
      {trailing}
      <ChevronRight className="icon settings-disclosure-chevron" aria-hidden="true" />
    </button>
  );
}

function SkillsList({
  skills,
  pluginName,
  onPreview,
}: {
  skills: SkillSummary[];
  pluginName: (id: string) => string;
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
              <span className="catalog-row-mark" aria-hidden="true">
                <CapabilityMark motif={skillCapability(skill.name)} />
              </span>
            }
            title={skill.name}
            description={catalogSkillDescription(skill)}
            trailing={pluginID ? (
              <span className="catalog-row-meta" title={t("skills.pluginSkillTitle")}>
                <PluginBlocksIcon className="icon-sm" aria-hidden="true" />
                {pluginName(pluginID)}
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
    // The mark, the name and close share one row like the Model services
    // dialogs; the file follows. Its own opening states what the catalog row
    // already showed, so the dialog repeats no description.
    <Modal
      ariaLabel={t("skills.previewLabel", { name: skill.name })}
      icon={<CapabilityMark motif={skillCapability(skill.name)} className="icon" />}
      title={skill.name}
      panelClassName="model-dialog skill-preview-dialog"
      onClose={onClose}
      footer={skill.user_invocable ? (
        <button className="settings-button settings-button-primary" type="button" onClick={onTry}>
          {t("skills.tryNow")}
        </button>
      ) : undefined}
    >
      <div className="skill-preview-body" data-scroll-fade="">
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
