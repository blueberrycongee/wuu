import type { CodexModelSummary } from "../shared/protocol";
import { resolveLocalizedText } from "./i18n";

export type CodexModelLoadState = {
  provider?: string;
  loading: boolean;
  error: string;
  models: CodexModelSummary[];
};

// The runtime picker opens directly into the model panel (search + provider
// groups + reasoning-effort pills). There is deliberately no intermediate
// "main" menu anymore: the two orthogonal controls users reach for most —
// which model, and how hard it should reason — live on one screen.
export type CodexRuntimeMenu = "model" | null;
export type ComposerVariant = "dock" | "document" | "hero";
export type FloatingMenuOwner =
  | "sidebar-account"
  | "conversation-actions"
  | "composer-handoff"
  | "composer-runtime"
  | "composer-access"
  | "composer-context-meter"
  | "composer-token-gauge"
  | "composer-focus"
  | "composer-plus"
  | "composer-attach"
  | "composer-plugin-tools"
  | "composer-slash"
  | "codex-runtime"
  | "composer-query-history"
  | "minute-clock"
  | "channel-mention"
  | "collaboration-new"
  | "select-menu";
export type FloatingMenuPlacement = "above" | "below" | "middle";
export type FloatingMenuAlign = "left" | "right";
export type PermissionMode =
  | "standard"
  | "read_only"
  | "unconfined";

const HIDDEN_COMPOSER_STATUSES = new Set([
  "ready",
  "正在发送请求",
  "Sending request",
]);

export function composerStatusText(status: string): string {
  const resolved = resolveLocalizedText(status);
  return HIDDEN_COMPOSER_STATUSES.has(resolved) ? "" : resolved;
}

export function composerStatusIsLiveProgress(liveProgress?: boolean): boolean {
  return liveProgress === true;
}
