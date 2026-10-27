/** Private, versioned transport for bundled browser-chrome surfaces. */
export const CHROME_OVERLAY_VERSION = 1 as const;
export type ChromeOverlaySurface = "account" | "capsule";
export type ChromeOverlayRect = { x: number; y: number; width: number; height: number };
export type ChromeOverlayAppearance = {
  theme: "light" | "dark";
  locale: "en-US" | "zh-CN";
  fontFamily: string;
  variables: Record<string, string>;
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
export type ChromeCapsuleModel = { kind: "capsule"; draft: string; expanded: boolean; menuOpen: boolean; busy: boolean; sessionId?: string; error?: string;
  sourceTitle: string; label: string; placeholder: string; sendLabel: string; openLabel: string; collapseLabel: string };
export type ChromeOverlayModel = { kind: "account"; panel: ChromeAccountPanel } | ChromeCapsuleModel;
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
  | { type: "capsule-expand" | "capsule-open" | "capsule-menu" }
  | { type: "capsule-collapse" | "capsule-draft" | "capsule-send"; text: string }
  | { type: "account-action"; action: ChromeAccountAction }
  | { type: "dismiss"; reason: "escape" | "tab" | "outside" | "owner" | "failed" };
export type ChromeOverlayEvent = { token: string; generation: number; command: ChromeOverlayCommand };
export interface ChromeOverlayApi {
  show(request: ChromeOverlayRequest): Promise<void>;
  hide(token: string, generation: number): Promise<void>;
  onEvent(listener: (event: ChromeOverlayEvent) => void): () => void;
}
export interface ChromeOverlayRendererApi {
  ready(): Promise<ChromeOverlayRequest | null>;
  onState(listener: (request: ChromeOverlayRequest | null) => void): () => void;
  painted(token: string, generation: number): void;
  command(event: ChromeOverlayEvent): void;
}
