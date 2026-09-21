/// <reference types="vite/client" />

/** Phone access is development-only until the mobile connection flow ships. */
export const ENABLE_REMOTE_CONTROL =
  import.meta.env.DEV && import.meta.env.VITE_ENABLE_REMOTE_CONTROL !== "false";

/** Temporarily hidden to keep the conversation focused; retain the edit data and components. */
export const ENABLE_TURN_EDIT_SUMMARY = false;
export const ENABLE_CONVERSATION_TURN_RAIL = false;

/** Explicit file deliveries remain accessible beside their conversation. */
export const ENABLE_TURN_ARTIFACT_SUMMARY = true;

/**
 * Collaboration is part of the default desktop product in development and
 * release builds. Keep a build-time opt-out for emergency rollback without
 * maintaining a separate release-only product surface.
 */
export const ENABLE_GROUP_CHAT =
  import.meta.env.VITE_ENABLE_GROUP_CHAT !== "false";

/**
 * Account and device-linking UI stays available in development. All production
 * builds, including local packages, hide it until the flow is ready for users;
 * a leftover development environment variable must not expose it in a release.
 */
export const ENABLE_ACCOUNT =
  import.meta.env.DEV && import.meta.env.VITE_ENABLE_ACCOUNT !== "false";

/**
 * Agent-driven browser overlay (WebContentsView takeover, bounds reporting,
 * and server-request routing). Visiting a page still happens in a hidden host;
 * the overlay only appears after an explicit foreground promotion. Set
 * VITE_ENABLE_BROWSER=false to hide the overlay during development.
 */
export const ENABLE_EMBEDDED_BROWSER =
  import.meta.env.VITE_ENABLE_BROWSER !== "false";
