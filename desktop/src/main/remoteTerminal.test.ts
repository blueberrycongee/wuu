import { it,expect,vi } from 'vitest';
import type { TerminalSessionManager } from './terminalSessions';
import { requestRemoteTerminal } from './remoteTerminal';
it('binds terminal operations to the authenticated connection owner',()=>{
 const service={startInContext:vi.fn(),write:vi.fn(),resize:vi.fn(),stop:vi.fn()} as unknown as TerminalSessionManager;
 const context={kind:'no_project' as const,cwd:'/workspace'};
 expect(()=>requestRemoteTerminal(service,context,17,'desktop/terminal/start',{params:{cwd:'/private'}})).toThrow();
 requestRemoteTerminal(service,context,17,'desktop/terminal/write',{id:'term-1',data:'pwd\n'});
 expect(service.write).toHaveBeenCalledWith('term-1','pwd\n',17);
 expect(()=>requestRemoteTerminal(service,context,17,'desktop/terminal/write',{id:'term-1',data:null})).toThrow();
});
