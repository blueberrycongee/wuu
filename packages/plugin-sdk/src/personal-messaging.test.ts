import test from 'node:test';
import assert from 'node:assert/strict';
import type { RuntimeHost } from './index.js';
import { personalMessagingChannel, requirePersonalMessagingChannel } from './personal-messaging.js';

test('selected channel adapter preserves native source and uses host admission gateways', async () => {
  const calls: unknown[] = [];
  const host = { call: async (_method: string, input: any) => { calls.push(input); return input.service === 'host.personal' ? {ok:true,value:{accepted:true}} : {id:'enrollment'}; } } as unknown as RuntimeHost;
  const binding = {platform:'slack' as const,tenant_id:'T1',sender_id:'U1',conversation_id:'D1',enrollment_id:'enrollment'};
  const source = {...binding,app_id:'A1',thread_id:'1',message_id:'1',event_id:'Ev1',text:'literal message'};
  const input = {request_id:'native-request',prompt:source.text,channel_source:source};
  const channel = personalMessagingChannel(host);
  assert.deepEqual(await channel.call(binding,'agents.send',input),{accepted:true});
  assert.deepEqual(calls[0],{service:'host.personal',method:'call',params:{operation:'channel.call',input:{binding,method:'agents.send',input}}});
  await channel.readOwner('enrollment');
  assert.deepEqual(calls[1],{service:'host.execution.authorization',method:'call',params:{operation:'read-owner',params:{id:'enrollment'}}});
  assert.deepEqual(requirePersonalMessagingChannel().map(value=>value.name),['host.personal','host.execution.authorization']);
});

test('channel gateway refusal is not an accepted input', async () => {
  const host = {call:async()=>({ok:false,error:{message:'not selected'}})} as unknown as RuntimeHost;
  await assert.rejects(personalMessagingChannel(host).call({platform:'slack',tenant_id:'T',sender_id:'U',conversation_id:'D',enrollment_id:'e'},'agents.history',{}),/not selected/);
});
