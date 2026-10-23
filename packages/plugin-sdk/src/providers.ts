import type {
  ArtifactImportParams, ImportedArtifact, AuthorizationRequest, AuthorizationDecision,
  RuntimeHost, ServiceDescriptor,
} from './index.js';

export const CREDENTIAL_STORE_SERVICE = 'state.credentials';
export const CONFIG_SOURCE_SERVICE = 'state.config';
export const PLUGIN_SETTINGS_STORE_SERVICE = 'state.plugin-settings';
export const ARTIFACT_STORE_SERVICE = 'state.artifacts';
export const DATA_QUERY_SERVICE = 'host.data.query';

function descriptor(name: string, methods: readonly string[], preparation: readonly string[] = []): ServiceDescriptor {
  return { name, version: '1.0.0', methods: methods.map(method => ({
    name: method, input_schema: `${name}.${method}.input.v1`, output_schema: `${name}.${method}.output.v1`,
    ...(preparation.includes(method) ? { preparation: true } : {}),
  })) };
}
export const CREDENTIAL_STORE_DESCRIPTOR = descriptor(CREDENTIAL_STORE_SERVICE, ['get', 'set', 'delete', 'compare-exchange']);
export const CONFIG_SOURCE_DESCRIPTOR = descriptor(CONFIG_SOURCE_SERVICE, ['read', 'compare-exchange'], ['read']);
export const PLUGIN_SETTINGS_STORE_DESCRIPTOR = descriptor(PLUGIN_SETTINGS_STORE_SERVICE, ['read', 'update', 'read-state', 'update-state'], ['read', 'read-state']);
export const ARTIFACT_STORE_DESCRIPTOR = descriptor(ARTIFACT_STORE_SERVICE, ['import', 'resolve', 'read', 'fork']);
export const DATA_QUERY_DESCRIPTOR: ServiceDescriptor = {
  name: DATA_QUERY_SERVICE, version: '1.0.0', methods: [
    { name: 'call', input_schema: 'host.data.query.input.v1', output_schema: 'host.data.query.output.v1' },
  ],
};

/** Standard base64, as encoded by Go's JSON []byte representation. */
export type Base64Bytes = string;
export interface CredentialEntry { namespace: string; key: string }
export interface CredentialValue {
  /** null is absent; an empty string is a present zero-byte credential. */
  value: Base64Bytes | null;
}
export interface CredentialSet extends CredentialEntry { value: Base64Bytes }
export interface CredentialCompareExchange extends CredentialEntry {
  expected: Base64Bytes | null;
  /** null deletes the entry. */
  value: Base64Bytes | null;
}
export interface CredentialExchangeResult { exchanged: boolean }

/** Source bytes are preserved for editing; config is the validated effective
 * configuration. Both may contain secrets. The config schema is product-owned,
 * not a second configuration schema maintained by this SDK.
 */
export interface ConfigSourceView {
  /** null preserves absent source bytes; an empty string is present zero-byte source. */
  source: Base64Bytes | null;
  revision: string;
  config: Readonly<Record<string, unknown>>;
  /** A durable write succeeded even if applying its overlay then failed. */
  committed?: boolean;
  activation_error?: string;
}
export interface ConfigSourceCompareExchange {
  expected_revision: string;
  /** null supplies zero replacement bytes, not deletion. Native file mode
   * creates/overwrites an empty file; trusted project mode rejects empty JSON.
   */
  source: Base64Bytes | null;
}
export interface PluginSettingsAddress { subject: string; scope: 'user' | 'workspace' }
export interface PluginSettingsDocument {
  schema_version: number;
  plugin_id: string;
  fingerprint?: string;
  values: Record<string, unknown>;
}
export interface PluginStateDocument {
  schema_version: number;
  plugin_id: string;
  values: Record<string, string>;
}
export interface PluginSettingsView { document: PluginSettingsDocument; revision: string }
export interface PluginStateView { document: PluginStateDocument; revision: string }
export interface PluginSettingsUpdate extends PluginSettingsAddress {
  expected_revision: string;
  fingerprint: string;
  /** Complete replacement, not a merge patch. */
  values: Record<string, unknown>;
}
export interface PluginStateUpdate extends PluginSettingsAddress {
  expected_revision: string;
  values: Record<string, string>;
}

/** Scope supplied by an admitted consumer; domain IDs do not grant authority.
 * Ordinary tool authors use importArtifact(host, input) instead.
 */
export interface ArtifactScope {
  thread_id: string;
  execution_id: string;
  plugin_id: string;
  call_id?: string;
  cwd?: string;
}
export interface ArtifactStoreImport { scope: ArtifactScope; input: ArtifactImportParams; present?: boolean }
export interface ArtifactResolve { thread_id: string; uri: string }
export interface ArtifactResolved { artifact: ImportedArtifact; managed: boolean }
export interface ArtifactRead extends ArtifactResolve { max_bytes: number }
export interface ArtifactBytes { data: Base64Bytes; mime_type: string }
export interface ArtifactFork { source_thread_id: string; target_thread_id: string; uri: string }
export interface DataQuery {
  thread_id: string;
  /** Unknown fact types match no events. Empty or absent means all types. */
  types?: readonly string[];
  turn_id?: string;
  /** Nonnegative event bound. Omitted or zero is unbounded; negatives fail. */
  limit?: number;
}
export interface DataEvent {
  type: string;
  thread_id?: string;
  turn_id?: string;
  created_at?: string;
  data?: unknown;
}
export interface DataQueryResult { version: string; thread_id: string; events: DataEvent[] | null }

async function call<T>(host: RuntimeHost, service: string, method: string, params: object): Promise<T> {
  return await host.call('host.service.call', {service, method, params}) as T;
}

/** Bind to the RuntimeHost passed to the current callback. Helpers never resolve
 * a newer generation, retry a write, or substitute a local backend. Low-level
 * providers require declared service dependencies; ordinary plugins use the
 * host.settings / host.storage / host.credentials gateways for their own data.
 */
export function credentialStore(host: RuntimeHost) {
  return {
    get: (entry: CredentialEntry) => call<CredentialValue>(host, CREDENTIAL_STORE_SERVICE, 'get', entry),
    set: (entry: CredentialSet) => call<Record<string, never>>(host, CREDENTIAL_STORE_SERVICE, 'set', entry),
    delete: (entry: CredentialEntry) => call<Record<string, never>>(host, CREDENTIAL_STORE_SERVICE, 'delete', entry),
    compareExchange: (entry: CredentialCompareExchange) => call<CredentialExchangeResult>(host, CREDENTIAL_STORE_SERVICE, 'compare-exchange', entry),
  };
}
export function configurationStore(host: RuntimeHost) {
  return {
    read: () => call<ConfigSourceView>(host, CONFIG_SOURCE_SERVICE, 'read', {}),
    compareExchange: (input: ConfigSourceCompareExchange) => call<ConfigSourceView>(host, CONFIG_SOURCE_SERVICE, 'compare-exchange', input),
  };
}
export function pluginSettingsStore(host: RuntimeHost) {
  return {
    read: (input: PluginSettingsAddress) => call<PluginSettingsView>(host, PLUGIN_SETTINGS_STORE_SERVICE, 'read', input),
    update: (input: PluginSettingsUpdate) => call<PluginSettingsView>(host, PLUGIN_SETTINGS_STORE_SERVICE, 'update', input),
    readState: (input: PluginSettingsAddress) => call<PluginStateView>(host, PLUGIN_SETTINGS_STORE_SERVICE, 'read-state', input),
    updateState: (input: PluginStateUpdate) => call<PluginStateView>(host, PLUGIN_SETTINGS_STORE_SERVICE, 'update-state', input),
  };
}
export function artifactStore(host: RuntimeHost) {
  return {
    import: (input: ArtifactStoreImport) => call<ImportedArtifact>(host, ARTIFACT_STORE_SERVICE, 'import', input),
    resolve: (input: ArtifactResolve) => call<ArtifactResolved>(host, ARTIFACT_STORE_SERVICE, 'resolve', input),
    read: (input: ArtifactRead) => call<ArtifactBytes>(host, ARTIFACT_STORE_SERVICE, 'read', input),
    fork: (input: ArtifactFork) => call<ImportedArtifact>(host, ARTIFACT_STORE_SERVICE, 'fork', input),
  };
}
export function queryData(host: RuntimeHost, input: DataQuery): Promise<DataQueryResult> {
  return call(host, DATA_QUERY_SERVICE, 'call', input);
}
export function authorize(host: RuntimeHost, input: AuthorizationRequest): Promise<AuthorizationDecision> {
  return call(host, 'security.authorize', 'authorize', input);
}
