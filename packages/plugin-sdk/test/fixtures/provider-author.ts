import {
  type RuntimePlugin, credentialStore, configurationStore, pluginSettingsStore,
  artifactStore, dataIndex, queryData, authorize, managedSessions, projectCoordination,
  CREDENTIAL_STORE_DESCRIPTOR, CONFIG_SOURCE_DESCRIPTOR,
  PLUGIN_SETTINGS_STORE_DESCRIPTOR, ARTIFACT_STORE_DESCRIPTOR, DATA_QUERY_DESCRIPTOR,
  MANAGED_SESSION_DESCRIPTOR, PROJECT_COORDINATION_DESCRIPTOR,
  documentStore, type StateImportResult, type StateCompaction, type RuntimeHost,
  type StateExportPage, type StateExportPageRequest,
  type PluginGenerationApi, type DataLatestContextResult, type DataQueryResult, type DataAppendTurn, type DataAppendTurnResult,
  type ManagedSessionCreate, type CredentialCompareExchange, type ConfigSourceView,
  type ArtifactDeleteThreadRequest, type ArtifactDeleteThreadResult,
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
  }, {workspaceId: 'workspace', signal: api.signal});
  // @ts-expect-error The host supplies execution identity, never the renderer.
  api.invokeService({service:'host.data.query',method:'call',execution_id:'another-owner'});
  return facts.events?.map(event => event.type);
}
void desktopConsumer;

// Compile against the package entry, including descriptors needed by providers.
const descriptors = [CREDENTIAL_STORE_DESCRIPTOR, CONFIG_SOURCE_DESCRIPTOR,
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
    const settings = await pluginSettingsStore(host).read({subject:'author',scope:'workspace'});
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
    const sessions = managedSessions(host);
    const launch = params.params as { launch_prompt: string; launch_intent: string };
    const created = await sessions.create({...create, launch: {
      kind: 'handoff', intent: launch.launch_intent, prompt: launch.launch_prompt,
    }});
    const admitted = await sessions.submit({session_id:created.session_id,message:{Role:'user',Content:'inspect',ClientID:'input-2'},allow_start:true});
    const admission = await projectCoordination(host).admission();
    return {absent,swapped,empty,errorCode,decodedSource,emptySource,changed,settings,artifact,deleted,deletedAgain,artifactUncertain,deletedRead,facts,emptyFacts,latestContext,contextLimit,appended,duplicate,indexConflict,indexUncertain,permission,created,admitted,admission,descriptorCount:descriptors.length};
  },
} satisfies RuntimePlugin;
