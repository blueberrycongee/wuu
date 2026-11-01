import { EventEmitter } from 'node:events';
import { PassThrough } from 'node:stream';
import { describe, expect, it, vi } from 'vitest';
import { DictationService } from './dictation';

function fixture(permission = Promise.resolve(true)) {
  const child = Object.assign(new EventEmitter(), { stdout: new PassThrough(), stderr: new PassThrough(), stdin: new PassThrough(), kill: vi.fn() });
  const spawn = vi.fn(() => child);
  const service = new DictationService({ askForMicrophoneAccess: () => permission, spawn });
  const events = vi.fn();
  return { child, spawn, service, events };
}
describe('dictation session lifecycle', () => {
  it('waits for the final result after stop and forwards the complete text', async () => {
    const { service, child, events } = fixture();
    await service.start('one', 'zh-CN', events);
    service.stop('one');
    expect(child.kill).not.toHaveBeenCalled();
    child.stdout.write(JSON.stringify({ type: 'result', text: '最后几个字', is_final: true }) + '\n');
    expect(events).toHaveBeenCalledWith({ sessionId: 'one', type: 'result', text: '最后几个字', is_final: true });
    child.emit('close', 0);
    expect(events.mock.calls.some(([event]) => event.type === 'error')).toBe(false);
  });
  it('retains earlier speech when macOS starts a new recognition window', async () => {
    // macOS Speech starts a new window at its audio limit or after a long
    // pause without a final event; the next window begins at live audio.
    const { service, child, events } = fixture();
    await service.start('long', 'zh-CN', events);
    const firstWindow = '第一句今天我们来测试一段很长的语音听写看看前面的内容会不会丢失';
    const correctedWindow = firstWindow + '第二句窗外阳光很好桌上的咖啡还冒着热气我准备把今天要做的事情都说一遍';
    const revisedWindow = firstWindow + '第二句窗外阳光很好桌上的咖啡还冒着N';
    const nextWindow = '第三句';
    const finalWindow = '第三句上午先整理项目文档把昨天讨论的接口变更补充清楚';
    for (const [text, is_final] of [
      [firstWindow, false],
      [revisedWindow, false],
      [correctedWindow, false],
      [nextWindow, false],
      [finalWindow, true],
    ] as const) {
      child.stdout.write(JSON.stringify({ type: 'result', text, is_final }) + '\n');
    }
    const results = events.mock.calls.map(([event]) => event).filter(event => event.type === 'result');
    expect(results.every(event => event.text.startsWith(firstWindow))).toBe(true);
    expect(results.at(-1)?.text).toBe(correctedWindow + finalWindow);
  });
  it('does not start recording after cancellation during permission access', async () => {
    let allow!: (value: boolean) => void;
    const { service, spawn, events } = fixture(new Promise(resolve => { allow = resolve; }));
    const started = service.start('one', 'zh-CN', events);
    service.cancel('one'); allow(true); await started;
    expect(spawn).not.toHaveBeenCalled();
  });
  it('isolates stale stop requests and callbacks from a replacement session', async () => {
    const { service, child, events } = fixture();
    await service.start('one', 'zh-CN', events);
    service.cancel('one');
    expect(events).toHaveBeenCalledWith({ sessionId: 'one', type: 'cancelled' });
    const next = fixture();
    // A stop with the previous id must not interrupt a new owner.
    await next.service.start('two', 'zh-CN', next.events);
    next.service.stop('one');
    expect(next.child.stdin.read()).toBeNull();
    child.stdout.write('{"type":"result","text":"old","is_final":true}\n');
    expect(events.mock.calls.some(([event]) => event.type === 'result')).toBe(false);
    next.service.cancel();
  });
  it('reports permission denial and unexpected helper exit without success', async () => {
    const denied = fixture(Promise.resolve(false));
    await denied.service.start('one', 'zh-CN', denied.events);
    expect(denied.spawn).not.toHaveBeenCalled();
    expect(denied.events).toHaveBeenCalledWith(expect.objectContaining({ type: 'error', code: 'microphone_permission_denied' }));
    const broken = fixture();
    await broken.service.start('two', 'zh-CN', broken.events);
    broken.child.emit('close', 1);
    expect(broken.events).toHaveBeenCalledWith(expect.objectContaining({ type: 'error' }));
  });
});
