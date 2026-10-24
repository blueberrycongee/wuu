import type { PluginGenerationApi, UISlotRenderProps } from '@wuu/plugin-sdk';

export const descriptor = { id: 'research-evidence' };

export function activate(api: PluginGenerationApi): void {
  const React = api.react;
  const { Button, TextArea } = api.ui;
  api.registerUISlot({ id: 'details', cardinality: 'many' });
  api.registerStyle({ id: 'notebook', css: `
.research-notebook{display:grid;gap:1em;min-width:0}.research-notebook h3{font-size:1em;font-weight:500}.research-notebook textarea{width:100%;min-height:5em}.research-notebook ol{padding-left:1.3em;margin:0;display:grid;gap:1em}.research-notebook blockquote{margin:.4em 0;white-space:pre-wrap;overflow-wrap:anywhere;line-height:1.5;padding-left:.7em;border-left:2px solid var(--wuu-color-border-strong)}.research-notebook small{color:var(--wuu-color-text-muted)}.research-notebook button{margin-top:.4em}
` });
  function Evidence({ application, slots: { Slot } }: UISlotRenderProps) {
    const state = React.useSyncExternalStore(application.subscribe, application.getSnapshot);
    const threadId = state.conversation.threadId ?? '';
    const [notebooks, setNotebooks] = React.useState<Record<string, { note: string; clips: { id: string; text: string }[] }>>({});
    const notebook = notebooks[threadId] ?? { note: '', clips: [] };
    const answers = state.conversation.turns.flatMap((turn) => turn.items).filter((item) => item.kind === 'assistant-message' && item.status !== 'streaming');
    const latest = answers.at(-1);
    const text = latest?.text ?? latest?.content?.map((content) => content.text).join('\n');
    const update = (value: typeof notebook) => setNotebooks((current) => ({ ...current, [threadId]: value }));
    return <section className="research-notebook" aria-label="Evidence clips">
      <p><strong>{answers.length}</strong> completed answers</p>
      <Button disabled={!latest || !text || notebook.clips.some((clip) => clip.id === latest.id)} onClick={() => { if (latest && text) update({ ...notebook, clips: [...notebook.clips, { id: latest.id, text }] }); }}>Capture latest answer</Button>
      <TextArea label="Working notes" value={notebook.note} disabled={!threadId} onChange={(event) => update({ ...notebook, note: event.currentTarget.value })} />
      <small>Clips and notes stay in this loaded extension until it is reloaded.</small>
      <ol>{notebook.clips.map((clip) => <li key={clip.id}><blockquote>{clip.text}</blockquote><Button onClick={() => update({ ...notebook, clips: notebook.clips.filter((item) => item.id !== clip.id) })}>Remove clip</Button></li>)}</ol>
      <Slot name="research-evidence/details" context={{ threadId, clipCount: notebook.clips.length }} />
    </section>;
  }
  api.registerUIContribution({ id: 'notebook', target: 'research-workbench/evidence', render: Evidence });
  api.registerUIContribution({ id: 'source-details', target: 'research-evidence/details', render: ({ context }) =>
    Number(context.clipCount) > 0 ? <small>{Number(context.clipCount)} captured excerpts · Verify claims against original sources.</small> : null });
}
