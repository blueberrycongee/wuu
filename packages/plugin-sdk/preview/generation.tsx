import { scopeApplicationWebWorkspace } from '../dist/ui-web-workspace.js';
import { scopeApplicationEnvironment, scopeApplicationArtifacts, scopeApplicationOnboarding, scopeApplicationBrowser, scopeApplicationFileSelection, scopeApplicationProjects, scopeApplicationTerminals, scopeApplicationCatalog, scopeApplicationPreviewContent, scopeApplicationConversationText, scopeApplicationSettingsControls } from '../dist/ui.js';
import * as React from 'react';
import { createLocaleTranslator } from '../dist/localization.js';
import type {
  ApplicationServiceV1, ApplicationShellDefinition, ApplicationShellRenderProps, Disposable,
  PluginGenerationApi, UIContributionDefinition, UICompositionV1, UISlotDefinition, UISlotProps, ViewTypeDefinition, LocaleRegistration,
} from '../dist/index.js';
import { createPreviewUIKit } from './ui';

export interface PreviewModule {
  readonly descriptor?: { readonly id: string; readonly services?: readonly { readonly name: string; readonly version: string }[] };
  activate?(api: PluginGenerationApi): void | Promise<void>;
  preview?(api: PluginGenerationApi): React.ReactNode;
}
export interface PreviewEntry {
  readonly id: string;
  readonly title: string;
  readonly render: React.ComponentType<ApplicationShellRenderProps>;
}

class ContributionBoundary extends React.Component<{
  identity: string; children: React.ReactNode; recover?: boolean; fallback?: React.ReactNode;
}, { error?: Error }> {
  state: { error?: Error } = {};
  static getDerivedStateFromError(error: Error) { return { error }; }
  componentDidCatch(error: Error) { console.error(`${this.props.identity}: ${error.message}`); }
  render() {
    return this.state.error
      ? this.props.recover ? this.props.fallback : <div role="alert">{this.props.identity}: {this.state.error.message}</div>
      : this.props.children;
  }
}

/** Local preview lifetime; the product provides its own authoritative graph. */
export async function preparePreviewGeneration(modules: readonly PreviewModule[], application: ApplicationServiceV1, revision: string, onChange: () => void) {
  const entries: PreviewEntry[] = [];
  const owners = new Map<string, { signal: AbortSignal; application: ApplicationServiceV1 }>();
  const slots = new Map<string, UISlotDefinition>();
  const contributions: { owner: string; definition: UIContributionDefinition; removed: boolean }[] = [];
  const locales: { owner: string; definition: LocaleRegistration; removed: boolean }[] = [];
  const localeEntries = (locale: string) => Object.assign({}, ...locales.filter(record => !record.removed && record.definition.locale === locale).map(record => record.definition.entries)) as Readonly<Record<string, string>>;
  const cleanup: (() => void)[] = [];
  const styles: HTMLStyleElement[] = [];
  let phase: 'staging' | 'active' | 'disposed' = 'staging';
  const changed = () => { if (phase === 'active') onChange(); };
  const SlotPath = React.createContext<readonly string[]>([]);
  const composition: UICompositionV1 = { Slot: ({ name, context = {}, fallback = null, layout: Layout }: UISlotProps) => {
    const locale = React.useSyncExternalStore(application.subscribe, () => application.getSnapshot().locale);
    const translate = React.useMemo(() => createLocaleTranslator(locale, localeEntries), [locale]);
    const path = React.useContext(SlotPath);
    if (path.includes(name)) throw new Error(`Nested UI slot cycle: ${[...path, name].join(' → ')}`);
    if (!slots.has(name)) return null;
    const records = contributions.filter((record) => !record.removed && record.definition.target === name);
    const content = records.filter(({ definition }) => definition.mode !== 'wrap');
    const items = content.map(({ owner, definition }) => {
      const Render = definition.render;
      const identity = `${owner}/${definition.id}`;
      return { key: identity, pluginId: owner, id: definition.id, title: definition.title ?? definition.id,
        node: <ContributionBoundary key={identity} identity={identity}>
          <Render application={owners.get(owner)!.application} slots={composition} context={context} locale={locale} translate={translate} />
        </ContributionBoundary>,
      };
    });
    let composed: React.ReactNode = items.length === 0 ? fallback : Layout && !slots.get(name)!.service ? <Layout items={items} context={context} /> : items.map(item => item.node);
    for (const { owner, definition } of records.filter(({ definition }) => definition.mode === 'wrap')) {
      const Render = definition.render;
      const identity = `${owner}/${definition.id}`;
      composed = <ContributionBoundary key={identity} identity={identity} recover fallback={composed}>
        <Render application={owners.get(owner)!.application} slots={composition} context={context} locale={locale} translate={translate}>{composed}</Render>
      </ContributionBoundary>;
    }
    return <SlotPath.Provider value={[...path, name]}>{composed}</SlotPath.Provider>;
  } };
  const dispose = () => {
    if (phase === 'disposed') return;
    phase = 'disposed';
    const errors: unknown[] = [];
    for (const callback of [...cleanup].reverse()) { try { callback(); } catch (error) { errors.push(error); } }
    for (const style of styles) style.remove();
    if (errors.length) throw new AggregateError(errors, 'Preview cleanup failed.');
  };
  try {
    for (const [index, module] of modules.entries()) {
      const pluginId = module.descriptor?.id ?? `component-${index + 1}`;
      if (owners.has(pluginId)) throw new Error(`Duplicate preview plugin identity: ${pluginId}`);
      const controller = new AbortController();
      cleanup.push(() => controller.abort());
      const assertActive = () => {
        controller.signal.throwIfAborted();
        if (phase !== 'active') throw new Error(`Plugin ${pluginId} is not active.`);
      };
      const scoped: ApplicationServiceV1 = {
        contractVersion: 1,
        get composerPresentation() { assertActive(); controller.signal.throwIfAborted(); return application.composerPresentation; },
        get desktop() { assertActive(); controller.signal.throwIfAborted(); return application.desktop; },
        webWorkspace: scopeApplicationWebWorkspace(() => application.webWorkspace, controller.signal, assertActive),
        environment: scopeApplicationEnvironment(() => application.environment, controller.signal, assertActive),
        artifacts: scopeApplicationArtifacts(() => application.artifacts, controller.signal, assertActive),
        onboarding: scopeApplicationOnboarding(() => application.onboarding, controller.signal, assertActive),
        browser: scopeApplicationBrowser(() => application.browser, controller.signal, assertActive),
        fileSelection: scopeApplicationFileSelection(() => application.fileSelection, controller.signal, assertActive),
        projects: scopeApplicationProjects(() => application.projects, controller.signal, assertActive),
        terminals: scopeApplicationTerminals(() => application.terminals, controller.signal, assertActive),
        catalog: scopeApplicationCatalog(() => application.catalog, controller.signal, assertActive),
        previewContent: scopeApplicationPreviewContent(() => application.previewContent, controller.signal, assertActive),
        conversationText: scopeApplicationConversationText(() => application.conversationText, controller.signal, assertActive),
        settingsControls: scopeApplicationSettingsControls(() => application.settingsControls, controller.signal, assertActive),
        getSnapshot: () => { controller.signal.throwIfAborted(); return application.getSnapshot(); },
        subscribe(listener) {
          assertActive();
          const stop = application.subscribe(() => { if (phase === 'active' && !controller.signal.aborted) listener(); });
          const unsubscribe = () => { stop(); controller.signal.removeEventListener('abort', unsubscribe); };
          controller.signal.addEventListener('abort', unsubscribe, { once: true });
          return unsubscribe;
        },
        async invoke(action, input, options) {
          assertActive(); options?.signal?.throwIfAborted();
          const signal = options?.signal ? AbortSignal.any([options.signal, controller.signal]) : controller.signal;
          const result = await application.invoke(action, input, { signal });
          assertActive(); signal.throwIfAborted(); return result;
        },
      };
      owners.set(pluginId, { signal: controller.signal, application: scoped });
      const ids = new Set<string>();
      const claim = (kind: string, id: string) => {
        if (phase !== 'staging') throw new Error('Register contributions during activation.');
        if (!id.trim() || id.includes('/')) throw new Error(`Invalid local ${kind} ID: ${id}`);
        const key = `${kind}/${id}`;
        if (ids.has(key)) throw new Error(`Duplicate ${pluginId}/${key}`);
        ids.add(key);
      };
      const removeEntry = (entry: PreviewEntry): Disposable => ({ dispose() { const index = entries.indexOf(entry); if (index !== -1) entries.splice(index, 1); changed(); } });
      const registerStyle = ({ id, css }: { id: string; css: string }) => {
        claim('style', id);
        const style = document.createElement('style'); style.textContent = css; styles.push(style);
        return { dispose() { style.remove(); const index = styles.indexOf(style); if (index !== -1) styles.splice(index, 1); } };
      };
      const implemented = {
        react: React, ui: createPreviewUIKit(), pluginId, manifestId: pluginId, generation: revision, signal: controller.signal, application: scoped,
        registerCleanup(callback: () => void) {
          cleanup.push(callback);
          return { dispose() { const index = cleanup.indexOf(callback); if (index !== -1) { cleanup.splice(index, 1); callback(); } } };
        },
        registerApplicationShell(definition: ApplicationShellDefinition) {
          claim('shell', definition.id);
          if (!module.descriptor?.services?.some((service) => service.name === 'ui.shell' && service.version === '1.0.0')) {
            throw new Error(`${pluginId}: declare descriptor.services ui.shell@1.0.0 before registering a shell.`);
          }
          const Shell = definition.render;
          const Render = (props: ApplicationShellRenderProps) => <Shell {...props} application={scoped} />;
          const entry = { id: `${pluginId}/${definition.id}`, title: `${pluginId} · ${definition.id}`, render: Render };
          entries.push(entry); return removeEntry(entry);
        },
        registerUISlot(definition: UISlotDefinition) {
          claim('slot', definition.id);
          const name = `${pluginId}/${definition.id}`; slots.set(name, definition);
          return { dispose() { slots.delete(name); changed(); } };
        },
        registerUIContribution(definition: UIContributionDefinition) {
          claim('contribution', definition.id);
          if (definition.mode !== undefined && definition.mode !== 'content' && definition.mode !== 'wrap') throw new Error('Invalid UI contribution mode');
          const record = { owner: pluginId, definition, removed: false }; contributions.push(record);
          return { dispose() { record.removed = true; changed(); } };
        },
        registerLocale(definition: LocaleRegistration) {
          claim(`locale:${definition.locale}`, definition.id);
          const record = { owner: pluginId, definition, removed: false }; locales.push(record);
          return { dispose() { record.removed = true; changed(); } };
        },
        registerViewType(definition: ViewTypeDefinition) {
          claim('view', definition.id);
          const View = definition.render;
          const unavailable = async () => { throw new Error('View host effects require the Wuu desktop application.'); };
          const Render = () => <View locale={application.getSnapshot().locale} translate={createLocaleTranslator(application.getSnapshot().locale, localeEntries)} context={{}} host={{
            getStorage: unavailable, setStorage: unavailable, getSetting: unavailable, executeCommand: unavailable,
            openView: unavailable, closeView: unavailable,
          }} />;
          const entry = { id: `${pluginId}/${definition.id}`, title: definition.title, render: Render };
          entries.push(entry); return removeEntry(entry);
        },
        registerStyle, registerCSSSnippet: registerStyle,
      };
      const api = new Proxy(implemented, { get(target, key) {
        if (key in target) return Reflect.get(target, key);
        return () => { throw new Error(`${String(key)} requires the Wuu desktop host and is unavailable in component preview.`); };
      } }) as unknown as PluginGenerationApi;
      if (module.activate) await module.activate(api);
      if (module.preview) {
        const Preview = () => module.preview!(api);
        entries.push({ id: `${pluginId}/preview`, title: `${pluginId} component`, render: Preview });
      }
    }
    const compare = (a: string, b: string) => a < b ? -1 : a > b ? 1 : 0;
    contributions.sort((a, b) => (a.definition.order ?? 0) - (b.definition.order ?? 0) || compare(a.owner, b.owner) || compare(a.definition.id, b.definition.id));
    locales.sort((a, b) => (a.definition.order ?? 0) - (b.definition.order ?? 0) || compare(a.owner, b.owner) || compare(a.definition.id, b.definition.id));
    for (const [name, slot] of slots) {
      const records = contributions.filter((record) => !record.removed && record.definition.target === name);
      if (slot.service && records.some(({ definition }) => definition.mode === 'wrap')) {
        throw new Error(`Selected UI surfaces cannot be wrapped: ${name}`);
      }
      if (slot.cardinality === 'one' && records.filter(({ definition }) => definition.mode !== 'wrap').length > 1) {
        throw new Error(`UI slot ${name} accepts one contribution; multiple plugins claimed it.`);
      }
    }
    if (entries.length === 0) throw new Error('Export preview(api), or activate(api) and register a view or a declared application shell.');
    return {
      entries, composition,
      activate() { if (phase !== 'staging') throw new Error('Preview generation already settled.'); phase = 'active'; for (const style of styles) document.head.append(style); },
      dispose,
    };
  } catch (error) {
    try { dispose(); } catch (cleanupError) { throw new AggregateError([error, cleanupError], 'Preview preparation and cleanup failed.'); }
    throw error;
  }
}
