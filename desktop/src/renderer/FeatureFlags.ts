/// <reference types="vite/client" />

/** Native phone pairing is available wherever the host supports remote control. */
export const ENABLE_REMOTE_CONTROL =
  import.meta.env.VITE_ENABLE_REMOTE_CONTROL !== "false";

/** Temporarily hidden to keep the conversation focused; retain the edit data and components. */
export const ENABLE_TURN_EDIT_SUMMARY = false;

/** Keep the information panel hidden until its contents warrant a permanent entry point. */
export const ENABLE_ENVIRONMENT_PANEL = false;

/** Explicit file deliveries remain accessible beside their conversation. */
export const ENABLE_TURN_ARTIFACT_SUMMARY = true;

/**
 * Account and device-linking UI stays available in development. All production
 * builds, including local packages, hide it until the flow is ready for users;
 * a leftover development environment variable must not expose it in a release.
 */
export const ENABLE_ACCOUNT =
  import.meta.env.DEV && import.meta.env.VITE_ENABLE_ACCOUNT !== "false";

/** Subscription visibility follows quota-aware provider data in production. */
export const ENABLE_SUBSCRIPTIONS = true;

/**
 * Embedded browser. The workspace panel and the agent's page are one tab.
 * The page stays in a hidden host until that panel is showing it. Set
 * VITE_ENABLE_BROWSER=false to hide it during development.
 */
export const ENABLE_EMBEDDED_BROWSER =
  import.meta.env.VITE_ENABLE_BROWSER !== "false";
