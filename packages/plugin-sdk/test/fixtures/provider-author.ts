import {
  type PersonalExecutionFacts, PERSONAL_AUTHORITY_METHODS,
  type RuntimePlugin, credentialStore, configurationStore, pluginSettingsStore,
  artifactStore, dataIndex, queryData, authorize, acquireAuthorization, releaseAuthorization, authorizationServiceDescriptor, managedSessions, projectCoordination,
  CREDENTIAL_STORE_DESCRIPTOR, CONFIG_SOURCE_DESCRIPTOR, PROCESS_SANDBOX_SERVICE_DESCRIPTOR, type ProcessSandboxRequest, type ProcessSandboxResult,
  PLUGIN_SETTINGS_STORE_DESCRIPTOR, ARTIFACT_STORE_DESCRIPTOR, DATA_QUERY_DESCRIPTOR,
  MANAGED_SESSION_DESCRIPTOR, PROJECT_COORDINATION_DESCRIPTOR,
  documentStore, type StateImportResult, type StateCompaction, type RuntimeHost,
  type StateExportPage, type StateExportPageRequest,
  type PluginGenerationApi, type DataLatestContextResult, type DataQueryResult, type DataAppendTurn, type DataAppendTurnResult,
  type ManagedSessionCreate, type CredentialCompareExchange, type ConfigSourceView,
  executionAuthorization, EXECUTION_AUTHORIZATION_DESCRIPTOR, type ExecutionAuthorizationScope,
  type ArtifactDeleteThreadRequest, type ArtifactDeleteThreadResult, type StateTransfer,
} from '@wuu/plugin-sdk';

// The document owner adds these to persistence.ts; the public barrel must
// expose them in a packed author project without a private subpath import.
async function maintain(host: RuntimeHost) {
  const store = documentStore(host);
  const compacted: StateCompaction = await store.compact();
  const imported: StateImportResult = await store.import(await store.export());
  const query: StateExportPageRequest = { section: 'documents', target_bytes: 1024, max_items: 2 };
  const page: StateExportPage = await store.exportPage(query);
  const receipts = await store.exportPage({
    section: 'receipts', at_revision: page.revision, generation: page.generation,
    target_bytes: 1024, max_items: 2,
  });
  return {compacted, imported, page, receipts};
}
void maintain;

async function desktopConsumer(api: PluginGenerationApi) {
  const facts = await api.invokeService<DataQueryResult>({
    service: 'host.data.query', method: 'call', params: {thread_id: 'worker'},
  }, {workspaceId: 'workspace', compositionProfile: 'independent-profile', signal: api.signal});
  void api.invokeRuntime('refresh', {}, {userSpaceId: 'chosen-space', productDomain: 'personal', compositionProfile: 'independent-profile', signal: api.signal});
  // @ts-expect-error The host supplies execution identity, never the renderer.
  api.invokeService({service:'host.data.query',method:'call',execution_id:'another-owner'});
  // @ts-expect-error Host admission cannot be supplied in renderer request JSON.
  api.invokeService({service:'execution.authorization',method:'issue',admission:{version:1}});
  return facts.events?.map(event => event.type);
}
void desktopConsumer;

// Compile against the package entry, including descriptors needed by providers.
const descriptors = [authorizationServiceDescriptor({effect_fence:true}), EXECUTION_AUTHORIZATION_DESCRIPTOR, CREDENTIAL_STORE_DESCRIPTOR, CONFIG_SOURCE_DESCRIPTOR, PROCESS_SANDBOX_SERVICE_DESCRIPTOR,
  PLUGIN_SETTINGS_STORE_DESCRIPTOR, ARTIFACT_STORE_DESCRIPTOR, DATA_QUERY_DESCRIPTOR,
  MANAGED_SESSION_DESCRIPTOR, PROJECT_COORDINATION_DESCRIPTOR];
const create: ManagedSessionCreate = {
  request_id: 'create-1', visibility: 'plugin', context_source: 'fresh',
  owner: 'coordinator', source: 'project', session_id: 'worker',
  initial_input: { ClientID: 'initial-1', SessionID: 'worker', Content: 'inspect', Wake: true },
};
const invalidCAS: CredentialCompareExchange = {
  namespace: 'auth', key: 'token', expected: null,
  // @ts-expect-error Credentials use base64 bytes, never JSON numbers.
  value: 42,
};
void invalidCAS;
const incompleteBatch: DataAppendTurn = { thread_id: 'thread', turn_id: 'turn', events: [
  // @ts-expect-error Append events require scope and JSON data, unlike sparse query envelopes.
  { type: 'turn' },
] };
void incompleteBatch;

export default {
  initialize() { return { protocol_version: 3, required_services: descriptors.map(({name, version}) => ({
    name, major_version: Number(version.split('.')[0]), required: true,
  })) }; },
  async invokeService(params, host) {
    const confinement: ProcessSandboxRequest = {argv:['/bin/sh','-c','true'],policy:{mode:'workspace-write',writable_roots:['/workspace'],protected_paths:['/workspace/private']}};
    const confined = await host.call('host.service.call', {service:'sandbox.process',method:'confine',params:confinement}) as ProcessSandboxResult;
    const protectedPathsEnforced: boolean | undefined = confined.protected_paths_enforced;
    const grants = executionAuthorization(host);
    const policy = await grants.describe();
    const scope: ExecutionAuthorizationScope = {kind:'exact-run',authority:{service:'task.authority',major:1,provider:'task-provider',subject:'task-subject',fingerprint:'task-fingerprint'},bounds:{model_providers:['model-provider'],models:['model'],permission_modes:['read_only'],max_depth:1},depth:0,consumer:'author',user_space_id:'user',product_domain:'personal',task_id:'task',run_id:'run',session_id:'worker',context_digest:'digest',revision:1};
    const granted = await grants.issue({request_id:'grant-once',scope,capabilities:[{action:'user/session/send',resources:[{kind:'session',id:'worker'}]}],expires_at:'2099-01-01T00:00:00Z'});
    const checked = await grants.validate({reference:granted.reference,scope,action:'user/session/send',resources:[{kind:'session',id:'worker'}]});
    const revoked = await grants.revoke({reference:granted.reference,scope});
    let grantUncertain: unknown;
    try { await grants.issue({request_id:'grant-uncertain',scope,capabilities:granted.capabilities,expires_at:granted.expires_at}); }
    catch (error) { grantUncertain = (error as {code:string}).code; }
    const credentials = credentialStore(host);
    const absent = await credentials.get({namespace:'auth',key:'token'});
    const swapped = await credentials.compareExchange({namespace:'auth',key:'token',expected:absent.value,value:''});
    const empty = await credentials.get({namespace:'auth',key:'token'});
    let errorCode: unknown;
    try { await credentials.set({namespace:'auth',key:'token',value:'eA=='}); }
    catch (error) { errorCode = (error as {code:string}).code; }
    const config: ConfigSourceView = await configurationStore(host).read();
    // Missing source bytes remain null; callers must narrow before decoding.
    // @ts-expect-error A source response is not always a base64 string.
    const unsafeDecode: string = config.source;
    void unsafeDecode;
    const decodedSource = config.source === null ? null : atob(config.source);
    const changed = await configurationStore(host).compareExchange({expected_revision:config.revision,source:config.source});
    const emptyConfig = await configurationStore(host).read();
    const emptySource = emptyConfig.source === null ? null : atob(emptyConfig.source);
    const documents = documentStore(host);
    const inspection = await documents.acquireInspection({id:'guard-one',namespaces:['records']});
    await documents.releaseInspection({id:inspection.id,namespaces:inspection.namespaces});
    const settingsStore = pluginSettingsStore(host);
    const settingsInspection = await settingsStore.acquireInspection({id:'guard-two',subject:'author',scope:'user'});
    await settingsStore.releaseInspection({id:settingsInspection.id,subject:'author',scope:'user'});
    const settings = await settingsStore.read({subject:'author',scope:'workspace'});
    const transfer: StateTransfer = { id: 'move-1', source_subject_id: 'old-owner', target_subject_id: 'author', key_prefix: 'records.', scope: 'user' };
    await settingsStore.checkStateTransfer(transfer);
    await settingsStore.transferState(transfer);
    let transferUnsupported: unknown, transferUncertain: unknown;
    try { await settingsStore.transferState({...transfer, id: 'unsupported'}); }
    catch (error) { transferUnsupported = (error as {code:string}).code; }
    try { await settingsStore.transferState({...transfer, id: 'uncertain'}); }
    catch (error) { transferUncertain = (error as {code:string}).code; }
    const artifacts = artifactStore(host);
    const artifactInput = params.params as { artifact_thread: string };
    const artifactAddress = {thread_id:artifactInput.artifact_thread,uri:'artifact:one',max_bytes:8};
    const artifact = await artifacts.read(artifactAddress);
    const deletion: ArtifactDeleteThreadRequest = {thread_id:artifactInput.artifact_thread};
    const deleted: ArtifactDeleteThreadResult = await artifacts.deleteThread(deletion);
    const deletedAgain = await artifacts.deleteThread(deletion);
    let artifactUncertain: unknown, deletedRead: unknown;
    try { await artifacts.deleteThread({thread_id:'uncertain-thread'}); }
    catch (error) { artifactUncertain = (error as {code:string}).code; }
    try { await artifacts.read(artifactAddress); }
    catch (error) { deletedRead = (error as {code:string}).code; }
    const indexInput = params.params as { index_thread: string; index_turn: string; index_payload: string };
    const index = dataIndex(host);
    const batch: DataAppendTurn = {
      thread_id: indexInput.index_thread, turn_id: indexInput.index_turn,
      events: ['turn', 'future_fact', 'final'].map(type => ({
        type, thread_id: indexInput.index_thread, turn_id: indexInput.index_turn,
        created_at: '2026-10-24T00:00:00Z', data: { text: indexInput.index_payload, nested: { zero: 0, absent: null } },
      })),
    };
    const appended: DataAppendTurnResult = await index.appendTurn(batch);
    const duplicate = await index.appendTurn(batch);
    let indexConflict: unknown, indexUncertain: unknown;
    try { await index.appendTurn({...batch, events: batch.events.map(event => ({...event, data: { changed: true }}))}); }
    catch (error) { indexConflict = (error as {code:string}).code; }
    try { await index.appendTurn({...batch, turn_id: 'uncertain', events: batch.events.map(event => ({...event, turn_id: 'uncertain'}))}); }
    catch (error) { indexUncertain = (error as {code:string}).code; }
    const facts = await index.query({thread_id:batch.thread_id,types:['future_fact'],turn_id:batch.turn_id,limit:1});
    const emptyFacts = await queryData(host, {thread_id:batch.thread_id});
    const latestContext: DataLatestContextResult = await index.latestContext({thread_id:batch.thread_id});
    let contextLimit: unknown;
    try { await index.latestContext({thread_id:'oversized-context'}); }
    catch(error) { contextLimit = (error as {code:string}).code; }
    const permission = await authorize(host, {cwd:'/workspace',permission_mode:'read_only',tool:{name:'read',kind:'file',read_only:true,concurrency_safe:true,destructive:false}});
    const effectFence=await acquireAuthorization(host,{id:'effect-one',request:{cwd:'/workspace',permission_mode:'read_only',tool:{name:'write',kind:'file',read_only:false,concurrency_safe:false,destructive:false}}});
    await releaseAuthorization(host,{id:effectFence.id});
    const sessions = managedSessions(host);
    const launch = params.params as { launch_prompt: string; launch_intent: string };
    const created = await sessions.create({...create, launch: {
      kind: 'handoff', intent: launch.launch_intent, prompt: launch.launch_prompt,
    }});
    const admitted = await sessions.submit({session_id:created.session_id,message:{Role:'user',Content:'inspect',ClientID:'input-2'},allow_start:true});
    const admission = await projectCoordination(host).admission();
    const executionFacts: PersonalExecutionFacts = {session_id:'packed-session',agent_id:'packed-agent',main_session_id:'packed-session',active:true};
    return {executionFacts,authorityMethods:PERSONAL_AUTHORITY_METHODS,effectFence,stewardship:params.storage_stewardship,grantPolicy:policy,serviceAdmission:params.admission,inspection,settingsInspection,granted,checked,revoked,grantUncertain,protectedPathsEnforced,absent,swapped,empty,errorCode,decodedSource,emptySource,changed,settings,transferUnsupported,transferUncertain,artifact,deleted,deletedAgain,artifactUncertain,deletedRead,facts,emptyFacts,latestContext,contextLimit,appended,duplicate,indexConflict,indexUncertain,permission,created,admitted,admission,descriptorCount:descriptors.length};
  },
} satisfies RuntimePlugin;
