const { ipcRenderer } = require('electron');
module.exports = api => {
  const cwd = process.env.WUU_RESIZE_E2E_CWD;
  const now = '2026-11-04T10:30:00Z';
  const contexts = [
    { kind: 'project', project_id: 'wuu-demo', cwd },
    { kind: 'project', project_id: 'website-demo', cwd: `${cwd}/website` },
  ];
  const projects = contexts.map((context, index) => ({ id: context.project_id, name: index ? 'Website' : 'Wuu', path: context.cwd, created_at: now, updated_at: now }));
  const threads = ['修复会话切换时的滚动位置', '检查发布构建的执行结果', '整理网站无障碍改进清单'].map((title, index) => ({
    id: `recovery-${index}`, title, preview: '', cwd: contexts[index === 2 ? 1 : 0].cwd,
    workspace_id: contexts[index === 2 ? 1 : 0].project_id, status: 'idle', engine_id: 'wuu',
    model_provider: 'e2e', model: 'mock-resize', permission_mode: 'default', created_at: now, updated_at: now,
    turns: [{ id: `recovery-${index}-turn-0001`, status: index === 1 ? 'completed' : 'interrupted', items_view: 'full', items: [
      { id: `user-${index}`, type: 'user_message', role: 'user', status: 'completed', text: title },
    ] }],
  }));
  const listeners = new Set();
  ipcRenderer.on('test:recovery-refresh', () => { for (const listener of listeners) listener({ kind: 'notification', workdir: cwd, message: { method: 'thread/recoveryChanged', params: {} } }); });
  return { ...api,
    listProjects: async () => ({ projects, active_context: contexts[0] }),
    listThreads: async () => ({ threads }), listAllThreads: async () => ({ threads }), listArchivedThreads: async () => ({ threads: [] }),
    resumeThread: async id => ({ thread: threads.find(thread => thread.id === id) ?? threads[0] }),
    listRestartRecovery: async () => ({ items: (await ipcRenderer.invoke('test:recovery-list')).map(index => ({ thread: threads[index], turn_id: threads[index].turns[0].id, reason: index === 1 ? 'background_result' : 'interrupted' })) }),
    dismissRestartRecovery: ids => ipcRenderer.invoke('test:recovery-dismiss', ids),
    startTurn: (...args) => ipcRenderer.invoke('test:recovery-start', args),
    onServerEvent: listener => { listeners.add(listener); return () => listeners.delete(listener); },
  };
};
