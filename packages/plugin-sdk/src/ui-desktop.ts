/** Account transport preserves the host's credential storage and authentication flow. */
export type ApplicationAccountActionV1 = 'status' | 'login' | 'register' | 'recover' | 'logout' | 'revoke' | 'password' | 'config' | 'github-start' | 'github-poll' | 'github-cancel';
export type ApplicationAccountDeviceV1 = { pub: string; account: string; name: string; role: 'host' | 'phone'; online: boolean; added_at: number };
export type ApplicationAccountViewV1 = { username?: string; server?: string; pub?: string; devices?: ApplicationAccountDeviceV1[]; recovery?: string; localLogoutOnly?: boolean; oauth_url?: string; github?: boolean; registration?: boolean; auth_method?: string; display_name?: string; unavailable?: boolean };
export type ApplicationAccountDriverV1 = (action: ApplicationAccountActionV1, input?: Record<string, string>) => Promise<ApplicationAccountViewV1>;
export type ApplicationDesktopContextV1 = { kind: "project"; project_id: string; cwd: string } | { kind: "no_project"; cwd: string };
export type ApplicationDesktopPetTargetV1 = { thread_id: string } | { workspace: ApplicationDesktopContextV1 };

/** A window capability handle belongs to one mounted UI generation. */
export interface ApplicationDesktopControlsV1 {
  readonly platform?: "darwin" | "linux" | "win32";
  readonly hostKind: "desktop" | "web";
  readonly popOut: { readonly kind: "thread" | "draft" | null; readonly threadID: string | null; readonly context: Readonly<ApplicationDesktopContextV1> | null };
  readonly capabilities: { readonly account: boolean; readonly pets: boolean; readonly nativeControls: boolean; readonly chromeOverlay: boolean; readonly notifications: boolean };
  readonly account?: ApplicationAccountDriverV1;
  readonly openAccountWindow?: () => Promise<void>;
  readonly closeAccountWindow?: () => Promise<void>;
  readonly minimize?: () => Promise<void>;
  readonly close?: () => Promise<void>;
  readonly toggleMaximize?: () => Promise<boolean>;
  readonly toggleFullScreen?: () => Promise<void>;
  readonly isMaximized?: () => Promise<boolean>;
  readonly isFullScreen?: () => Promise<boolean>;
  readonly pointerPosition?: () => Promise<{ x: number; y: number } | null>;
  readonly setControls?: (state: { visible: boolean; x: number; y: number }) => Promise<void>;
  readonly onResizeState?: (listener: (state: { resizing: boolean }) => void) => () => void;
  readonly onMaximizedChange?: (listener: (maximized: boolean) => void) => () => void;
  readonly onFullScreenChange?: (listener: (fullscreen: boolean) => void) => () => void;
  readonly onVisibilityChange: (listener: (visible: boolean) => void) => () => void;
  readonly showNotification?: (params: { title: string; body: string }) => Promise<{ shown: boolean }>;
  readonly setActiveCUAThread?: (threadId?: string) => void;
  readonly chromeOverlay?: ApplicationDesktopChromeOverlayApiV1;
}

/** Presentation-only native overlays; session/account authority remains in the host. */
export const APPLICATION_DESKTOP_CHROME_OVERLAY_VERSION = 1 as const;
export type ApplicationDesktopChromeOverlaySurfaceV1 = "account" | "capsule";
export type ApplicationDesktopChromeOverlayRectV1 = { x: number; y: number; width: number; height: number };
export type ApplicationDesktopChromeOverlayAppearanceV1 = {
  theme: "light" | "dark";
  locale: "en-US" | "zh-CN";
  fontFamily: string;
  variables: Record<string, string>;
};
export type ApplicationDesktopChromeAccountTriggerV1 = { title: string; initials?: string; disabled: boolean; open: boolean; label: string };
export type ApplicationDesktopChromeDesktopUpdateV1 = { state: "available" | "ready" | "error" | "downloading" | "checking" | "prompting" | "installing"; label: string; progressLabel: string; busy: boolean; percent?: number };
export type ApplicationDesktopChromeAccountActionV1 = "devices" | "usage" | "account" | "providers" | "logout";
export type ApplicationDesktopChromeAccountPanelV1 = {
  label: string;
  profile?: { title: string; initials: string; server?: string };
  rows: { action: ApplicationDesktopChromeAccountActionV1; label: string; disabled?: boolean }[];
  error?: string; notice?: string;
};
export type ApplicationDesktopChromeCapsuleModelV1 = { kind: "capsule"; draft: string; expanded: boolean; menuOpen: boolean; busy: boolean; sessionId?: string; error?: string;
  sourceTitle: string; label: string; placeholder: string; sendLabel: string; openLabel: string; collapseLabel: string };
export type ApplicationDesktopChromeOverlayModelV1 = { kind: "account"; panel: ApplicationDesktopChromeAccountPanelV1 } | ApplicationDesktopChromeCapsuleModelV1;
export type ApplicationDesktopChromeOverlayRequestV1 = {
  version: typeof APPLICATION_DESKTOP_CHROME_OVERLAY_VERSION;
  token: string;
  generation: number;
  bounds: ApplicationDesktopChromeOverlayRectV1;
  appearance: ApplicationDesktopChromeOverlayAppearanceV1;
  focus: boolean;
  model: ApplicationDesktopChromeOverlayModelV1;
};
export type ApplicationDesktopChromeOverlayCommandV1 =
  | { type: "capsule-expand" | "capsule-open" | "capsule-menu" }
  | { type: "capsule-collapse" | "capsule-draft" | "capsule-send"; text: string }
  | { type: "account-action"; action: ApplicationDesktopChromeAccountActionV1 }
  | { type: "dismiss"; reason: "escape" | "tab" | "outside" | "owner" | "failed" };
export type ApplicationDesktopChromeOverlayEventV1 = { token: string; generation: number; command: ApplicationDesktopChromeOverlayCommandV1 };
export interface ApplicationDesktopChromeOverlayApiV1 {
  show(request: ApplicationDesktopChromeOverlayRequestV1): Promise<void>;
  hide(token: string, generation: number): Promise<void>;
  onEvent(listener: (event: ApplicationDesktopChromeOverlayEventV1) => void): () => void;
}
