// Single source for the categories this renderer knows how to display;
// runtime membership checks (unknown wire values degrade to "internal")
// and the union type both derive from it.
const USER_FACING_ERROR_CATEGORIES = [
  "cancelled",
  "network",
  "auth",
  "provider",
  "invalid_request",
  "tool",
  "local",
  "internal",
] as const;
export type UserFacingErrorCategory = (typeof USER_FACING_ERROR_CATEGORIES)[number];

function isUserFacingErrorCategory(value: string): value is UserFacingErrorCategory {
  return (USER_FACING_ERROR_CATEGORIES as readonly string[]).includes(value);
}
export type UserFacingErrorTone = "neutral" | "warning" | "auth" | "error";
export type UserFacingErrorContext = "turn" | "tool" | "status";

import type { TurnError } from "../shared/protocol";
import { translateCurrent as t } from "./i18n";

export type UserFacingErrorDisplay = {
  category: UserFacingErrorCategory;
  tone: UserFacingErrorTone;
  title: string;
  detail: string;
  diagnostic?: string;
};

export function rawErrorMessage(error: unknown, fallback = ""): string {
  if (error instanceof Error) {
    return error.message || fallback;
  }
  if (typeof error === "string") {
    return error || fallback;
  }
  return fallback;
}

// Reason phrases for the HTTP status codes the classifier can extract from
// a provider error. The numeric code stays in the title ("401 未授权") so
// screenshots carry enough signal to identify the upstream side of the
// problem, while the phrase itself follows the current interface language.
const HTTP_REASON_KEYS = {
  "400": "error.http400",
  "401": "error.http401",
  "403": "error.http403",
  "404": "error.http404",
  "408": "error.http408",
  "413": "error.http413",
  "429": "error.http429",
  "500": "error.http500",
  "502": "error.http502",
  "503": "error.http503",
  "504": "error.http504",
  "529": "error.http529",
} as const;

function extractHttpCode(message: string): string | undefined {
  const match = message.match(/\b(400|401|403|404|408|413|429|500|502|503|504|529)\b/);
  return match?.[1];
}

function httpTitle(code: string): string {
  const key = HTTP_REASON_KEYS[code as keyof typeof HTTP_REASON_KEYS];
  return key ? `${code} ${t(key)}` : code;
}

// Keep this legacy message-only fallback aligned with the Go core's explicit
// overflow identities. Persisted history does not retain every structured
// error field, so these patterns must classify a rebuilt turn the same way as
// the live structured error without guessing from quota wording.
const CONTEXT_OVERFLOW_PATTERNS = [
  /context[_ ]length[_ ]exceeded/i,
  /exceeds the context window/i,
  /context window exceeds limit/i,
  /maximum context length/i,
  /model_context_window_exceeded/i,
  /prompt is too long/i,
  /request_too_large/i,
  /input is too long/i,
  /input token count.*exceeds the maximum/i,
  /maximum prompt length is \d+/i,
  /reduce the length of the messages/i,
  /exceeds (?:the )?maximum allowed input length/i,
  /is longer than the model'?s context length/i,
  /prompt token count(?: of)? [\d,]+ exceeds the limit of [\d,]+/i,
  /exceeds the available context size/i,
  /greater than the context length/i,
  /exceeded model token limit/i,
  /message size [\d,]+ exceeds limit/i,
  /prompt too long; exceeded (?:max )?context length/i,
];

function hasContextOverflowPhrasing(message: string): boolean {
  return CONTEXT_OVERFLOW_PATTERNS.some((pattern) => pattern.test(message));
}

function structuredStatusFact(structured: TurnError | undefined): string | undefined {
  const statusCode = structured?.status_code;
  if (typeof statusCode !== "number" || !Number.isFinite(statusCode) || statusCode <= 0) {
    return undefined;
  }
  return `HTTP ${Math.trunc(statusCode)}`;
}

function structuredProviderCode(structured: TurnError | undefined): string | undefined {
  const code = structured?.code?.trim();
  return code && /^[A-Za-z0-9._-]{1,128}$/.test(code) ? code : undefined;
}

type SpecificDisplay = {
  /** Readable localized title, when the message maps to a known situation. */
  title?: string;
};

/**
 * Pull a specific situation out of the raw error so the system event is readable
 * at a glance. Known keywords map to a localized title; identifiers we cannot
 * translate remain diagnostic data and never become a second visible label.
 *
 * The list is intentionally short: anything renderer-specific belongs to
 * the turn notice, not the error model.
 */
function extractSpecificDisplay(
  message: string,
  category: UserFacingErrorCategory,
): SpecificDisplay {
  const lower = message.toLowerCase();
  switch (category) {
    case "cancelled":
      return {};
    case "network": {
      if (isResponseCompletedMissingMessage(lower)) {
        return { title: t("error.responseIncomplete") };
      }
      // OpenAI/Anthropic-style wrapped errors look like
      // "stream request failed: stream error (previous_response_not_found)".
      // The parenthesized token at the end of the message is the actual
      // provider error code. Unknown identifiers remain diagnostic-only.
      const wrapped = message.match(/\(([^()]+)\)\s*$/);
      if (wrapped) return {};
      const code = extractHttpCode(message);
      if (code) return { title: httpTitle(code) };
      if (lower.includes("rate limit") || lower.includes("too many requests")) return { title: t("error.rateLimited") };
      if (lower.includes("overloaded")) return { title: t("error.upstreamOverloaded") };
      if (lower.includes("timeout") || lower.includes("deadline exceeded")) return { title: t("error.requestTimeout") };
      if (lower.includes("connection refused")) return { title: t("error.connectionRefused") };
      if (lower.includes("connection reset")) return { title: t("error.connectionReset") };
      if (lower.includes("connection dropped")) return { title: t("error.connectionDropped") };
      if (lower.includes("no such host")) return { title: t("error.hostResolutionFailed") };
      if (lower.includes("dns")) return { title: t("error.dnsFailed") };
      if (lower.includes("eof")) return { title: t("error.connectionInterrupted") };
      if (lower.includes("temporarily unavailable")) return { title: t("error.temporarilyUnavailable") };
      return {};
    }
    case "auth": {
      const code = extractHttpCode(message);
      if (code) return { title: httpTitle(code) };
      if (lower.includes("api key")) return { title: t("error.apiKeyInvalid") };
      if (lower.includes("oauth")) return { title: t("error.oauthFailed") };
      if (lower.includes("invalid token") || lower.includes("access token")) return { title: t("error.credentialExpired") };
      if (lower.includes("permission denied")) return { title: t("error.accessDenied") };
      return {};
    }
    case "provider": {
      if (isResponseCompletedMissingMessage(lower)) {
        return { title: t("error.responseIncomplete") };
      }
      if (hasContextOverflowPhrasing(lower)) {
        return { title: t("error.contextExceeded") };
      }
      if (lower.includes("too many tokens")) return { title: t("error.tokenLimitExceeded") };
      // A status code embedded in the message ("HTTP 429: …") is the one
      // fact that survives a history rebuild, so it outranks keyword
      // guesses — resume-safe identity beats phrasing.
      const code = extractHttpCode(message);
      if (code) return { title: httpTitle(code) };
      if (lower.includes("content policy") || lower.includes("content_policy")) return { title: t("error.contentPolicy") };
      if (lower.includes("rate_limit") || lower.includes("rate limit")) return { title: t("error.rateLimited") };
      if (lower.includes("model_not_found")) return { title: t("error.modelNotFound") };
      if (lower.includes("empty response") || lower.includes("empty answer")) return { title: t("error.modelEmpty") };
      if (lower.includes("model returned")) return { title: t("error.modelError") };
      if (lower.includes("response failed") || lower.includes("response error")) return { title: t("error.responseFailed") };
      if (lower.includes("invalid_request_error")) return { title: t("error.invalidRequest") };
      return {};
    }
    case "invalid_request": {
      const code = extractHttpCode(message);
      if (code) return { title: httpTitle(code) };
      return {};
    }
    case "tool": {
      const first = message.split(/[.\n]/)[0]?.trim();
      if (!first) return {};
      const clipped = first.length > 60 ? `${first.slice(0, 57)}…` : first;
      // A tool error that is already Chinese reads fine as the title; an
      // arbitrary English message remains diagnostic-only.
      if (/[一-鿿]/.test(clipped)) return { title: clipped };
      return {};
    }
    case "local": {
      if (lower.includes("permission denied")) return { title: t("error.permissionInsufficient") };
      if (lower.includes("enoent") || lower.includes("no such file")) return { title: t("error.fileNotFound") };
      if (lower.includes("not a directory")) return { title: t("error.notDirectory") };
      if (lower.includes("is a directory")) return { title: t("error.isDirectory") };
      if (
        lower.includes("outside the current workspace") ||
        lower.includes("outside the current git repository")
      ) {
        return { title: t("error.outsideWorkspace") };
      }
      if (lower.includes("command failed") || lower.includes("exit status")) return { title: t("error.commandFailed") };
      return {};
    }
    case "internal":
    default:
      return {};
  }
}

export function userFacingErrorForMessage(
  input: string | TurnError | undefined,
  context: UserFacingErrorContext
): UserFacingErrorDisplay {
  // Accept either a raw string (legacy callers, including server-error
  // text and the composer status row) or a structured TurnError from the
  // Go core. The Go side's BuildTurnError is the authoritative source:
  // when its TurnError arrives, we use it as-is and only fall back to
  // message-substring matching when fields are missing (so an older app
  // server or a manual string still produces a sensible display).
  const structured: TurnError | undefined =
    typeof input === "object" && input !== null ? input : undefined;
  const message = (
    structured?.message ?? (typeof input === "string" ? input : "") ?? ""
  ).trim();

  // Category: prefer the wire value, fall back to the legacy classifier.
  // A wire value we do not recognize (a newer Go core added a category
  // before this renderer learned it) degrades to the internal-error
  // rendering instead of producing a blank, tone-less display.
  const wireCategory = structured?.category;
  const category: UserFacingErrorCategory =
    wireCategory !== undefined
      ? isUserFacingErrorCategory(wireCategory)
        ? wireCategory
        : "internal"
      : classifyUserFacingError(message, context);

  // Message-derived specifics win over structured transport facts: after a
  // thread resume the turn snapshot is rebuilt from persisted history, which
  // retains only the message — not status_code / code. A title that depends
  // on structured fields would flip wording across a resume (tab switch), so
  // the traceable title is kept only as the fallback for opaque messages
  // (e.g. a post-200 provider failure whose message carries no status).
  const structuredCode = structuredProviderCode(structured);
  const specific = extractSpecificDisplay(message, category);
  const specificFromCode = structuredCode
    ? extractSpecificDisplay(structuredCode, category)
    : {};
  const statusFact = structuredStatusFact(structured);
  // Context-overflow phrasing is an identity, not a keyword guess: like the
  // embedded HTTP status it survives history rebuilds, so it shares the top
  // precedence instead of losing to the transport status fact.
  const overflowTitle = hasContextOverflowPhrasing(message.toLowerCase()) ? t("error.contextExceeded") : undefined;
  const traceableFailureTitle = statusFact
    ? `${t("error.requestFailedTitle")} · ${statusFact}`
    : category === "provider" && structuredCode && !specific.title && !specificFromCode.title
      ? `${t("error.requestFailedTitle")} · ${structuredCode}`
      : undefined;
  const title =
    overflowTitle ||
    specific.title ||
    specificFromCode.title ||
    traceableFailureTitle ||
    defaultTitleForCategory(category);
  // Detail is hover and accessibility copy, not a visible second line.
  const detail =
    specificDetailForMessage(message, category) ||
    defaultDetailForCategory(category);

  return {
    category,
    tone: toneForCategory(category),
    title,
    detail,
    diagnostic: message || undefined,
  };
}

function toneForCategory(category: UserFacingErrorCategory): UserFacingErrorTone {
  switch (category) {
    case "cancelled":
      return "neutral";
    case "auth":
      return "auth";
    case "network":
    case "provider":
    case "invalid_request":
    case "tool":
    case "local":
    case "internal":
      return "error";
  }
}

function defaultTitleForCategory(category: UserFacingErrorCategory): string {
  switch (category) {
    case "cancelled":
      return t("error.cancelledTitle");
    case "network":
      return t("error.networkTitle");
    case "auth":
      return t("error.authTitle");
    case "provider":
      return t("error.providerTitle");
    case "invalid_request":
      return t("error.invalidRequest");
    case "tool":
      return t("error.toolTitle");
    case "local":
      return t("error.localTitle");
    case "internal":
      return t("error.internalTitle");
  }
}

function defaultDetailForCategory(category: UserFacingErrorCategory): string {
  switch (category) {
    case "cancelled":
      return t("error.cancelledDetail");
    case "network":
      return t("error.networkDetail");
    case "auth":
      return t("error.authDetail");
    case "provider":
      return t("error.providerDetail");
    case "invalid_request":
      return t("error.invalidRequestDetail");
    case "tool":
      return t("error.toolDetail");
    case "local":
      return t("error.localDetail");
    case "internal":
      return t("error.internalDetail");
  }
}

function specificDetailForMessage(
  message: string,
  category: UserFacingErrorCategory,
): string | undefined {
  const normalized = message.toLowerCase();
  if (category === "provider" && (normalized.includes("empty response") || normalized.includes("empty answer"))) {
    return t("error.modelEmptyDetail");
  }
  if (
    (category === "provider" || category === "network") &&
    isResponseCompletedMissingMessage(normalized)
  ) {
    return t("error.responseIncompleteDetail");
  }
  return undefined;
}

export function classifyUserFacingError(message: string, context: UserFacingErrorContext): UserFacingErrorCategory {
  const normalized = message.toLowerCase();
  if (isCancellationMessage(normalized)) {
    return "cancelled";
  }
  if (isLocalOperationError(normalized)) {
    return "local";
  }
  if (isAuthOrPermissionError(normalized)) {
    return "auth";
  }
  if (isProviderBusinessError(normalized)) {
    return "provider";
  }
  if (isInvalidRequestError(normalized)) {
    return "invalid_request";
  }
  if (isNetworkOrUpstreamError(normalized)) {
    return "network";
  }
  return context === "tool" ? "tool" : "internal";
}

export function isCancellationMessage(message: string): boolean {
  return (
    message.includes("context canceled") ||
    message.includes("context cancelled") ||
    message.includes("user canceled") ||
    message.includes("user cancelled") ||
    message.includes("request canceled") ||
    message.includes("request cancelled") ||
    message.includes("operation was aborted") ||
    message.includes("aborterror")
  );
}

function isAuthOrPermissionError(message: string): boolean {
  return (
    /\b(401|403)\b/.test(message) ||
    message.includes("unauthorized") ||
    message.includes("unauthenticated") ||
    message.includes("forbidden") ||
    message.includes("permission denied") ||
    message.includes("api key") ||
    message.includes("access token") ||
    message.includes("invalid token") ||
    message.includes("oauth") ||
    message.includes("login required") ||
    message.includes("log in")
  );
}

function isNetworkOrUpstreamError(message: string): boolean {
  return (
    /\b(500|502|503|504)\b/.test(message) ||
    message.includes("network") ||
    message.includes("stream request failed") ||
    message.includes("request failed") ||
    message.includes("connection refused") ||
    message.includes("connection reset") ||
    message.includes("connection dropped") ||
    message.includes("no such host") ||
    message.includes("dial tcp") ||
    message.includes("dns") ||
    message.includes("timeout") ||
    message.includes("deadline exceeded") ||
    message.includes("temporarily unavailable") ||
    message.includes("overloaded") ||
    message.includes("too many requests") ||
    message.includes("rate limit") ||
    message.includes("eof")
  );
}

function isProviderBusinessError(message: string): boolean {
  return (
    isResponseCompletedMissingMessage(message) ||
    hasContextOverflowPhrasing(message) ||
    // Mirror the Go core's HTTP mapping (categoryFromHTTPError): 413 is a
    // request-size failure, while 429/529 are rate limit / overloaded — all
    // are provider-side, never network faults.
    /\b(413|429|529)\b/.test(message) ||
    message.includes("too many tokens") ||
    message.includes("empty response") ||
    message.includes("empty answer") ||
    message.includes("model returned") ||
    message.includes("provider") ||
    message.includes("response failed") ||
    message.includes("response error") ||
    message.includes("content policy") ||
    message.includes("rate_limit")
  );
}

// Message-only counterpart of the wire `invalid_request` category: keeps a
// resumed turn (rebuilt from the persisted message, without structured
// fields) classified the same as the live turn the Go core tagged.
function isInvalidRequestError(message: string): boolean {
  return (
    message.includes("invalid_request") ||
    message.includes("bad request") ||
    message.includes("http 400")
  );
}

function isResponseCompletedMissingMessage(message: string): boolean {
  return message.includes("before response.completed");
}

function isLocalOperationError(message: string): boolean {
  const hasLocalPermission =
    (message.includes("permission denied") || message.includes("operation not permitted")) &&
    (message.includes("file") ||
      message.includes("path") ||
      message.includes("directory") ||
      message.includes("command") ||
      message.includes("git") ||
      message.includes("gh") ||
      message.includes("eacces") ||
      message.includes("eperm"));
  return (
    hasLocalPermission ||
    message.includes("enoent") ||
    message.includes("eacces") ||
    message.includes("eperm") ||
    message.includes("no such file") ||
    message.includes("not a directory") ||
    message.includes("is a directory") ||
    message.includes("outside the current workspace") ||
    message.includes("outside the current git repository") ||
    message.includes("selected path") ||
    message.includes("git ") ||
    message.includes("github cli") ||
    message.includes("exit status") ||
    message.includes("command failed")
  );
}

export function statusMessageForError(error: unknown, fallback: string): string {
  // The composer status row renders a single-line label between two
  // dividers, so return the same single user-facing label as the inline
  // turn event. Machine identifiers remain diagnostic-only.
  const display = userFacingErrorForMessage(rawErrorMessage(error, fallback), "status");
  return display.title;
}

// Keyword lists mirror the title vocabulary produced above (specific
// titles, HTTP phrases, category defaults) — extend them together.
const AUTH_STATUS_KEYWORDS = [
  "权限",
  "登录",
  "认证",
  "凭据",
  "未授权",
  "API Key",
  "OAuth",
  "authentication",
  "credentials",
  "unauthorized",
  "access denied",
  "login",
];
const ERROR_STATUS_KEYWORDS = [
  "失败",
  "错误",
  "异常",
  "不可用",
  "内部",
  "超时",
  "限流",
  "中断",
  "断开",
  "拒绝",
  "重置",
  "过载",
  "过大",
  "无效",
  "不存在",
  "为空",
  "超出",
  "无法解析",
  "内容安全",
  "未完整",
  "stream_closed",
  "response.completed",
  "failed",
  "error",
  "unavailable",
  "timeout",
  "rate limit",
  "interrupted",
  "disconnected",
  "refused",
  "reset",
  "overloaded",
  "invalid",
  "not found",
  "empty",
  "exceeded",
  "incomplete",
];

export function statusToneClass(status: string): string {
  const trimmed = status.trim();
  if (!trimmed || trimmed === "ready" || trimmed === "connecting" || trimmed === "opening" || trimmed.startsWith("正在")) {
    return "";
  }
  if (AUTH_STATUS_KEYWORDS.some((keyword) => trimmed.includes(keyword))) {
    return " auth";
  }
  if (ERROR_STATUS_KEYWORDS.some((keyword) => trimmed.includes(keyword))) {
    return " error";
  }
  return "";
}
