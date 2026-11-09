/** Explicit, read-only local app snapshots. These never grant Computer Use. */
export type AppContextSettings = {
  enabled: boolean;
  shortcut: string;
  include_text: boolean;
};

export type AppContextSnapshot = {
  id: string;
  captured_at: string;
  app_name: string;
  bundle_id: string;
  window_title: string;
  window_id: number;
  width: number;
  height: number;
  image_base64: string;
  available_text: string;
  text_status: "included" | "truncated" | "not_requested" | "permission_missing" | "unavailable";
};

export type AppContextState = {
  available: boolean;
  settings: AppContextSettings;
  shortcut_registered: boolean;
  phase: "idle" | "capturing" | "ready" | "error";
  app_name?: string;
  error?: string;
  snapshot?: AppContextSnapshot;
};
