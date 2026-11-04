/** Selects a conversation capability and connection identity without granting execution permission. */
export interface ApplicationPluginReferenceV1 {
  readonly authorization_id?: string;
  readonly plugin_id?: string;
  readonly mcp_server?: string;
  readonly resource_url?: string;
  readonly account_id?: string;
  readonly workspace_id?: string;
}
