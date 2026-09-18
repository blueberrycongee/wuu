/// <reference types="vite/client" />

/** Phone access is available in development; the local desktop release hides it. */
export const ENABLE_REMOTE_CONTROL =
  import.meta.env.VITE_ENABLE_REMOTE_CONTROL !== "false";

/** Temporarily hidden to keep the conversation focused; retain the edit data and components. */
export const ENABLE_TURN_EDIT_SUMMARY = false;
export const ENABLE_CONVERSATION_TURN_RAIL = false;

/**
 * Same turn output-summary family as the file-change card. Hide the file-list
 * card until that surface returns; keep snapshots and inline image previews.
 */
export const ENABLE_TURN_ARTIFACT_SUMMARY = false;

/**
 * Collaboration is part of the default desktop product in development and
 * release builds. Keep a build-time opt-out for emergency rollback without
 * maintaining a separate release-only product surface.
 */
export const ENABLE_GROUP_CHAT =
  import.meta.env.VITE_ENABLE_GROUP_CHAT !== "false";

/**
 * Account and device-linking UI stays available in development, but the
 * current desktop release is intentionally unauthenticated until that flow
 * is ready for users.
 */
export const ENABLE_ACCOUNT =
  import.meta.env.VITE_ENABLE_ACCOUNT !== "false";

/** The verification-model settings page is not part of the local release. */
export const ENABLE_COLLABORATION_SETTINGS =
  import.meta.env.VITE_ENABLE_COLLABORATION_SETTINGS !== "false";

/**
 * The embedded browser remains an internal development capability. Production
 * builds do not expose its workspace surface even if the build environment
 * happens to contain the opt-in variable.
 */
export const ENABLE_EMBEDDED_BROWSER =
  import.meta.env.DEV && import.meta.env.VITE_ENABLE_BROWSER === "true";
