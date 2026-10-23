/** Private, versioned transport for bundled browser-chrome surfaces. */
export const CHROME_OVERLAY_VERSION = 1 as const;
export type ChromeOverlaySurface = "ribbon" | "account" | "capsule";
export type ChromeOverlayRect = { x: number; y: number; width: number; height: number };
export type ChromeOverlayAppearance = {
  theme: "light" | "dark";
  locale: "en-US" | "zh-CN";
  fontFamily: string;
  variables: Record<string, string>;
  shellSkin?: boolean;
  shellImage?: string;
};
export type RibbonIconModel =
  | { kind: "conversations" }
  | { kind: "catalog" }
  | { kind: "public"; name?: string }
  | { kind: "image"; light: string; dark?: string }
  | { kind: "website"; url: string; title: string; favicon?: string; loading?: boolean; startPage?: boolean };
export type ChromeRibbonItem = {
  id: string; title: string; icon: RibbonIconModel; active?: boolean; disabled?: boolean;
  pinned?: boolean; closable?: boolean; website?: boolean; component?: string; pluginId?: string;
  audible?: boolean; muted?: boolean; canMute?: boolean;
};
export type ChromeAccountTrigger = { title: string; initials?: string; disabled: boolean; open: boolean; label: string };
export type ChromeDesktopUpdate = { state: "available" | "ready" | "error" | "downloading" | "checking" | "prompting" | "installing"; label: string; progressLabel: string; busy: boolean; percent?: number };
export type ChromeAccountAction = "devices" | "usage" | "account" | "providers" | "logout";
export type ChromeAccountPanel = {
  label: string;
  profile?: { title: string; initials: string; server?: string };
  rows: { action: ChromeAccountAction; label: string; disabled?: boolean }[];
  error?: string; notice?: string;
};
export type ChromeRibbonFocus = { kind: "item" | "close"; id: string } | { kind: "account" | "new-tab" | "desktop-update" };
export type ChromeRibbonModel = {
  collapsedWidth: number;
  resizeLabel?: string;
  /** Shell-painted header immediately above the native surface, in CSS pixels. */
  headerHeight?: number;
  kind: "ribbon";
  items: ChromeRibbonItem[];
  label: string; newTabLabel: string; canCreateTab: boolean;
  closeTabLabel: string; pinTabLabel: string; unpinTabLabel: string; reorderHint: string;
  muteTabLabel: string; unmuteTabLabel: string;
  paddingTop: number; paddingBottom: number; scrollTop: number;
  focusTarget?: ChromeRibbonFocus;
  footer?: { account?: ChromeAccountTrigger; update?: ChromeDesktopUpdate };
};
export type ChromeCapsuleModel = { kind: "capsule"; draft: string; expanded: boolean; menuOpen: boolean; busy: boolean; sessionId?: string; error?: string;
  sourceTitle: string; label: string; placeholder: string; sendLabel: string; openLabel: string; collapseLabel: string };
export type ChromeOverlayModel = ChromeRibbonModel | { kind: "account"; panel: ChromeAccountPanel } | ChromeCapsuleModel;
export type ChromeOverlayRequest = {
  version: typeof CHROME_OVERLAY_VERSION;
  token: string;
  generation: number;
  bounds: ChromeOverlayRect;
  appearance: ChromeOverlayAppearance;
  focus: boolean;
  model: ChromeOverlayModel;
};
export type ChromeOverlayCommand =
  | { type: "reveal"; fraction: number }
  | { type: "resize-ribbon"; width: number; finished: boolean }
  | { type: "capsule-expand" | "capsule-open" | "capsule-menu" }
  | { type: "capsule-collapse" | "capsule-draft" | "capsule-send"; text: string }
  | { type: "select" | "close-tab"; id: string }
  | { type: "pin-tab"; id: string; pinned: boolean }
  | { type: "mute-tab"; id: string; muted: boolean }
  | { type: "reorder"; id: string; target: string }
  | { type: "new-tab" | "desktop-update" }
  | { type: "account-open"; anchor: ChromeOverlayRect }
  | { type: "account-action"; action: ChromeAccountAction }
  | { type: "dismiss"; reason: "escape" | "tab" | "outside" | "leave" | "owner" | "failed" }
  | { type: "tab-menu"; id: string; x: number; y: number }
  | { type: "pointer"; inside: boolean }
  | { type: "retain"; active: boolean }
  | { type: "scroll"; top: number };
export type ChromeOverlayEvent = { token: string; generation: number; command: ChromeOverlayCommand };
export interface ChromeOverlayApi {
  show(request: ChromeOverlayRequest): Promise<void>;
  hide(token: string, generation: number): Promise<void>;
  onEvent(listener: (event: ChromeOverlayEvent) => void): () => void;
}
export interface ChromeOverlayRendererApi {
  ready(): Promise<ChromeOverlayRequest | null>;
  onVisibility(listener: (visible: boolean) => void): () => void;
  onState(listener: (request: ChromeOverlayRequest | null) => void): () => void;
  painted(token: string, generation: number): void;
  command(event: ChromeOverlayEvent): void;
}
