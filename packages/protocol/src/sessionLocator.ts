/** Versioned session placement, shared with the Extension API session locator. */
export type SessionLocatorV1 = {
  version: 1;
  execution_host: string;
  session_id?: string;
  storage_host?: string;
  display_host?: string;
};
