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
 * Account and device-linking UI stays available in development. All production
 * builds, including local packages, hide it until the flow is ready for users;
 * a leftover development environment variable must not expose it in a release.
 */
export const ENABLE_ACCOUNT =
  import.meta.env.DEV && import.meta.env.VITE_ENABLE_ACCOUNT !== "false";

/** Keep the subscription dashboard development-only until it is ready to ship. */
export const ENABLE_SUBSCRIPTIONS = import.meta.env.DEV;

/**
 * Embedded browser. The workspace panel and the agent's page are one tab.
 * The page stays in a hidden host until that panel is showing it. Set
 * VITE_ENABLE_BROWSER=false to hide it during development.
 */
export const ENABLE_EMBEDDED_BROWSER =
  import.meta.env.VITE_ENABLE_BROWSER !== "false";
