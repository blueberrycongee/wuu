import { Tooltip } from "./Tooltip";
import { TruncatedText } from "./TruncatedText";
import {
  AlertCircle,
  AlertTriangle,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  Monitor,
  MoreHorizontal,
  PackagePlus,
  Plug,
  Plus,
  PanelLeft,
  PanelRight,
  RefreshCw,
  Settings,
  Terminal,
} from "./WuuIcons";
import { PluginBlocksIcon } from "./PluginBlocksIcon";
import { SkillsIcon } from "./SkillsIcon";
import { createContext, useContext, useEffect, useMemo, useRef, useState, useSyncExternalStore, type ReactNode } from "react";
import type { AppLocale } from "../shared/protocol";
import type { ApplicationServiceV1 } from "../../../packages/plugin-sdk/src/ui";
import type { ApplicationCatalogV1, CatalogExtensionV1 as ExtensionInventoryRecord,
 CatalogPackageActionV1 as ExtensionPackageAction, CatalogSkillV1 as SkillSummary } from "../../../packages/plugin-sdk/src/ui-catalog";
import { composerSkillPrompt } from "./ComposerSlashCommands";

import { CatalogSearchField } from "./CatalogSearchField";
import { DefaultExtensionIcon } from "./DefaultExtensionIcon";
import { confirmAction } from "./ConfirmDialog";
import { extensionPackageApproval, pluginToggle } from "./extensionPackageState";
import { groupPluginFamilies, type PluginFamilyGroup } from "./pluginFamilies";
import { translateCurrent, useI18n } from "./i18n";
import type { TranslationKey } from "./i18n/resources/zh-CN";
import { Modal } from "./Modal";
import { PluginIcon } from "./PublicIcon";
import { SettingsControlsProvider } from "./SettingsControlsContext";
import { PluginSettingsEditor } from "./PluginSettingsEditor";
import { MCPServices, MCPServicesProvider, useMCPServices } from "./MCPServices";
import { MCPServerEditor } from "./MCPServerEditor";
import { RichContent } from "./RichContent";
import { SettingsGroup, SettingsPageHeader, SettingsSection } from "./SettingsSection";
import { ThreadContextMenu, type ThreadContextMenuItem } from "./ThreadContextMenu";
import { showErrorToast, showToast, toastErrorMessage } from "./Toast";
import { showExtensionMutationError } from "./extensionMutationError";
import { Switch } from "./ui/Switch";

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

export type SkillsCatalogSection = "plugins" | "skills";

const CatalogContext = createContext<ApplicationCatalogV1 | null>(null);
function useCatalog(): ApplicationCatalogV1 {
 const catalog = useContext(CatalogContext);
 if (!catalog) throw new Error("Catalog application binding is required");
 return catalog;
}

export function SkillsCatalog(props: SkillsCatalogProps): JSX.Element {
 const catalog = props.application.catalog.getSnapshot();
 const ownerKey = JSON.stringify([catalog.contextKey, catalog.inventory.filter(item => item.kind === "plugin")
   .map(item => [item.id, item.fingerprint, item.provenance, item.package_source, item.enabled])]);
 return <SettingsControlsProvider controls={props.application.settingsControls}><CatalogContext.Provider value={props.application.catalog}><MCPServicesProvider key={catalog.contextKey} contextKey={ownerKey} catalog={props.application.catalog}><SkillsCatalogView {...props} /></MCPServicesProvider></CatalogContext.Provider></SettingsControlsProvider>;
}
type SkillsCatalogProps = {
 application: ApplicationServiceV1 & {readonly catalog:ApplicationCatalogV1};
 active?:boolean; section?:SkillsCatalogSection; onSectionChange?:(section:SkillsCatalogSection)=>void;
 selectedPluginID?:string; onSelectPlugin?:(id:string)=>void; openPluginRequest?:{id:string;request:number};
};

function SkillsCatalogView({
  active = true,
  section,
  onSectionChange,
  selectedPluginID: selectedPluginIDProp,
  onSelectPlugin,
  application,
  openPluginRequest,
}: SkillsCatalogProps): JSX.Element {
  const catalog = application.catalog;
  const catalogSnapshot = useSyncExternalStore(listener => application.subscribe(listener), () => catalog.getSnapshot(), () => catalog.getSnapshot());
  const extensionInventory = catalogSnapshot.inventory;
  const capabilities = catalogSnapshot.capabilities;
  const { locale, t } = useI18n();
  const [state, setState] = useState<LoadState>(initialLoadState);
  const [filter, setFilter] = useState("");
  const [addMenu, setAddMenu] = useState<{ x: number; y: number } | null>(null);
  const [addingMCP, setAddingMCP] = useState(false);
  const { refresh: refreshMCP } = useMCPServices();
  const [previewSkill, setPreviewSkill] = useState<SkillSummary | null>(null);
  const [localSelectedPluginID, setLocalSelectedPluginID] = useState("");
  const selectedPluginID = selectedPluginIDProp ?? localSelectedPluginID;
  const setSelectedPluginID = onSelectPlugin ?? setLocalSelectedPluginID;
  const [chosenTab, setActiveTab] = useState<SkillsCatalogSection>("plugins");
  const previousSection = useRef(section);
  useEffect(() => {
    // A new shell can restore an open detail; only navigation clears it.
    if (previousSection.current === section) return;
    previousSection.current = section;
    setSelectedPluginID("");
    setPreviewSkill(null);
  }, [section, setSelectedPluginID]);
  useEffect(() => {
    if (!openPluginRequest) return;
    setSelectedPluginID(openPluginRequest.id);
    setPreviewSkill(null);
  }, [openPluginRequest]);
  const [packageMutation, setPackageMutation] = useState("");
  // State alone cannot stop a second click that lands before the re-render.
  const mutationInFlight = useRef(false);
  const [packageActionMenu, setPackageActionMenu] = useState<{
    record: ExtensionInventoryRecord;
    x: number;
    y: number;
  } | null>(null);
  useEffect(() => {
    if (active) return;
    setPreviewSkill(null);
    setPackageActionMenu(null);
    setAddMenu(null);
    setAddingMCP(false);
  }, [active]);
  const contextKey = catalogSnapshot.contextKey;
  const contextKeyRef = useRef(contextKey);
  contextKeyRef.current = contextKey;
  useEffect(() => { setAddMenu(null); setAddingMCP(false); }, [contextKey]);

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
        const skillsResult = await catalog.listSkills();
        if (cancelled) {
          return;
        }
        setState({
          loading: false,
          error: "",
          skills: [...skillsResult],
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
  }, [catalog, contextKey, locale]);

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

  const reportedDiagnostics = useRef<{ contextKey: string; messages: Set<string> }>({ contextKey, messages: new Set() });
  useEffect(() => {
    if (!active || packageMutation || state.loading) return;
    if (reportedDiagnostics.current.contextKey !== contextKey) {
      reportedDiagnostics.current = { contextKey, messages: new Set() };
    }
    const current = new Set<string>();
    const nameOf = (id: string) => plugins.find(record => (record.provenance.plugin_id ?? record.id) === id)?.name ?? id;
    for (const record of plugins) {
      if (!pluginToggle(record).on) continue;
      const diagnostics = [
        ...(record.runtime_state === "failed" ? [{ tone: "error" as const, text: toastErrorMessage(record.last_error, t("skills.pluginStatusFailed")) }] : []),
        ...(record.activation_issues ?? []).map(issue => pluginActivationNotice(issue, t, nameOf)),
      ];
      for (const diagnostic of diagnostics) {
        const key = `${record.id}:${diagnostic.tone}:${diagnostic.text}`;
        current.add(key);
        if (reportedDiagnostics.current.messages.has(key)) continue;
        showToast({
          message: `${record.name}: ${diagnostic.text}`,
          tone: diagnostic.tone === "error" ? "error" : "info",
          // Mutation errors use this same key, so a failed action and its
          // resulting inventory snapshot do not produce duplicate notices.
          dedupeKey: `${diagnostic.tone === "error" ? "error" : "info"}:${diagnostic.text}`,
        });
      }
    }
    reportedDiagnostics.current.messages = current;
  }, [active, contextKey, plugins, packageMutation, state.loading, t]);

  const pluginGroups = useMemo(() => groupPluginFamilies(plugins, locale), [plugins, locale]);
  const visiblePlugins = useMemo(() => {
    const query = filter.trim().toLowerCase();
    if (!query) return pluginGroups;
    return pluginGroups.filter(group =>
      [group.name, group.description, ...group.members.flatMap(record =>
        [record.name, record.description, ...(record.requested_permissions ?? [])])]
        .some(value => value?.toLowerCase().includes(query)),
    );
  }, [filter, pluginGroups]);
  const selectedPlugin = plugins.find((record) => record.id === selectedPluginID);
  const selectedFamily = pluginGroups.find(group => group.family && group.id === selectedPluginID);
  const parentFamily = selectedPlugin?.family
    ? pluginGroups.find(group => group.members.some(record => record.id === selectedPlugin.id))
    : undefined;
  // Notices and skill rows name related plugins the way their cards do.
  const pluginName = (id: string) =>
    plugins.find((record) => (record.provenance.plugin_id ?? record.id) === id)?.name ?? id;

  async function trySkill(skill: SkillSummary): Promise<void> {
    if (!catalog.getSnapshot().contextKey) return;
    const { activeTabId, navigationRevision } = application.getSnapshot().tabs;
    try {
      await application.invoke("draft.start-with-content", {
        text: composerSkillPrompt(skill),
        sourceTabId: activeTabId,
        navigationRevision,
      });
    } catch (error) {
      showErrorToast(error, translateCurrent("skills.loadFailed"));
    }
  }

  async function refreshSkills(): Promise<void> {
    if (state.loading || packageMutation) return;
    const requestedContextKey = contextKey;
    setState((current) => ({ ...current, loading: true, error: "" }));
    try {
      const skills = await catalog.refresh();
      if (!skills || contextKeyRef.current !== requestedContextKey) {
        return;
      }
      setState({
        loading: false,
        error: "",
        skills: [...skills],
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
    if (!capabilities.update || mutationInFlight.current) {
      return;
    }
    mutationInFlight.current = true;
    setPackageMutation(`${record.id}:${action}`);
    try {
      const fingerprint =
        action === "promote_update" || action === "reject_update"
          ? record.pending_update?.fingerprint
          : record.fingerprint;
      await catalog.update({ id: record.id, fingerprint, action });
    } catch (error) {
      showExtensionMutationError(error, translateCurrent("skills.pluginUpdateFailed"));
    } finally {
      mutationInFlight.current = false;
      setPackageMutation("");
    }
  }

  // A plugin that asks for permissions shows them before it first runs;
  // other enablement actions apply in place.
  function togglePlugin(record: ExtensionInventoryRecord): void {
    const toggle = pluginToggle(record);
    if (toggle.review) {
      setSelectedPluginID(record.id);
      return;
    }
    void updateExtensionPackage(record, toggle.action);
  }

  async function installPluginPackage(): Promise<void> {
    if (!capabilities.install || mutationInFlight.current) {
      return;
    }
    const requestedContextKey = contextKey;
    mutationInFlight.current = true;
    setPackageMutation("install");
    try {
      const result = await catalog.install();
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
    if (!capabilities.remove || !pluginID || mutationInFlight.current) {
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
      const result = await catalog.remove(pluginID);
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
    if (capabilities.update && secondaryAction) {
      items.push({
        label: extensionPackageActionLabel(record, secondaryAction, t),
        disabled: Boolean(packageMutation),
        onSelect: () => updateExtensionPackage(record, secondaryAction),
      });
    }
    if (capabilities.remove && isRemovableUserPlugin(record)) {
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

  const activeTab = section ?? (plugins.length === 0 ? "skills" : chosenTab);
  const pluginSkills = (record: ExtensionInventoryRecord) =>
    state.skills.filter((skill) => pluginSkillID(skill.source) === record.provenance.plugin_id);

  if (selectedPlugin) {
    return (
      <section className="settings-page skills-catalog plugin-page" aria-label={selectedPlugin.name} data-wuu-component="plugin-detail">
        <PluginDetailPage
          record={selectedPlugin}
          skills={pluginSkills(selectedPlugin)}
          packageMutation={packageMutation}
          reloading={state.loading}
          canUpdate={capabilities.update}
          canRemove={capabilities.remove}
          canInstall={capabilities.install}
          backLabel={parentFamily?.name}
          onBack={() => {
            setSelectedPluginID(parentFamily?.id ?? "");
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
              void trySkill(skill);
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

  if (selectedFamily) {
    return (
      <section className="settings-page skills-catalog plugin-page" aria-label={selectedFamily.name} data-wuu-component="plugin-family-detail">
        <nav className="settings-page-back" aria-label={t("skills.tabPlugins")}>
          <button type="button" onClick={() => setSelectedPluginID("")}>
            <ChevronLeft className="icon" aria-hidden="true" />{t("skills.tabPlugins")}
          </button>
        </nav>
        <SettingsPageHeader title={selectedFamily.name} description={selectedFamily.description} />
        <SettingsSection title={t("skills.familyComponents")} description={t("skills.familyStatus", {
          enabled: selectedFamily.members.filter(record => pluginToggle(record).on).length,
          count: selectedFamily.members.length,
        })}>
          <div className="plugin-catalog-grid">
            {selectedFamily.members.map(record => (
              <PluginRow key={record.id} record={record}
                toggleDisabled={Boolean(packageMutation)} toggleBusy={packageMutation.startsWith(`${record.id}:`)}
                onOpen={() => setSelectedPluginID(record.id)}
                onToggle={capabilities.update ? () => togglePlugin(record) : undefined} />
            ))}
          </div>
        </SettingsSection>
      </section>
    );
  }

  return (
    <section
      className={`settings-page skills-catalog${activeTab === "plugins" ? " plugin-catalog" : ""}`}
      aria-label={t("skills.catalogLabel")}
      data-wuu-component="skills-catalog"
    >
      <SettingsPageHeader title={t(activeTab === "plugins" ? "skills.tabPlugins" : "skills.tabSkills")} />
      <div className="catalog-toolbar">
        <CatalogSearchField
          value={filter}
          placeholder={t(activeTab === "plugins" ? "skills.searchPlugins" : "skills.searchSkills")}
          onValueChange={setFilter}
        />
        <Tooltip asChild content={t("skills.refresh")}><button
          className="settings-button settings-button-ghost settings-icon-button catalog-refresh"
          type="button"
          aria-label={t("skills.refresh")}
          disabled={state.loading || Boolean(packageMutation)}
          data-mutation-blocked={(Boolean(packageMutation) && !state.loading) || undefined}
          aria-busy={state.loading}
          onClick={() => void refreshSkills()}
        >
          <RefreshCw className={`icon${state.loading ? " settings-spin" : ""}`} aria-hidden="true" />
        </button></Tooltip>
        <button
          className="settings-button settings-button-primary catalog-add"
          type="button"
          disabled={Boolean(packageMutation)}
          aria-haspopup="menu"
          aria-expanded={Boolean(addMenu)}
          onClick={event => {
            const bounds = event.currentTarget.getBoundingClientRect();
            setAddMenu({ x: bounds.right, y: bounds.bottom + 4 });
          }}
        >
          <span>{packageMutation === "install" ? t("skills.pluginInstalling") : t("skills.add")}</span>
          <ChevronDown className="icon" aria-hidden="true" />
        </button>
        {addMenu ? <ThreadContextMenu x={addMenu.x} y={addMenu.y} onClose={() => setAddMenu(null)} items={[
          { label: t("skills.pluginInstall"), icon: <PackagePlus className="icon" />, disabled: !capabilities.install, onSelect: () => void installPluginPackage() },
          { label: t("skills.addCustomMCP"), icon: <Plug className="icon" />, onSelect: () => setAddingMCP(true) },
        ]} /> : null}
        {addingMCP ? <MCPServerEditor name="" onClose={() => setAddingMCP(false)} onSaved={() => { setAddingMCP(false); void refreshMCP(); }} /> : null}
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
            onClick={() => (onSectionChange ?? setActiveTab)(tab)}
          >
            {tab === "plugins" ? t("skills.tabPlugins") : t("skills.tabSkills")}
            <span className="catalog-tab-count">{tab === "plugins" ? visiblePlugins.length : visibleSkills.length}</span>
          </button>
        ))}
      </div>

      {/* Skills come from the core and plugins from the inventory, so a failed
       * skill load is reported on the Skills tab, with the reload that
       * recovers, in place of an empty list. */}
      {activeTab === "skills" && state.error ? (
        <div className="plugin-detail-notice is-error skills-catalog-error" role="alert">
          <AlertCircle className="icon-sm" aria-hidden="true" />
          <span className="plugin-detail-notice-text">{t("skills.loadFailed")}</span>
          {state.error !== t("skills.loadFailed") ? <span className="plugin-detail-notice-detail">{state.error}</span> : null}
          <button type="button" className="settings-button" disabled={state.loading} onClick={() => void refreshSkills()}>
            {t("skills.pluginRetryStart")}
          </button>
        </div>
      ) : null}

      {activeTab === "plugins" ? <div className="plugin-catalog-grid">
        <MCPServices filter={filter} catalogView />
        {visiblePlugins.map(group => {
          if (group.family) return <PluginFamilyRow key={group.id} group={group}
            onOpen={() => setSelectedPluginID(group.id)} />;
          const record = group.members[0];
          return <PluginRow
            key={record.id}
            record={record}
            toggleDisabled={Boolean(packageMutation)}
            toggleBusy={packageMutation.startsWith(`${record.id}:`)}
            onOpen={() => setSelectedPluginID(record.id)}
            onToggle={capabilities.update ? () => togglePlugin(record) : undefined}
          />;
        })}
      </div> : null}

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
            void trySkill(skill);
          }}
        />
      ) : null}

      {!state.loading && (activeTab === "plugins" ? visiblePlugins.length === 0 : visibleSkills.length === 0 && !state.error) ? (
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

function pluginActivationNotice(
  issue: NonNullable<ExtensionInventoryRecord["activation_issues"]>[number],
  t: ReturnType<typeof useI18n>["t"],
  pluginName: (id: string) => string,
): { tone: "error" | "warning"; text: string } {
  const labels: Record<typeof issue.kind, TranslationKey> = {
    missing_requirement: "skills.pluginDependencyMissing",
    version_mismatch: "skills.pluginDependencyVersionMismatch",
    conflict: "skills.pluginConflictWarning",
    optional_missing: "skills.pluginOptionalDependencyMissing",
    optional_version_mismatch: "skills.pluginOptionalDependencyVersionMismatch",
    optional_cycle: "skills.pluginOptionalDependencyCycle",
  };
  return {
    tone: issue.kind === "missing_requirement" || issue.kind === "version_mismatch" ? "error" : "warning",
    text: t(labels[issue.kind], { plugin: pluginName(issue.related_plugin_id) }),
  };
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
      icon: <SkillsIcon className="icon" />,
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

// Keep identity and enablement together, then put configuration before reference
// content. Approval notices and permissions precede both when a decision is due.
function PluginDetailPage({
  record,
  skills,
  packageMutation,
  reloading,
  canUpdate,
  canRemove,
  canInstall,
  onBack,
  onToggle,
  onPrimaryAction,
  onReload,
  onPreviewSkill,
  onMoreActions,
  backLabel,
}: {
  backLabel?: string;
  record: ExtensionInventoryRecord;
  skills: readonly SkillSummary[];
  packageMutation: string;
  reloading: boolean;
  canUpdate: boolean;
  canRemove: boolean;
  canInstall: boolean;
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
  const notices = [
    ...(approval ? [{ key: "approval", text: record.provenance.official ? approval : `${pluginSourceLabel(record, t)} · ${approval}` }] : []),
    ...(record.pending_update ? [{ key: "update", text: t("skills.pluginUpdateReady") }] : []),
  ];

  return (
    <>
      <nav className="settings-page-back" aria-label={t("skills.tabPlugins")}>
        <button type="button" onClick={onBack} data-testid="plugin-page-back">
          <ChevronLeft className="icon" aria-hidden="true" />
          {backLabel ?? t("skills.tabPlugins")}
        </button>
      </nav>

      <header className="plugin-page-header">
        <PluginMark record={record} className="plugin-page-mark" />
        <div className="plugin-page-identity">
          <h1 className="plugin-page-title">{record.name}</h1>
          <div className="plugin-page-meta">
            {record.developer ? <span>{t("skills.pluginDeveloper", { name: record.developer })}</span> : null}
            <span>{pluginSourceLabel(record, t)}</span>
          </div>
        </div>
        {record.description ? <p className="plugin-page-tagline">{record.description}</p> : null}
      </header>

      <div className="plugin-page-controls">
        <span className="plugin-page-state" aria-live="polite">
          {mutating ? t("skills.pluginUpdating") : t(toggle.on ? "skills.groupEnabled" : "skills.groupDisabled")}
        </span>
        <div className="plugin-page-actions">
          {record.runtime_state === "failed" ? (
            <button type="button" className="settings-button" disabled={reloading || Boolean(packageMutation)} onClick={onReload}>
              {t("skills.pluginRetryStart")}
            </button>
          ) : null}
          {hasMoreActions ? (
            <button
              type="button"
              className="settings-button settings-button-ghost settings-icon-button extension-package-more"
              aria-label={t("skills.pluginMoreActions", { name: record.name })}
              aria-haspopup="menu"
              disabled={Boolean(packageMutation) || !canInstall}
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
            <Switch
              checked={toggle.on}
              aria-label={t(toggle.on ? "skills.pluginDisableNamed" : "skills.pluginEnableNamed", { name: record.name })}
              disabled={Boolean(packageMutation)}
              onClick={onToggle}
            />
          )}
        </div>
      </div>

      {notices.length > 0 ? (
        <div className="plugin-detail-notices">
          {notices.map((notice) => (
            <div key={notice.key} className="plugin-detail-notice is-warning" role="status">
              <AlertTriangle className="icon-sm" aria-hidden="true" />
              <span className="plugin-detail-notice-text">{notice.text}</span>
            </div>
          ))}
        </div>
      ) : null}

      {deciding && permissionRows.length > 0 ? (
        <SettingsSection title={t("skills.pluginPermissions")}>
          <SettingsGroup>
            {permissionRows.map((permission) => (
              <Tooltip asChild content={permission.code} key={permission.code}><div className="catalog-row plugin-permission-row">
                <span className="catalog-row-title">{permission.label}</span>
              </div></Tooltip>
            ))}
          </SettingsGroup>
        </SettingsSection>
      ) : null}

      <PluginSettingsEditor plugin={record} title={t("skills.pluginSettingsLabel")} />
      <MCPServices pluginID={record.provenance.plugin_id ?? record.id} enabled={toggle.on && !deciding} />

      {record.long_description ? (
        <SettingsSection title={t("skills.pluginAbout")}>
          <p className="plugin-page-about">{record.long_description}</p>
        </SettingsSection>
      ) : null}

      {contributions.length > 0 ? (
        <SettingsSection title={t("skills.pluginAdds")}>
          <ul className="plugin-contributions">
            {contributions.map((item) => {
              const content = (
                <>
                  <span className="catalog-row-mark">{item.icon}</span>
                  <span className="plugin-contribution-copy">
                    <span className="catalog-row-title">{item.title}</span>
                    <span className="plugin-contribution-kind">{item.kind}</span>
                  </span>
                  {item.onOpen ? <ChevronRight className="icon settings-disclosure-chevron" aria-hidden="true" /> : null}
                </>
              );
              return (
                <li key={item.key}>
                  {item.onOpen ? (
                    <button type="button" className="plugin-contribution" onClick={item.onOpen}>{content}</button>
                  ) : (
                    <div className="plugin-contribution">{content}</div>
                  )}
                </li>
              );
            })}
          </ul>
        </SettingsSection>
      ) : null}
    </>
  );
}

function PluginMark({ record, className = "catalog-row-mark" }: {
  record: ExtensionInventoryRecord;
  className?: string;
}): JSX.Element {
  const catalog = useCatalog();
  const loadIcon = useMemo(() => (id:string, fingerprint:string, path:string) => catalog.loadIcon(id,fingerprint,path), [catalog]);
  return (
    <span className={className} aria-hidden="true">
      <PluginIcon loadIcon={loadIcon} icon={record.icon} pluginId={record.id} fingerprint={record.fingerprint ?? ""} />
    </span>
  );
}

function PluginFamilyRow({ group, onOpen }: {
  group: PluginFamilyGroup<ExtensionInventoryRecord>;
  onOpen: () => void;
}): JSX.Element {
  const { t } = useI18n();
  const on = group.members.some(record => pluginToggle(record).on);
  const [menu, setMenu] = useState<{ x: number; y: number } | null>(null);
  return (
    <div className="catalog-row plugin-row plugin-family-row" data-plugin-family={group.id}>
      <button className="catalog-row-open" type="button" onClick={onOpen}
        aria-label={t("skills.pluginDetailLabel", { name: group.name })}>
        <span className="catalog-row-mark"><PluginBlocksIcon aria-hidden="true" /></span>
        <TruncatedText className="catalog-row-title" text={group.name} />
        {group.description ? <TruncatedText as="span" className="catalog-row-description" text={group.description} /> : null}
      </button>
      <button className="settings-button settings-button-ghost settings-icon-button catalog-row-action" type="button"
        aria-label={t(on ? "provider.moreActions" : "skills.pluginEnableNamed", { name: group.name })}
        aria-haspopup={on ? "menu" : undefined} aria-expanded={on ? Boolean(menu) : undefined}
        onClick={event => {
          if (!on) { onOpen(); return; }
          const bounds = event.currentTarget.getBoundingClientRect();
          setMenu({ x: bounds.right, y: bounds.bottom });
        }}>
        {on ? <MoreHorizontal className="icon" aria-hidden="true" /> : <Plus className="icon" aria-hidden="true" />}
      </button>
      {menu ? <ThreadContextMenu x={menu.x} y={menu.y} onClose={() => setMenu(null)} items={[
        { label: t("skills.pluginDetailLabel", { name: group.name }), onSelect: onOpen },
      ]} /> : null}
    </div>
  );
}

// Keep each entry in place when its state changes. The row opens details;
// inactive plugins offer enablement and active plugins expose a menu.
function PluginRow({
  record,
  toggleDisabled = false,
  toggleBusy = false,
  onOpen,
  onToggle,
}: {
  record: ExtensionInventoryRecord;
  toggleDisabled?: boolean;
  toggleBusy?: boolean;
  onOpen: () => void;
  /** Omitted when the catalog cannot change packages. */
  onToggle?: () => void;
}): JSX.Element {
  const { t } = useI18n();
  const toggle = pluginToggle(record);
  // A grant or change needs the fingerprint it approves.
  const toggleUnavailable = toggle.action === "grant" && !toggle.review && !record.fingerprint;
  const [menu, setMenu] = useState<{ x: number; y: number } | null>(null);
  const canEnable = !toggle.on;
  return (
    <div
      className="catalog-row plugin-row"
      data-plugin={record.provenance.plugin_id ?? record.id}
      data-on={toggle.on ? "" : undefined}
      aria-busy={toggleBusy || undefined}
    >
      <button
        className="catalog-row-open"
        type="button"
        aria-label={t("skills.pluginDetailLabel", { name: record.name })}
        onClick={onOpen}
      >
        <PluginMark record={record} />
        <TruncatedText className="catalog-row-title" text={record.name} />
        {record.description ? (
          <TruncatedText as="span" className="catalog-row-description" text={record.description} />
        ) : null}
      </button>
      <button
        className="settings-button settings-button-ghost settings-icon-button catalog-row-action"
        type="button"
        aria-label={t(canEnable ? "skills.pluginEnableNamed" : "provider.moreActions", { name: record.name })}
        aria-haspopup={canEnable ? undefined : "menu"}
        aria-expanded={canEnable ? undefined : Boolean(menu)}
        disabled={toggleDisabled || (canEnable && (toggleUnavailable || !onToggle))}
        aria-busy={toggleBusy || undefined}
        onClick={event => {
          if (canEnable) { onToggle?.(); return; }
          const bounds = event.currentTarget.getBoundingClientRect();
          setMenu({ x: bounds.right, y: bounds.bottom });
        }}
      >
        {canEnable ? <Plus className="icon" aria-hidden="true" /> : <MoreHorizontal className="icon" aria-hidden="true" />}
      </button>
      {menu ? <ThreadContextMenu x={menu.x} y={menu.y} onClose={() => setMenu(null)} items={[
        { label: t("skills.pluginDetailLabel", { name: record.name }), onSelect: onOpen },
        ...(onToggle ? [{ label: t(toggle.on ? "skills.pluginDisableNamed" : "skills.pluginEnableNamed", { name: record.name }), disabled: toggleDisabled || toggleUnavailable, onSelect: onToggle }] : []),
      ]} /> : null}
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
      <TruncatedText className="catalog-row-title" text={title} />
      {description ? <TruncatedText as="span" className="catalog-row-description" text={description} /> : null}
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
    <SettingsGroup className="skill-catalog-grid">
      {skills.map((skill) => {
        const pluginID = pluginSkillID(skill.source);
        return (
          <CatalogRow
            key={`${skill.source}:${skill.name}`}
            label={t("skills.previewSkill", { name: skill.name })}
            artwork={
              <span className="catalog-row-mark" aria-hidden="true">
                <DefaultExtensionIcon />
              </span>
            }
            title={skill.name}
            description={catalogSkillDescription(skill)}
            trailing={pluginID ? (
              <Tooltip asChild content={t("skills.pluginSkillTitle")}><span className="catalog-row-meta">
                <PluginBlocksIcon className="icon-sm" aria-hidden="true" />
                {pluginName(pluginID)}
              </span></Tooltip>
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
  const catalog = useCatalog();
  const [contentState, setContentState] = useState<SkillContentState>({
    loading: true,
    error: "",
    content: "",
  });

  useEffect(() => {
    let cancelled = false;
    setContentState({ loading: true, error: "", content: "" });
    void catalog.readSkill({ name: skill.name, source: skill.source })
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
  }, [catalog, skill.name, skill.source]);

  return (
    // The mark, the name and close share one row like the Model services
    // dialogs; the file follows. Its own opening states what the catalog row
    // already showed, so the dialog repeats no description.
    <Modal
      ariaLabel={t("skills.previewLabel", { name: skill.name })}
      icon={<DefaultExtensionIcon className="icon" />}
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
