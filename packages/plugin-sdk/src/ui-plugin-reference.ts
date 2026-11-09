/** Selects a conversation capability without granting execution permission. */
export interface ApplicationPluginReferenceV1 {
  readonly authorization_id?: string;
  readonly plugin_id?: string;
  readonly mcp_server?: string;
  readonly resource_url?: string;
}
