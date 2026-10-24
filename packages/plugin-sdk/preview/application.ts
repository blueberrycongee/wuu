import type { ApplicationActionMapV1, ApplicationServiceV1, ApplicationSnapshotV1 } from '../dist/index.js';

export function createPreviewApplication(initial?: ApplicationSnapshotV1): ApplicationServiceV1 {
  let sequence = 1;
  let state: ApplicationSnapshotV1 = initial ?? {
    contractVersion: 1, revision: 0, phase: 'ready', connection: { status: 'connected' }, locale: 'en',
    workspaces: { activeWorkspaceId: 'research', workspaces: [{ id: 'research', name: 'Research', root: '/preview/research', available: true }] },
    navigation: { contractVersion: 1, section: 'conversations', catalogSection: 'plugins', catalogPluginId: '', activeNodeId: 'research-1', nodes: [
      { id: 'research', kind: 'project', label: 'Research' },
      { id: 'research-1', kind: 'thread', label: 'Compare the evidence', parentId: 'research', active: true },
    ] },
    tabs: { activeTabId: 'thread:research-1', activePane: 'primary', primaryThreadId: 'research-1', canSubmit: true, items: [{ id: 'thread:research-1', kind: 'thread',
      title: 'Compare the evidence', workspaceId: 'research', workspaceRoot: '/preview/research',
      threadId: 'research-1', project: false, draftText: '', hasAttachments: false }] },
    conversation: { threadId: 'research-1', title: 'Compare the evidence', loading: false, turns: [{
      id: 'turn-1', status: 'completed', items: [
        { contractVersion: 1, id: 'user-1', kind: 'user-message', text: 'Which approach has the strongest evidence?' },
        { contractVersion: 1, id: 'answer-1', kind: 'assistant-message', text: 'Compare the source, the method and the limitations. Keep observations separate from conclusions.', status: 'completed' },
      ], process: { contractVersion: 1, kind: 'tool-group', status: 'completed', streaming: false, active: false,
        items: [{ id: 'search-1', kind: 'tool-activity', status: 'completed', toolName: 'Search sources', displayLabel: 'Reviewed 3 sources' }] },
    }] },
    composer: { contractVersion: 1, threadId: 'research-1', draftText: '', running: false, availableSubmissionModes: ['send'], model: { id: 'preview', label: 'Preview fixture' }, permission: { id: 'standard', label: 'Standard' } },
    composerPending: [],
    drafts: { activeTabId: 'thread:research-1', activePane: 'primary', split: false,
      primary: { text: '', imageIds: [], fileIds: [], selections: [] }, secondary: { text: '', imageIds: [], fileIds: [], selections: [] }, attachments: [] },
    settings: { contractVersion: 1, open: false, engineInventoryError: '', runningProviderNames: [], archivedThreads: [], runtimeSwitchPending: false, availablePages: [{ id: 'appearance', label: 'Appearance' }],
      providers: [{ id: 'preview', label: 'Synthetic preview data', configured: true }], plugins: [] },
    preview: null, previewWorkspace: { tabs: [], conversationSelected: false }, appearance: { theme: 'system', resolvedTheme: 'light', uiFontSize: 15, codeFontSize: 13 },
    status: { contractVersion: 1, items: [{ id: 'ready', label: 'Preview fixture', kind: 'info' }] },
  };
  if (state.contractVersion !== 1) throw new Error('Preview state requires ApplicationSnapshotV1 contractVersion 1.');
  const listeners = new Set<() => void>();
  const histories = new Map([[state.conversation.threadId, state.conversation.turns]]);
  const update = (change: Partial<ApplicationSnapshotV1>) => {
    state = { ...state, ...change, revision: state.revision + 1 };
    for (const listener of listeners) listener();
  };
  const unavailable = (): never => { throw new Error("Settings controls are unavailable in the synthetic preview"); };
  const service: ApplicationServiceV1 = {
    contractVersion: 1,
    settingsControls: {
      capabilities: Object.freeze({ browser: false, browserReload: false, browserOptions: false, remote: false, build: false, catalog: false, engines: false, pets: false, diagnostics: false }),
      subscribe: () => () => {}, subscribeRemote: () => () => {}, getPages: () => [], getConflicts: () => [], getDiagnostics: () => [],
      renderPage: unavailable, renderPresentation: unavailable,
      chooseConflict: unavailable,
      getBuildInfo: unavailable,
      useCodexCredentials: unavailable,
      loadCodexModels: unavailable,
      refreshModelCatalog: unavailable,
      listCatalogProviders: unavailable,
      listEngines: unavailable,
      getPluginSetting: unavailable,
      setPluginSetting: unavailable,
      getPluginDiagnostics: unavailable,
      listMCPServers: unavailable,
      connectMCPServer: unavailable,
      disconnectMCPServer: unavailable,
      refreshMCPServer: unavailable,
      startMCPAuth: unavailable,
      finishMCPAuth: unavailable,
      removeMCPAuth: unavailable,
      listChatGPTModels: unavailable,
      startXAILogin: unavailable,
      pollXAILogin: unavailable,
      cancelXAILogin: unavailable,
      listBrowserProfiles: unavailable,
      importBrowserCookies: unavailable,
      listBrowserExtensions: unavailable,
      addBrowserExtension: unavailable,
      setBrowserExtensionEnabled: unavailable,
      reloadBrowserExtension: unavailable,
      openBrowserExtensionOptions: unavailable,
      removeBrowserExtension: unavailable,
      getRemoteControlSnapshot: unavailable,
      setRemoteHostEnabled: unavailable,
      startRemotePairing: unavailable,
      removeRemoteDevice: unavailable,
      openExternal: unavailable,
    },
    getSnapshot: () => state,
    subscribe(listener) { listeners.add(listener); return () => { listeners.delete(listener); }; },
    async invoke<Action extends keyof ApplicationActionMapV1>(action: Action, input: ApplicationActionMapV1[Action]['input'], options?: { signal?: AbortSignal }) {
      options?.signal?.throwIfAborted();
      // This adapter deliberately implements local fixtures, never product RPCs.
      const params = input as Record<string, any>;
      let result: unknown;
      switch (action) {
        case 'workspace.select':
          if (!state.workspaces.workspaces.some((workspace) => workspace.id === params.workspaceId)) throw new Error('Workspace is unavailable.');
          update({ workspaces: { ...state.workspaces, activeWorkspaceId: params.workspaceId } }); break;
        case 'thread.create': {
          const threadId = `preview-${++sequence}`;
          histories.set(threadId, []);
          update({ navigation: { ...state.navigation, activeNodeId: threadId, nodes: [...state.navigation.nodes.map((node) => ({ ...node, active: false })), { id: threadId, kind: 'thread', label: 'New research', active: true }] },
            conversation: { threadId, title: 'New research', loading: false, turns: [] }, composer: { ...state.composer, threadId, draftText: '' } });
          result = { threadId }; break;
        }
        case 'thread.select': {
          const node = state.navigation.nodes.find((node) => node.id === params.threadId && node.kind === 'thread');
          if (!node) throw new Error('Thread is unavailable.');
          update({ navigation: { ...state.navigation, activeNodeId: node.id, nodes: state.navigation.nodes.map((item) => ({ ...item, active: item.id === node.id })) },
            conversation: { threadId: node.id, title: node.label, loading: false, turns: histories.get(node.id) ?? [] }, composer: { ...state.composer, threadId: node.id, draftText: '' } }); break;
        }
        case 'thread.rename':
          update({ navigation: { ...state.navigation, nodes: state.navigation.nodes.map((node) => node.id === params.threadId ? { ...node, label: params.title } : node) },
            conversation: state.conversation.threadId === params.threadId ? { ...state.conversation, title: params.title } : state.conversation }); break;
        case 'thread.archive':
          if (!params.archived) throw new Error('This preview fixture does not retain archived threads.');
          update({ navigation: { ...state.navigation, nodes: state.navigation.nodes.filter((node) => node.id !== params.threadId) },
            conversation: state.conversation.threadId === params.threadId ? { loading: false, turns: [] } : state.conversation }); break;
        case 'composer.set-draft': update({ composer: { ...state.composer, draftText: params.text } }); break;
        case 'composer.submit': {
          if (!state.composer.draftText?.trim()) throw new Error('Enter a message first.');
          if (!state.conversation.threadId) throw new Error('Create a thread first.');
          const id = `preview-turn-${++sequence}`;
          const turns = [...state.conversation.turns, { id, status: 'completed' as const, items: [
            { contractVersion: 1 as const, id: `${id}-user`, kind: 'user-message' as const, text: state.composer.draftText },
            { contractVersion: 1 as const, id: `${id}-answer`, kind: 'assistant-message' as const, text: 'Preview response: your message reached the public action adapter. No model request was sent.' },
          ] }];
          histories.set(state.conversation.threadId, turns);
          update({ conversation: { ...state.conversation, turns }, composer: { ...state.composer, draftText: '', running: false } }); break;
        }
        case 'composer.stop': update({ composer: { ...state.composer, running: false } }); break;
        case 'composer.configure': throw new Error('Model, runtime and permission configuration require a product application adapter.');
        case 'settings.open': update({ settings: { ...state.settings, open: true, activePageId: params.pageId } }); break;
        case 'settings.close': update({ settings: { ...state.settings, open: false } }); break;
        case 'settings.update-value': throw new Error('Persistent settings require a product application adapter.');
        case 'appearance.set': update({ appearance: { ...state.appearance, ...params, resolvedTheme: (params.theme === 'system' ? state.appearance.resolvedTheme : params.theme) ?? state.appearance.resolvedTheme } }); break;
        case 'preview.open': update({ preview: { contractVersion: 1, resourceId: params.resourceId, workspaceRelativePath: params.resourceId, text: 'Preview resource fixture.', readOnly: true } }); break;
        case 'preview.close': update({ preview: null }); break;
        case 'runtime.refresh': update({ connection: { status: 'connected' } }); break;
        default: throw new Error(`Unsupported preview action: ${String(action)}`);
      }
      return result as ApplicationActionMapV1[Action]['result'];
    },
  };
  return service;
}
