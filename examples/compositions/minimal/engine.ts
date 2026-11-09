/** A complete deterministic engine, not an adapter to a bundled loop or model.
 * No inference, tools, credentials or network endpoint is needed. */
import {
  engineGateway, engineServiceDescriptor, requireEngineGateway,
  type EngineBinding, type EngineDescriptor, type EngineRunParams, type EngineSessionParams, type RuntimePlugin,
} from '@wuu/plugin-sdk';

interface Session {
  binding: EngineBinding;
  abort?: AbortController;
  done?: Promise<void>;
  closed: boolean;
}

export function createEchoEngine(label = 'echo', record: (event: string, data?: Record<string, unknown>) => void = () => {}): RuntimePlugin {
  const sessions = new Map<string, Session>();
  let sequence = 0;
  return {
    // Independent runs must not serialize behind a held conversation. State is
    // session-local; close waits for the same session's admitted run to settle.
    concurrentServices: ['engine.wuu'],
    initialize: () => ({
      protocol_version: 3, lifecycle_version: 2,
      provided_services: [engineServiceDescriptor('wuu')],
      required_services: [requireEngineGateway()],
    }),
    activate() { record('activate', {label}); },
    async shutdown() {
      for (const session of sessions.values()) session.abort?.abort();
      await Promise.all([...sessions.values()].map(session => session.done));
      sessions.clear();
      record('shutdown', {label});
    },
    async invokeService({method, params}, host, execution) {
      if (method === 'descriptor') return {id: 'wuu', version: '1.0.0', enabled: true, requires_host_model: false, capabilities: ['canonical-history']} satisfies EngineDescriptor;
      if (method === 'describe') return {binary_ok: true, display_name: 'Independent echo', protocol: 'plugin', enabled: true};
      if (method === 'models') return {models: []};
      if (method === 'bind') {
        const id = `echo-${++sequence}`;
        sessions.set(id, {binding: params as EngineBinding, closed: false});
        record('bind', {id, label});
        return {session_id: id};
      }
      const id = (params as EngineSessionParams).session_id;
      const session = sessions.get(id);
      if (!session) throw new Error('Echo session is unavailable');
      if (method === 'interrupt') { session.abort?.abort(); return {}; }
      if (method === 'close') {
        session.closed = true;
        session.abort?.abort();
        await session.done;
        sessions.delete(id);
        record('close', {id, label});
        return {};
      }
      if (method !== 'run') throw new Error(`Echo does not support ${method}`);
      if (session.closed || session.done) throw new Error('Echo session is closed or running');
      const abort = new AbortController();
      const signal = AbortSignal.any([execution.signal, abort.signal]);
      session.abort = abort;
      let settled!: () => void;
      session.done = new Promise<void>(resolve => {settled = resolve;});
      const messages = (params as EngineRunParams).messages;
      const prompt = messages.findLast(message => message.role === 'user')?.content ?? '';
      const gateway = engineGateway(host, session.binding.gateway_id);
      record('run', {id, label, prompt});
      try {
        // An explicit interactive wait makes cancellation and generation
        // retention observable without timers or an external model provider.
        if (prompt === '/wait') {
          await gateway.emit({version: 1, type: 'content_delta', content: `${label}:waiting`});
          await new Promise<void>(resolve => {
            if (signal.aborted) resolve();
            else signal.addEventListener('abort', () => resolve(), {once: true});
          });
          record('canceled', {id, label});
          throw new Error('Echo run canceled');
        }
        if (signal.aborted) throw new Error('Echo run canceled');
        const content = `${label}:${prompt}`;
        await gateway.emit({version: 1, type: 'content_delta', content});
        return {result: {version: 1, content, finish_reason: 'stop',
          new_messages: [{role: 'assistant', content}],
          durable_new_messages: [{role: 'assistant', content}],
        }};
      } finally {
        session.abort = undefined;
        session.done = undefined;
        settled();
        record('settled', {id, label});
      }
    },
  };
}

export default createEchoEngine();
