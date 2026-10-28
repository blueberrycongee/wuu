// Synthetic selected-provider bridge for the production renderer. No inference.
const { ipcRenderer } = require('electron');
module.exports = api => {
  let graph = 'restoration-original', rootFailure = false, requestId = 0;
  const listeners = new Set();
  const context = { kind: 'no_project', cwd: process.env.WUU_RESIZE_E2E_CWD };
  const now = '2026-01-01T00:00:00Z';
  const threads = ['unavailable', 'healthy'].map(id => ({ id, cwd: context.cwd,
    title: id === 'unavailable' ? 'Unavailable conversation' : 'Healthy conversation', preview: '',
    status: 'idle', model_provider: 'e2e', model: 'mock-resize', created_at: now, updated_at: now,
    turns: [{ id: `${id}-turn`, status: 'completed', items_view: 'full', items: [
      { id: `${id}-answer`, type: 'agent_message', text: `${id} history remains accessible`, status: 'completed' },
    ] }],
  }));
  const configuration = async () => {
    const initialized = await api.initialize();
    return { ...initialized, workspace_root: context.cwd,
      extension_inventory: initialized.extension_inventory.map(row => ({ ...row, graph_generation: graph })),
    };
  };
  ipcRenderer.on('test:restoration-graph', async (_event, failure) => {
    graph += '-next'; rootFailure = Boolean(failure);
    const initialized = await configuration();
    for (const listener of listeners) listener({ kind: 'notification', workdir: context.cwd,
      message: { method: 'plugin/inventory/changed', params: initialized } });
  });
  return { ...api,
    openRecovery: async () => {},
    listProjects: async () => ({ projects: [], active_context: context }),
    selectNoProject: async () => ({ projects: [], active_context: context }),
    initialize: async () => {
      if (rootFailure) throw new Error('Synthetic selected application configuration unavailable');
      return configuration();
    },
    listThreads: async () => ({ threads: threads.map(thread => ({ ...thread, turns: [] })) }),
    listAllThreads: async () => ({ threads: threads.map(thread => ({ ...thread, turns: [] })) }),
    listArchivedThreads: async () => ({ threads: [] }),
    resumeThread: async id => {
      await ipcRenderer.invoke('test:restoration-resume', { id, graph, requestId: ++requestId });
      return { thread: threads.find(thread => thread.id === id) };
    },
    startThread: (...args) => ipcRenderer.invoke('test:restoration-write', 'startThread', args),
    startTurn: (...args) => ipcRenderer.invoke('test:restoration-write', 'startTurn', args),
    queueTurn: (...args) => ipcRenderer.invoke('test:restoration-write', 'queueTurn', args),
    onServerEvent: listener => { listeners.add(listener); return () => listeners.delete(listener); },
  };
};
