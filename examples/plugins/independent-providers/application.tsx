import { APPLICATION_PROVIDER_SERVICE, APPLICATION_SHELL_SERVICE } from '@wuu/plugin-sdk';
import type {
  ApplicationCheckpointV1, ApplicationProviderInstanceV1, ApplicationServiceV1,
  ApplicationSnapshotV1, ApplicationShellRenderProps, PluginGenerationApi,
  PluginWorkspaceSnapshot, PluginThreadSummary, PluginVisibleHistoryResult,
} from '@wuu/plugin-sdk';

export const descriptor = {
  id: 'independent-application',
  services: [APPLICATION_PROVIDER_SERVICE, APPLICATION_SHELL_SERVICE],
};
const schema = 'independent-session-explorer.v1';
const unsupported = (name: PropertyKey): never => { throw new Error(`Session explorer does not support ${String(name)}`); };
/** A deliberately limited application, not a wrapper around the default controller.
 * Unsupported facets fail explicitly; select its own shell, not default product surfaces. */
export function createExplorer(api: PluginGenerationApi): ApplicationProviderInstanceV1 {
  let active = false, revision = 0, request = 0;
  let workspaceId: string | undefined, threadId: string | undefined;
  let workspaces: PluginWorkspaceSnapshot = {workspaces: []};
  let threads: readonly PluginThreadSummary[] = [];
  let history: PluginVisibleHistoryResult = {turns: []};
  let snapshot: ApplicationSnapshotV1;
  const listeners = new Set<() => void>();
  const scopes = new Set<AbortController>();
  const assertActive = () => { api.signal.throwIfAborted(); if (!active) throw new Error('Session explorer is stopped'); };
  function publish() {
    const supported = {
      contractVersion: 1 as const, revision: ++revision, phase: 'ready' as const,
      connection: {status: 'connected' as const},
      workspaces: {...workspaces, activeWorkspaceId: workspaceId},
      navigation: {contractVersion: 1 as const, section: 'conversations' as const,
        catalogSection: 'plugins' as const, catalogPluginId: '', activeNodeId: threadId,
        nodes: threads.map(row => ({id: row.id, kind: 'thread' as const, label: row.title, active: row.id === threadId}))},
      conversation: {threadId, title: threads.find(row => row.id === threadId)?.title, loading: false,
        turns: history.turns.map(turn => ({id: turn.id, status: 'completed' as const, items: [
          {contractVersion: 1 as const, id: `${turn.id}:user`, kind: 'user-message' as const, text: turn.userText},
          {contractVersion: 1 as const, id: `${turn.id}:assistant`, kind: 'assistant-message' as const, text: turn.assistantText},
        ]}))},
    } satisfies Pick<ApplicationSnapshotV1, 'contractVersion' | 'revision' | 'phase' | 'connection' | 'workspaces' | 'navigation' | 'conversation'>;
    // No fabricated default-product state: consumers requesting another facet get an error.
    snapshot = new Proxy(supported as unknown as ApplicationSnapshotV1, {
      get: (target, key) => key in target ? Reflect.get(target, key) : unsupported(`snapshot.${String(key)}`),
    });
    listeners.forEach(listener => listener());
  }
  async function load(signal?: AbortSignal, selection = {workspaceId, threadId}) {
    assertActive(); signal?.throwIfAborted(); const serial = ++request;
    const nextWorkspaces = await api.listWorkspaces();
    assertActive(); signal?.throwIfAborted();
    const selected = nextWorkspaces.workspaces.find(row => row.id === selection.workspaceId)
      ?? nextWorkspaces.workspaces.find(row => row.id === nextWorkspaces.activeWorkspaceId)
      ?? nextWorkspaces.workspaces[0];
    const nextThreads = selected ? await api.listThreads(selected.root) : [];
    assertActive(); signal?.throwIfAborted();
    const selectedThread = nextThreads.find(row => row.id === selection.threadId);
    const nextHistory = selected && selectedThread?.latestCompletedTurnId
      ? await api.readVisibleHistory({kind: 'main', threadId: selectedThread.id,
        workspaceId: selected.id, workspaceRoot: selected.root, beforeTurnId: selectedThread.latestCompletedTurnId})
      : {turns: []};
    assertActive(); signal?.throwIfAborted();
    if (serial !== request) return;
    workspaces = nextWorkspaces; workspaceId = selected?.id; threads = nextThreads;
    threadId = selectedThread?.id; history = nextHistory; publish();
  }
  function stop() { active = false; ++request; scopes.forEach(scope => scope.abort()); scopes.clear(); listeners.clear(); }
  return {
    async activate(checkpoint?: ApplicationCheckpointV1) {
      if (checkpoint) {
        if (checkpoint.schema !== schema || !checkpoint.value || typeof checkpoint.value !== 'object') throw new Error('Incompatible explorer checkpoint');
        const value = checkpoint.value as {workspaceId?: unknown; threadId?: unknown};
        if ((value.workspaceId !== undefined && typeof value.workspaceId !== 'string') || (value.threadId !== undefined && typeof value.threadId !== 'string')) throw new Error('Invalid explorer checkpoint');
        workspaceId = value.workspaceId; threadId = value.threadId;
      }
      active = true;
      try { await load(); } catch (error) { stop(); throw error; }
    },
    deactivate() { stop(); return {schema, value: {workspaceId, threadId}}; },
    dispose: stop,
    createScope(signal) {
      assertActive(); signal.throwIfAborted(); const lifetime = new AbortController(); scopes.add(lifetime);
      const abort = () => lifetime.abort(signal.reason); signal.addEventListener('abort', abort, {once: true});
      lifetime.signal.addEventListener('abort', () => {signal.removeEventListener('abort', abort); scopes.delete(lifetime);}, {once: true});
      const check = () => {assertActive(); lifetime.signal.throwIfAborted();};
      const service = {
        contractVersion: 1 as const,
        getSnapshot: () => {check(); return snapshot;},
        subscribe: (listener: () => void) => {
          check(); listeners.add(listener);
          const off = () => {listeners.delete(listener); lifetime.signal.removeEventListener('abort', off);};
          lifetime.signal.addEventListener('abort', off, {once: true}); return off;
        },
        invoke: async (action: string, input: {workspaceId?: string; threadId?: string}, options?: {signal?: AbortSignal}) => {
          check(); options?.signal?.throwIfAborted();
          const selection = {workspaceId, threadId};
          if (action === 'workspace.select') {selection.workspaceId = input.workspaceId; selection.threadId = undefined;}
          else if (action === 'thread.select') selection.threadId = input.threadId;
          else if (action !== 'navigation.refresh') unsupported(action);
          const operation = new AbortController();
          const abortOperation = () => operation.abort();
          lifetime.signal.addEventListener('abort', abortOperation, {once: true});
          options?.signal?.addEventListener('abort', abortOperation, {once: true});
          try { await load(operation.signal, selection); check(); }
          finally {
            lifetime.signal.removeEventListener('abort', abortOperation);
            options?.signal?.removeEventListener('abort', abortOperation);
          }
        },
      };
      return new Proxy(service as ApplicationServiceV1, {
        get: (target, key) => {check(); return key in target ? Reflect.get(target, key) : unsupported(key);},
      });
    },
  };
}

export function activate(api: PluginGenerationApi): void {
  const React = api.react;
  api.registerApplicationProvider({id: 'ui.application', prepare: () => createExplorer(api)});
  function Explorer({application}: ApplicationShellRenderProps) {
    const state = React.useSyncExternalStore(application.subscribe, application.getSnapshot);
    const invoke = (action: 'workspace.select' | 'thread.select' | 'navigation.refresh', input: {workspaceId?: string; threadId?: string}) => {
      void application.invoke(action, input as never).catch(() => {
        if (!api.signal.aborted) api.showNotice({message: 'Could not refresh the session explorer. Try Refresh again.', tone: 'error', key: 'explorer-refresh'});
      });
    };
    return <main aria-label="Independent session explorer" style={{padding: 24, overflow: 'auto', height: '100%'}}>
      <h1>Session explorer</h1><p>Independent application provider · read-only completed history</p>
      <button onClick={() => invoke('navigation.refresh', {})}>Refresh</button>
      <nav aria-label="Workspaces">{state.workspaces.workspaces.map(row => <button key={row.id}
        aria-current={row.id === state.workspaces.activeWorkspaceId ? 'page' : undefined}
        onClick={() => invoke('workspace.select', {workspaceId: row.id})}>{row.name}</button>)}</nav>
      <nav aria-label="Sessions">{state.navigation.nodes.map(row => <button key={row.id}
        aria-current={row.active ? 'page' : undefined} onClick={() => invoke('thread.select', {threadId: row.id})}>{row.label}</button>)}</nav>
      <h2>{state.conversation.title ?? 'Select a session'}</h2>
      <p>Bounded completed history before the latest completed turn; the boundary turn is not included.</p>
      {state.conversation.turns.map(turn => <section key={turn.id}>{turn.items.map(item => <pre key={item.id} style={{whiteSpace: 'pre-wrap'}}>{item.text}</pre>)}</section>)}
    </main>;
  }
  api.registerApplicationShell({id: 'ui.shell', render: Explorer});
}
