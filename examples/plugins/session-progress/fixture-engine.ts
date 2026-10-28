/** Acceptance-only independent engine. Explicit client control gates make live
 * progress observable without model inference, filesystem polling or sleeps. */
import {
  PLUGIN_CLIENT_REQUEST_CAPABILITY, engineGateway, engineServiceDescriptor, requireEngineGateway,
  type EngineBinding, type EngineDescriptor, type EngineRunParams, type EngineSessionParams,
  type EngineEventV1, type RuntimePlugin,
} from '@wuu/plugin-sdk';

interface Session {
  binding: EngineBinding;
  tag?: string;
  emit?: (event: EngineEventV1) => Promise<unknown>;
  complete?: () => void;
  abort?: AbortController;
  done?: Promise<void>;
}
export function createProgressEngine(): RuntimePlugin {
  const sessions = new Map<string, Session>();
  let sequence = 0;
  return {
    concurrentServices: ['engine.wuu'],
    concurrentCapabilities: [PLUGIN_CLIENT_REQUEST_CAPABILITY],
    initialize: () => ({protocol_version: 3, lifecycle_version: 2,
      capabilities: [{id: PLUGIN_CLIENT_REQUEST_CAPABILITY, kind: 'decision', version: 1}],
      provided_services: [engineServiceDescriptor('wuu')], required_services: [requireEngineGateway()]}),
    async shutdown() {
      for (const session of sessions.values()) session.abort?.abort();
      await Promise.all([...sessions.values()].map(session => session.done));
      sessions.clear();
    },
    async invokeCapability(params) {
      const request = params.input as {method: string; input: {tag: string}};
      const session = [...sessions.values()].find(session => session.tag === request.input.tag && session.emit);
      if (!session?.emit) throw new Error('No active fixture run with this tag');
      if (request.method === 'large-text') {
        await session.emit({version: 1, type: 'content_delta', content: '界'.repeat(3000)});
        return {output: {result: {emitted: true}}};
      }
      if (request.method === 'burst') {
        for (let index = 0; index < 40; index++) {
          const tool_call = {id: `burst-${index}`, name: `bounded_tool_${index}`, arguments: '{"secret":"PRIVATE_TOOL_ARGS"}'};
          await session.emit({version: 1, type: 'tool_use_start', tool_call});
          await session.emit({version: 1, type: 'tool_use_end', tool_call, tool_result: 'PRIVATE_TOOL_RESULT'});
        }
        return {output: {result: {emitted: true}}};
      }
      if (request.method === 'complete') {
        await session.emit({version: 1, type: 'tool_use_end', tool_call: {id: `tool-${session.tag}`, name: 'fixture_tool'}, tool_result: 'PRIVATE_TOOL_RESULT'});
        session.complete?.();
        return {output: {result: {released: true}}};
      }
      throw new Error('Unknown fixture control');
    },
    async invokeService({method, params}, host, execution) {
      if (method === 'descriptor') return {id: 'wuu', version: '1.0.0', enabled: true, requires_host_model: false, capabilities: ['canonical-history']} satisfies EngineDescriptor;
      if (method === 'describe') return {binary_ok: true, display_name: 'Progress fixture', protocol: 'plugin', enabled: true};
      if (method === 'models') return {models: []};
      if (method === 'bind') {
        const id = `progress-engine-${++sequence}`;
        sessions.set(id, {binding: params as EngineBinding}); return {session_id: id};
      }
      const id = (params as EngineSessionParams).session_id;
      const session = sessions.get(id);
      if (!session) throw new Error('Fixture session is unavailable');
      if (method === 'interrupt') {session.abort?.abort(); return {};}
      if (method === 'close') {session.abort?.abort(); await session.done; sessions.delete(id); return {};}
      if (method !== 'run' || session.done) throw new Error('Fixture session is not ready');
      const abort = new AbortController();
      const signal = AbortSignal.any([execution.signal, abort.signal]);
      session.abort = abort;
      let settle!: () => void;
      session.done = new Promise<void>(resolve => {settle = resolve;});
      const prompt = (params as EngineRunParams).messages.findLast(message => message.role === 'user')?.content ?? '';
      session.tag = prompt.endsWith('other') ? 'other' : prompt.endsWith('later') ? 'later' : 'active';
      const tag = session.tag;
      const gateway = engineGateway(host, session.binding.gateway_id);
      session.emit = event => gateway.emit(event);
      const gate = new Promise<void>(resolve => {
        session.complete = resolve;
        if (signal.aborted) resolve(); else signal.addEventListener('abort', () => resolve(), {once: true});
      });
      try {
        await gateway.emit({version: 1, type: 'thinking_delta', content: 'PRIVATE_REASONING'});
        await gateway.emit({version: 1, type: 'tool_use_start', tool_call: {id: `tool-${tag}`, name: 'fixture_tool', arguments: '{"secret":"PRIVATE_TOOL_ARGS"}'}});
        await gateway.emit({version: 1, type: 'content_delta', content: `public-progress:${tag}`});
        await gate;
        if (signal.aborted) throw new Error('Fixture run cancelled');
        const content = `finished:${tag}`;
        await gateway.emit({version: 1, type: 'content_delta', content});
        return {result: {version: 1, content, finish_reason: 'stop', new_messages: [{role: 'assistant', content}], durable_new_messages: [{role: 'assistant', content}]}};
      } finally {
        session.emit = undefined; session.complete = undefined; session.abort = undefined; session.done = undefined; settle();
      }
    },
  };
}
