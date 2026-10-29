/** Independent acceptance authority. No Wuu internals, native provider access or
 * owner synthesis. File persistence is a single-writer fixture, not production HA. */
import {appendFile, readFile, rename, writeFile} from 'node:fs/promises';
import {createHash, randomUUID} from 'node:crypto';
import {isDeepStrictEqual} from 'node:util';
import {
  EXECUTION_AUTHORIZATION_DESCRIPTOR, identityAuthenticationDescriptor, resourceAuthorizationProvider,
  type ExecutionAuthorizationGrant as Grant, type ExecutionAuthorizationIssue as Issue,
  type ExecutionAuthorizationReference as Reference, type ExecutionAuthorizationScope as Scope,
  type PrincipalIdentity, type ResourceReference, type RuntimePlugin, type ServiceInvokeParams,
} from '@wuu/plugin-sdk';
import {createAccessProvider} from './access.js';

interface Control {account_enabled: boolean; fail_grants?: boolean; fail_policy?: boolean; revoked_tokens: string[]; revoked_grants: string[]; expired_grants: string[]; deny_actions: string[]; deny_plugin_methods: string[]; grants: never[];}
interface GrantRecord {grant: Grant; request: Issue; request_digest: string; parent?: Reference; revoked: boolean;}
interface Database {records: Record<string, GrantRecord>; requests: Record<string, string>;}
const digest=(v: string)=>createHash('sha256').update(v).digest('hex');
const same=isDeepStrictEqual;
const subset=(a: readonly string[], b: readonly string[])=>a.every(value=>b.includes(value));
const resourceEqual=(a: ResourceReference,b: ResourceReference)=>a.kind===b.kind&&(a.id??'')===(b.id??'');
const permits=(grant: Grant,action: string,resources: readonly ResourceReference[])=>resources.length>0&&grant.capabilities.some(c=>c.action===action&&resources.every(r=>c.resources.some(a=>resourceEqual(a,r))));
function refused(message: string): never {throw Object.assign(new Error(message),{code:'execution_authorization_required'});}

export function createGrantProvider(options:{id:string;issuer:string;jwksPath:string;statePath:string;databasePath:string;auditPath:string;identity:PrincipalIdentity}): RuntimePlugin {
  const control=async()=>JSON.parse(await readFile(options.statePath,'utf8')) as Control;
  const audit=async(event:string, data:unknown)=>appendFile(options.auditPath,JSON.stringify({at:new Date().toISOString(),pid:process.pid,event,data})+'\n');
  const load=async()=>{try{return JSON.parse(await readFile(options.databasePath,'utf8')) as Database;}catch(error){if((error as NodeJS.ErrnoException).code==='ENOENT')return {records:{},requests:{}};throw error;}};
  const save=async(db:Database)=>{const temporary=options.databasePath+'.tmp';await writeFile(temporary,JSON.stringify(db),{mode:0o600});await rename(temporary,options.databasePath);};
  const access=createAccessProvider(options);
  const policy=resourceAuthorizationProvider(async p=>{
    const state=await control();
    await audit('policy',{...p});
    if(state.fail_policy)throw new Error('independent resource policy unavailable');
    if(p.action==='plugin/client/request'||p.action==='plugin/client/cancel'){
      const resource=(kind:string)=>p.resources.filter(r=>r.kind===kind);
      const value=(kind:string)=>resource(kind).length===1?resource(kind)[0]!.id:undefined;
      const subject=value('plugin-subject'),method=value('plugin-method');
      const methods: {[subject:string]:string[]}={
        'plugin:bundled:personal':['agents.ensure_default','tasks.create','tasks.get','tasks.history'],
        'plugin:user:fixture.task-consumer':['prepare','issue','effect','background','read','send','inspect','revoke','cancel-background','new-run','forged-issue'],
      };
      const scoped=Boolean(subject&&method&&methods[subject]?.includes(method)&&!state.deny_plugin_methods.includes(method)&&value('plugin-fingerprint')?.match(/^[a-f0-9]{64}$/)&&value('plugin-generation')&&value('plugin-capability')==='plugin.client.request'&&resource('plugin-service').length===0&&value('user-space')==='local');
      return {allow:!p.owner&&same(p.principal,options.identity)&&state.account_enabled&&scoped,reason:'exact synthetic plugin operation policy'};
    }
    const known=p.action==='initialize'||p.action==='plugin/client/request'||p.action==='plugin/client/cancel'||p.action.startsWith('event:')||p.action.startsWith('request:')||p.action.startsWith('personal/')||p.action.startsWith('user/session/')||p.action.startsWith('execution/grant/')||p.action==='service/fixture.effects/record';
    return {allow:!p.owner&&same(p.principal,options.identity)&&state.account_enabled&&known&&!state.deny_actions.includes(p.action),reason:'independent synthetic account policy'};
  });
  function active(db:Database,state:Control,ref:Reference,seen=new Set<string>()):GrantRecord {
    if(ref.provider!==options.id||seen.has(ref.id))refused('unknown authority or cyclic lineage');
    const record=db.records[ref.id];
    if(!record||record.revoked||state.revoked_grants.includes(ref.id)||state.expired_grants.includes(ref.id)||Date.parse(record.grant.expires_at)<=Date.now())refused('execution grant expired or revoked');
    if(!state.account_enabled||!same(record.grant.principal,options.identity))refused('durable account disabled');
    if(record.parent){seen.add(ref.id);active(db,state,record.parent,seen);}
    return record;
  }
  function admit(call:ServiceInvokeParams, action:string):PrincipalIdentity {
    const a=call.admission;
    if(!a||a.version!==1||a.action!==action||typeof a.input_json!=='string'||digest(a.input_json)!==a.input_digest||!same(JSON.parse(a.input_json),call.params)||!same(a.principal,options.identity))refused('host-attested authenticated admission required');
    // Empty authority is honest: the host did not attest local-installation.
    // This independent provider owns validity of this exact external account.
    if(a.authority!==''&&a.authority!=='task-lineage')refused('unexpected authority source');
    return a.principal;
  }
  function bounded(scope:Scope) {
    if(!scope||!['task-lineage','exact-run'].includes(scope.kind)||!scope.consumer||!scope.task_id||!scope.user_space_id||scope.product_domain!=='personal'||!scope.context_digest||scope.revision<1||!Number.isInteger(scope.depth)||!Number.isInteger(scope.bounds.max_depth)||scope.depth<0||scope.depth>scope.bounds.max_depth||scope.bounds.max_depth>8||!scope.authority.provider||!scope.authority.subject||!scope.authority.fingerprint)refused('invalid bounded scope');
    if([scope.bounds.model_providers,scope.bounds.models,scope.bounds.permission_modes].some(values=>!values.length||values.some(v=>!v)))refused('missing execution bounds');
    if(scope.kind==='exact-run'?(!scope.run_id||!scope.session_id):(scope.run_id!==''||scope.session_id!==''))refused('invalid execution extent');
  }
  return {
    concurrentServices:['identity.authentication','resource.authorization'],
    initialize:()=>({protocol_version:3,lifecycle_version:2,provided_services:[identityAuthenticationDescriptor(),policy.descriptor,EXECUTION_AUTHORIZATION_DESCRIPTOR,{name:"fixture.effects",version:"1.0.0",methods:[{name:"record",input_schema:"fixture.effects.record.input.v1",output_schema:"fixture.effects.record.output.v1"}]}]}),
    async activate(host,lifecycle){await access.activate?.(host,lifecycle);await audit('activate',{provider:options.id});},
    async shutdown(){await access.shutdown?.();},
    async invokeService(call,host,execution) {
      if(call.service==='identity.authentication'){
        const state=await control();if(!state.account_enabled)throw new Error('account disabled');
        const value=await access.invokeService!(call,host,execution);await audit('authenticated',value);return value;
      }
      if(call.service==='resource.authorization')return policy.invoke(call);
      if(call.service==='fixture.effects'){await audit('effect',call.params);return {recorded:true,...call.params as object};}
      if(call.service!=='execution.authorization')throw new Error('unsupported authority service');
      const state=await control();await audit('grant-call',{method:call.method,params:call.params,admission:call.admission});
      if(state.fail_grants)throw new Error('independent execution authority unavailable');
      if(call.method==='describe')return {default_lifetime_seconds:3600,max_lifetime_seconds:7200,authority_kinds:['fixture-account'],scope_kinds:['task-lineage','exact-run']};
      const db=await load();
      if(call.method==='issue') {
        const input=call.params as Issue;const who=admit(call,input.parent?'execution/grant/derive':'execution/grant/issue');
        bounded(input.scope);
        if(!state.account_enabled||!input.request_id||!input.capabilities.length||input.capabilities.some(c=>!c.action||!c.resources.length)||!(Date.parse(input.expires_at)>Date.now())||Date.parse(input.expires_at)>Date.now()+7200_000)refused('unbounded grant issuance');
        if(input.parent){
          const parent=active(db,state,input.parent).grant,p=parent.scope,c=input.scope,l=call.admission!.lineage as any;
          if(p.kind!=='task-lineage'||!same(parent.principal,who)||!same(p.authority,c.authority)||!l||!same(l.authority,p.authority)||l.parent_task_id!==p.task_id||l.parent_revision!==p.revision||l.task_id!==c.task_id||l.run_id!==c.run_id||l.session_id!==c.session_id||l.revision!==c.revision||l.context_digest!==c.context_digest||l.depth!==c.depth||c.depth!==p.depth+1||c.consumer!==p.consumer||c.user_space_id!==p.user_space_id||c.product_domain!==p.product_domain||c.bounds.max_depth>p.bounds.max_depth||!subset(c.bounds.models,p.bounds.models)||!subset(c.bounds.model_providers,p.bounds.model_providers)||!subset(c.bounds.permission_modes,p.bounds.permission_modes)||Date.parse(input.expires_at)>Date.parse(parent.expires_at))refused('child scope exceeds parent');
          if(c.kind==='exact-run'&&(!c.bounds.models.includes(l.model)||!c.bounds.model_providers.includes(l.model_provider)||!c.bounds.permission_modes.includes(l.permission_mode)))refused('attested child execution exceeds bounds');
          for(const capability of input.capabilities){const resources=capability.resources.map(r=>r.kind==='task'&&r.id===c.task_id||c.kind==='exact-run'&&r.kind==='session'&&r.id===c.session_id?{kind:'task',id:p.task_id}:r);if(!permits(parent,capability.action,resources))refused('expanded child capability');}
        }
        const key=digest(JSON.stringify([who,input.scope.consumer,input.request_id])),requestDigest=digest(JSON.stringify(input));
        const prior=db.requests[key];if(prior){const saved=active(db,state,{provider:options.id,id:prior});if(saved.request_digest!==requestDigest)refused('request identity conflict');return saved.grant;}
        const reference={provider:options.id,id:randomUUID()},grant={reference,principal:who,scope:input.scope,capabilities:input.capabilities,expires_at:input.expires_at};
        db.records[reference.id]={grant,request:input,request_digest:requestDigest,parent:input.parent,revoked:false};db.requests[key]=reference.id;await save(db);await audit('issued',grant);return grant;
      }
      const input=call.params as {reference:Reference;scope?:Scope;consumer?:string;action?:string;resources?:ResourceReference[]};
      if(call.method==='validate'){
        try{const record=active(db,state,input.reference);if(!same(record.grant.scope,input.scope)||!permits(record.grant,input.action!,input.resources!))refused('scope/action/resource mismatch');return {valid:true,principal:record.grant.principal,scope:record.grant.scope,expires_at:record.grant.expires_at};}
        catch(error){return {valid:false,reason:(error as Error).message};}
      }
      const record=active(db,state,input.reference);
      if(call.method==='inspect'){if(input.consumer!==record.grant.scope.consumer)refused('consumer mismatch');return record.grant;}
      if(call.method==='revoke'){const who=admit(call,'execution/grant/revoke');if(!same(who,record.grant.principal)||!same(input.scope,record.grant.scope))refused('revocation scope mismatch');record.revoked=true;await save(db);return {revoked:true};}
      throw new Error('unsupported execution authority method');
    },
  };
}
