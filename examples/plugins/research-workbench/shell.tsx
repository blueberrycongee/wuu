import { APPLICATION_SHELL_SERVICE } from '@wuu/plugin-sdk';
import type { ApplicationShellRenderProps, PluginGenerationApi } from '@wuu/plugin-sdk';

export const descriptor = { id: 'research-workbench', services: [APPLICATION_SHELL_SERVICE] };

export function activate(api: PluginGenerationApi): void {
  const React = api.react;
  const { Button, TextInput, TextArea, Select, EmptyState, ErrorState, LoadingState } = api.ui;
  api.registerUISlot({ id: 'evidence', cardinality: 'many' });
  api.registerStyle({ id: 'workbench', css: `
.research-shell{height:100%;min-height:0;display:flex;flex-direction:column;background:var(--wuu-color-canvas);color:var(--wuu-color-text);font-size:var(--wuu-font-size-ui,1rem)}
.research-shell *{box-sizing:border-box}.research-shell button,.research-shell input,.research-shell textarea,.research-shell select{font:inherit}.research-shell label{min-width:0}.research-shell input:not([type=checkbox]),.research-shell textarea,.research-shell select{min-width:0;max-width:100%;width:100%}.research-shell h1,.research-shell h2,.research-shell h3,.research-shell p{margin:0}.research-shell h1{font-size:1.2em;font-weight:600}.research-shell h2{font-size:1.05em;font-weight:600}.research-shell h3{font-size:1em;font-weight:600}
.research-topbar{display:flex;gap:.8em;align-items:center;flex-wrap:wrap;padding:.85em max(1em,var(--window-controls-inset-right,0px)) .85em max(1em,var(--window-controls-inset-left,0px));border-bottom:1px solid var(--wuu-color-border-subtle);flex-shrink:0}.research-topbar h1{flex:1;min-width:8em}.research-status{color:var(--wuu-color-text-muted);font-size:.9em}.research-layout{display:grid;grid-template-columns:minmax(11em,16em) minmax(0,1fr) minmax(13em,21em);flex:1;min-height:0}.research-sidebar,.research-evidence{overflow:auto;padding:1em;min-width:0}.research-sidebar{border-right:1px solid var(--wuu-color-border-subtle)}.research-evidence{border-left:1px solid var(--wuu-color-border-subtle);display:flex;flex-direction:column;gap:1em}.research-sidebar>*+*{margin-top:.8em}.research-nav{display:grid;gap:.35em}.research-nav button{text-align:left;width:100%;display:flex;align-items:center;gap:.5em;background:transparent;border:0;border-radius:.45em;padding:.7em;color:inherit;line-height:1.35}.research-nav button[aria-current=page]{background:var(--wuu-color-surface-muted)}.research-node-label{flex:1;min-width:0;overflow-wrap:anywhere}.research-node-status{font-size:.8em;color:var(--wuu-color-text-muted)}.research-main{display:flex;flex-direction:column;min-height:0;min-width:0}.research-threadbar{display:flex;align-items:center;gap:.6em;padding:1em;border-bottom:1px solid var(--wuu-color-border-subtle);flex-wrap:wrap}.research-threadbar h2{flex:1;min-width:6em;overflow-wrap:anywhere}.research-timeline{flex:1;min-height:6em;overflow:auto;padding:1.3em clamp(1em,3vw,3em)}.research-turn{max-width:54em;margin:0 auto 1.8em}.research-message{margin-top:1.1em;overflow-wrap:anywhere}.research-message header{font-size:.9em;color:var(--wuu-color-text-muted);margin-bottom:.5em}.research-message p{white-space:pre-wrap;line-height:1.65}.research-message[data-kind=user-message]{background:var(--wuu-color-surface-muted);border-radius:.65em;padding:1em}.research-process{margin-top:.8em;color:var(--wuu-color-text-muted)}.research-process li{margin:.5em 0;overflow-wrap:anywhere}.research-composer{padding:1em;border-top:1px solid var(--wuu-color-border-subtle);display:grid;gap:.7em;background:var(--wuu-color-surface)}.research-composer textarea{width:100%;min-height:5em;max-height:15em;resize:vertical}.research-composer-actions{display:flex;align-items:center;gap:.7em;flex-wrap:wrap}.research-composer-actions small{flex:1;color:var(--wuu-color-text-muted)}.research-error{padding:.7em 1em;color:var(--wuu-color-text);border-bottom:1px solid var(--wuu-color-border-strong)}.research-settings,.research-preview{flex:1;min-height:0;overflow:auto;padding:1.5em;display:flex;flex-direction:column;gap:1.2em}.research-settings label{max-width:28em}.research-settings ul{padding-left:1.2em}.research-preview pre{white-space:pre-wrap;overflow-wrap:anywhere;font-family:monospace}.research-footer{display:flex;gap:1em;padding:.5em 1em;border-top:1px solid var(--wuu-color-border-subtle);color:var(--wuu-color-text-muted);font-size:.9em;flex-wrap:wrap}.research-rename{display:flex;align-items:end;gap:.5em;flex:1;min-width:0}.research-rename label{flex:1;min-width:0}.research-shell [hidden]{display:none!important}
 .research-main{overflow:auto}.research-threadbar,.research-composer{flex-shrink:0}.research-shell[data-navigation=closed] .research-sidebar,.research-shell[data-evidence=closed] .research-evidence{display:none}.research-shell[data-navigation=closed] .research-layout{grid-template-columns:minmax(0,1fr) minmax(13em,21em)}.research-shell[data-evidence=closed] .research-layout{grid-template-columns:minmax(11em,16em) minmax(0,1fr)}.research-shell[data-navigation=closed][data-evidence=closed] .research-layout{grid-template-columns:minmax(0,1fr)}
@media(max-width:1100px){.research-layout,.research-shell[data-evidence=closed] .research-layout{grid-template-columns:minmax(10em,14em) minmax(0,1fr)}.research-shell[data-navigation=closed] .research-layout{grid-template-columns:minmax(0,1fr)}.research-shell[data-evidence=open] .research-main{display:none}.research-evidence{min-height:0;max-height:none}.research-threadbar h2{flex-basis:100%}}
@media(max-width:650px){.research-layout{display:flex;flex-direction:column}.research-sidebar,.research-evidence{flex:1;min-height:0;max-height:none;border:0}.research-main{flex:1}.research-shell[data-navigation=open] .research-main,.research-shell[data-navigation=open] .research-evidence{display:none}.research-topbar{padding-block:.7em}.research-topbar h1{min-width:6em}.research-timeline{padding:1em}}

` });

  function useMedia(query: string): boolean {
    const media = React.useMemo(() => window.matchMedia(query), [query]);
    const subscribe = React.useCallback((listener: () => void) => {
      media.addEventListener('change', listener);
      return () => media.removeEventListener('change', listener);
    }, [media]);
    return React.useSyncExternalStore(subscribe, () => media.matches);
  }

  function Shell({ application, slots: { Slot } }: ApplicationShellRenderProps) {
    const state = React.useSyncExternalStore(application.subscribe, application.getSnapshot);
    const [query, setQuery] = React.useState('');
    const [error, setError] = React.useState('');
    const narrow = useMedia('(max-width:650px)');
    const compact = useMedia('(max-width:1100px)');
    const [navigation, setNavigation] = React.useState(!narrow);
    const [evidence, setEvidence] = React.useState(!compact);
    React.useEffect(() => { setNavigation(!narrow); setEvidence(!compact); }, [narrow, compact]);
    const [renaming, setRenaming] = React.useState(false);
    const [title, setTitle] = React.useState('');
    const [mode, setMode] = React.useState<'send' | 'queue' | 'steer'>('send');
    const perform = (operation: Promise<unknown>) => { setError(''); void operation.catch((cause: unknown) => { if (!api.signal.aborted) setError(cause instanceof Error ? cause.message : String(cause)); }); };
    const thread = state.conversation;
    const nodes = state.navigation.nodes.filter((node) => node.kind === 'thread' && node.label.toLocaleLowerCase().includes(query.toLocaleLowerCase()));
    const turnsEnd = React.useRef<HTMLDivElement>(null);
    const timeline = React.useRef<HTMLDivElement>(null);
    const follow = React.useRef(true);
    React.useEffect(() => { follow.current = true; turnsEnd.current?.scrollIntoView({ block: 'end' }); }, [thread.threadId]);
    React.useEffect(() => { if (follow.current) turnsEnd.current?.scrollIntoView({ block: 'end' }); }, [thread.turns]);
    return <div className="research-shell" data-navigation={navigation ? 'open' : 'closed'} data-evidence={evidence ? 'open' : 'closed'}>
      <header className="research-topbar">
        <Button aria-label="Toggle navigation" aria-expanded={navigation} onClick={() => { setNavigation(!navigation); if (narrow) setEvidence(false); }}>Threads</Button>
        <h1>Research workbench</h1><span className="research-status" role="status">{state.connection.status}</span>
        <Button aria-label="Toggle evidence" aria-expanded={evidence} onClick={() => { setEvidence(!evidence); if (narrow) setNavigation(false); }}>Evidence</Button>
        <Button aria-label="Settings" onClick={() => perform(application.invoke(state.settings.open ? 'settings.close' : 'settings.open', {}))}>Settings</Button>
      </header>
      {(error || state.connection.message) && <div className="research-error" role="alert">{error || state.connection.message}</div>}
      <div className="research-layout">
        <aside className="research-sidebar" aria-label="Research navigation">
          <Select label="Workspace" value={state.workspaces.activeWorkspaceId ?? ''} onChange={(event) => perform(application.invoke('workspace.select', { workspaceId: event.currentTarget.value }))}>
            <option value="" disabled>Select a workspace</option>{state.workspaces.workspaces.map((workspace) => <option key={workspace.id} value={workspace.id} disabled={!workspace.available}>{workspace.name}</option>)}
          </Select>
          <Button variant="primary" onClick={() => perform(application.invoke('thread.create', { workspaceId: state.workspaces.activeWorkspaceId }))}>New research</Button>
          <TextInput label="Find threads" value={query} onChange={(event) => setQuery(event.currentTarget.value)} />
          <nav className="research-nav" aria-label="Threads">{nodes.map((node) => <button key={node.id} aria-current={node.active ? 'page' : undefined} disabled={node.disabled} onClick={() => { setNavigation(false); perform(application.invoke('thread.select', { threadId: node.id })); }}>
            <span className="research-node-label">{node.label}</span><span className="research-node-status">{node.running ? 'Running' : node.unread ? 'Unread' : node.pinned ? 'Pinned' : ''}</span>
          </button>)}{nodes.length === 0 && <p className="research-status">{query ? 'No matching threads.' : 'Create a research thread to begin.'}</p>}</nav>
        </aside>
        <main className="research-main">
          {state.settings.open ? <section className="research-settings" aria-label="Workbench settings">
            <h2>Settings</h2>
            <Select label="Theme" value={state.appearance.theme} onChange={(event) => perform(application.invoke('appearance.set', { theme: event.currentTarget.value as 'system' | 'light' | 'dark' }))}>
              <option value="system">System</option><option value="light">Light</option><option value="dark">Dark</option>
            </Select>
            <TextInput label="Interface text size" type="number" min={12} max={24} value={state.appearance.uiFontSize} onChange={(event) => { if (event.currentTarget.value) perform(application.invoke('appearance.set', { uiFontSize: Number(event.currentTarget.value) })); }} />
            <TextInput label="Code text size" type="number" min={10} max={24} value={state.appearance.codeFontSize} onChange={(event) => { if (event.currentTarget.value) perform(application.invoke('appearance.set', { codeFontSize: Number(event.currentTarget.value) })); }} />
            <h3>Providers</h3><ul>{state.settings.providers?.map((provider) => <li key={provider.id}>{provider.label} · {provider.status ?? (provider.configured ? 'Configured' : 'Not configured')}</li>)}</ul>
            <h3>Extensions</h3><ul>{state.settings.plugins?.map((plugin) => <li key={plugin.id}>{plugin.name} · {plugin.status ?? (plugin.enabled ? 'Enabled' : 'Disabled')}</li>)}</ul>
            <Button onClick={() => perform(application.invoke('runtime.refresh', {}))} disabled={state.settings.busy}>Refresh services</Button>
            {state.settings.error && <p role="alert">{state.settings.error}</p>}
          </section> : state.preview ? <section className="research-preview" aria-label="Resource preview">
            <h2>{state.preview.workspaceRelativePath}</h2><Button onClick={() => perform(application.invoke('preview.close', {}))}>Close preview</Button>
            {state.preview.error ? <p role="alert">{state.preview.error}</p> : state.preview.loading ? <LoadingState /> : <pre style={{ fontSize: state.appearance.codeFontSize }}>{state.preview.text ?? (state.preview.binary ? 'Binary resource' : 'No text preview available.')}</pre>}
            {state.preview.truncated && <small>Preview is truncated.</small>}
          </section> : <>
            <header className="research-threadbar">{renaming && thread.threadId ? <form className="research-rename" onSubmit={(event) => { event.preventDefault(); setRenaming(false); perform(application.invoke('thread.rename', { threadId: thread.threadId!, title })); }}>
              <TextInput label="Thread title" value={title} onChange={(event) => setTitle(event.currentTarget.value)} autoFocus /><Button type="submit" disabled={!title.trim()}>Save</Button><Button type="button" onClick={() => setRenaming(false)}>Cancel</Button>
            </form> : <><h2>{thread.title || 'Research'}</h2><Button disabled={!thread.threadId} onClick={() => { setTitle(thread.title ?? ''); setRenaming(true); }}>Rename</Button><Button disabled={!thread.threadId || state.composer.running} onClick={() => perform(application.invoke('thread.archive', { threadId: thread.threadId!, archived: true }))}>Archive</Button></>}</header>
            <div className="research-timeline" ref={timeline} role="log" aria-label="Conversation" onScroll={() => { const node = timeline.current!; follow.current = node.scrollHeight - node.scrollTop - node.clientHeight < 80; }}>
              {thread.error && <ErrorState title="Conversation unavailable" description={thread.error} />}
              {thread.loading ? <LoadingState title="Loading conversation" /> : thread.turns.length === 0 ? <EmptyState title="Start with a question" description="Compare sources, examine an idea, or develop a research plan." /> : thread.turns.map((turn) => <article className="research-turn" key={turn.id}>
                {turn.items.map((item) => <section key={item.id} className="research-message" data-kind={item.kind}>
                  <header>{item.kind === 'user-message' ? 'You' : item.kind === 'assistant-message' ? 'Assistant' : item.kind}{item.status === 'streaming' ? ' · Writing' : ''}</header>
                  <p>{item.text ?? item.content?.map((content) => content.text).join('\n') ?? item.summary}</p>
                  {item.attachments?.map((attachment) => <Button key={attachment.id} onClick={() => perform(application.invoke('preview.open', { resourceId: attachment.id }))}>{attachment.name}</Button>)}
                </section>)}
                {turn.process && <details className="research-process"><summary>Activity · {turn.process.status}</summary><ul>{turn.process.items.map((item) => <li key={item.id}>{item.displayLabel ?? item.toolName ?? item.text ?? item.kind} · {item.status}{item.error && <p role="alert">{item.error}</p>}</li>)}</ul></details>}
              </article>)}<div ref={turnsEnd} />
            </div>
            <form className="research-composer" onSubmit={(event) => { event.preventDefault(); perform(application.invoke('composer.submit', { mode })); }}>
              <TextArea label="Message" placeholder="Ask a question or follow up…" value={state.composer.draftText ?? ''} disabled={state.composer.readOnly} onChange={(event) => perform(application.invoke('composer.set-draft', { text: event.currentTarget.value }))} />
              <div className="research-composer-actions"><small>{state.composer.model?.label ?? 'No model'} · {state.composer.permission?.label ?? 'Default permissions'}</small>
                {(state.composer.availableSubmissionModes?.length ?? 0) > 1 && <Select label="Delivery" value={mode} onChange={(event) => setMode(event.currentTarget.value as typeof mode)}>{state.composer.availableSubmissionModes?.map((value) => <option key={value} value={value}>{value}</option>)}</Select>}
                {state.composer.running && <Button type="button" onClick={() => perform(application.invoke('composer.stop', {}))}>Stop</Button>}
                <Button variant="primary" type="submit" disabled={state.composer.readOnly || !thread.threadId || !state.composer.draftText?.trim()}>{mode === 'queue' ? 'Queue' : mode === 'steer' ? 'Steer' : 'Send'}</Button>
              </div>{state.composer.disabledReason && <small>{state.composer.disabledReason}</small>}
            </form>
          </>}
        </main>
        <aside className="research-evidence" aria-label="Research evidence"><h2>Evidence notebook</h2><Slot name="research-workbench/evidence" context={{ threadId: thread.threadId }} fallback={<p className="research-status">Enable an evidence extension to collect sources alongside the conversation.</p>} /></aside>
      </div>
      <footer className="research-footer">{state.status.items.map((item) => <span key={item.id}>{item.label}{item.detail ? ` · ${item.detail}` : ''}</span>)}<span>{thread.turns.length} turns</span></footer>
    </div>;
  }
  api.registerApplicationShell({ id: 'ui.shell', render: Shell });
}
