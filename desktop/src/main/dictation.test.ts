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
