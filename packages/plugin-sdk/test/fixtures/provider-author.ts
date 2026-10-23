import {
  type RuntimePlugin, credentialStore, configurationStore, pluginSettingsStore,
  artifactStore, queryData, authorize, managedSessions, projectCoordination,
  CREDENTIAL_STORE_DESCRIPTOR, CONFIG_SOURCE_DESCRIPTOR,
  PLUGIN_SETTINGS_STORE_DESCRIPTOR, ARTIFACT_STORE_DESCRIPTOR, DATA_QUERY_DESCRIPTOR,
  MANAGED_SESSION_DESCRIPTOR, PROJECT_COORDINATION_DESCRIPTOR,
  documentStore, type StateImportResult, type StateCompaction, type RuntimeHost,
  type ManagedSessionCreate, type CredentialCompareExchange, type ConfigSourceView,
} from '@wuu/plugin-sdk';

// The document owner adds these to persistence.ts; the public barrel must
// expose them in a packed author project without a private subpath import.
async function maintain(host: RuntimeHost) {
  const store = documentStore(host);
  const compacted: StateCompaction = await store.compact();
  const imported: StateImportResult = await store.import(await store.export());
  return {compacted, imported};
}
void maintain;

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

export default {
  initialize() { return { protocol_version: 3 }; },
  async invokeService(_params, host) {
    const credentials = credentialStore(host);
    const absent = await credentials.get({namespace:'auth',key:'token'});
    const swapped = await credentials.compareExchange({namespace:'auth',key:'token',expected:absent.value,value:''});
    const empty = await credentials.get({namespace:'auth',key:'token'});
    let errorCode: unknown;
    try { await credentials.set({namespace:'auth',key:'token',value:'eA=='}); }
    catch (error) { errorCode = (error as {code:string}).code; }
    const config: ConfigSourceView = await configurationStore(host).read();
    const changed = await configurationStore(host).compareExchange({expected_revision:config.revision,source:config.source});
    const settings = await pluginSettingsStore(host).read({subject:'author',scope:'workspace'});
    const artifact = await artifactStore(host).read({thread_id:'worker',uri:'artifact:one',max_bytes:8});
    const facts = await queryData(host, {thread_id:'worker',types:['turn']});
    const permission = await authorize(host, {cwd:'/workspace',permission_mode:'read_only',tool:{name:'read',kind:'file',read_only:true,concurrency_safe:true,destructive:false}});
    const sessions = managedSessions(host);
    const created = await sessions.create(create);
    const admitted = await sessions.submit({session_id:created.session_id,message:{Role:'user',Content:'inspect',ClientID:'input-2'},allow_start:true});
    const admission = await projectCoordination(host).admission();
    return {absent,swapped,empty,errorCode,changed,settings,artifact,facts,permission,created,admitted,admission,descriptorCount:descriptors.length};
  },
} satisfies RuntimePlugin;
