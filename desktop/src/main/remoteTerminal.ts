import { resolve } from 'node:path';
import type { RuntimeContext, TerminalSessionStartParams } from '../shared/protocol';
import type { TerminalSessionManager } from './terminalSessions';

export function requestRemoteTerminal(manager: TerminalSessionManager, context: RuntimeContext, owner: number, method: string, input: unknown): unknown {
 const p=(input??{}) as {id?:string;data?:string;cols?:number;rows?:number;params?:TerminalSessionStartParams};
 if(method==='desktop/terminal/start'){
  if(p.params?.cwd && resolve(p.params.cwd)!==resolve(context.cwd))throw new Error('Unknown terminal workspace');
  return manager.startInContext(context,p.params,owner);
 }
 if(typeof p.id!=='string')throw new Error('Terminal id is required');
 if(method==='desktop/terminal/write'){
  if(typeof p.data!=='string'||p.data.length>1024*1024)throw new Error('Invalid terminal input');
  return manager.write(p.id,p.data,owner);
 }
 if(method==='desktop/terminal/resize')return manager.resize(p.id,p.cols??80,p.rows??24,owner);
 if(method==='desktop/terminal/stop')return manager.stop(p.id,owner);
 throw new Error('Unknown terminal operation');
}
