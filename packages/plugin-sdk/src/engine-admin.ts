/** Administrative methods on the selected engine.<id>@1 service. These are
 * ordinary provider calls: no built-in identity grants additional authority. */
export interface EnginePermissionModeInfo {
  readonly mode: string;
  readonly id?: string;
  readonly label?: string;
}
export interface EngineModelOption {
  readonly id: string;
  readonly label: string;
  readonly type: string;
  readonly default_value: string;
  readonly choices: readonly { readonly value: string; readonly label: string }[];
}
export interface EngineModelInfo {
  readonly id: string;
  readonly display_name?: string;
  readonly resolved_model?: string;
  readonly default_effort?: string;
  readonly supported_efforts?: readonly string[];
  readonly is_default?: boolean;
  readonly fast_mode?: boolean;
  readonly default_speed?: string;
  readonly options?: readonly EngineModelOption[];
}
export interface EngineDescription {
  readonly enabled?: boolean;
  readonly display_name?: string;
  readonly protocol?: string;
  readonly install_url?: string;
  readonly update_url?: string;
  readonly cli_version?: string;
  readonly binary_path?: string;
  /** Readiness; a provider need not require a local binary. */
  readonly binary_ok: boolean;
  readonly error?: string;
  readonly permission_modes?: readonly EnginePermissionModeInfo[];
}
export interface EngineModelsRequest { readonly cwd?: string; readonly refresh?: boolean }
export interface EngineModelsResult {
  readonly models: readonly EngineModelInfo[] | null;
  readonly permission_modes?: readonly EnginePermissionModeInfo[];
  readonly status: string;
  /** Discovery diagnostics accompany partial or stale results. */
  readonly error?: string;
}
export interface EngineAuthRequest {
  /** Empty means discover only; never starts authentication. */
  readonly method_id?: string;
  /** Remote invocations cannot use the reserved local-cli method. */
  readonly remote?: boolean;
}
export interface EngineAuthMethod {
  readonly id: string;
  readonly name: string;
  readonly description?: string;
  readonly type?: string;
}
export interface EngineAuthResult {
  readonly methods: readonly EngineAuthMethod[];
  readonly authenticated: boolean;
}
/** Upstream allowance only, never inferred from local token counts. */
export interface EngineQuota {
  readonly status: string;
  readonly kind?: string;
  readonly account?: { readonly id: string; readonly label?: string; readonly source?: string };
  readonly plan?: string;
  readonly checked_at: string;
  readonly observed_at?: string;
  readonly expires_at?: string;
  readonly error_code?: string;
  readonly windows?: readonly {
    readonly id: string; readonly label?: string; readonly used_percent?: number;
    readonly window_minutes?: number; readonly resets_at?: string; readonly display?: string;
    readonly model?: string; readonly scope?: string; readonly unlimited?: boolean;
  }[];
  readonly balances?: readonly { readonly currency: string; readonly amount: string }[];
  readonly reset_credits?: number;
}
export interface EngineQuotaResult {
  readonly quota: EngineQuota;
  readonly error_code?: "network" | "rate_limited" | "sign_in" | "invalid_response" | "unsupported";
}
/** Native identity proof; deliberately not a replayable transcript. */
export interface EngineHistoryMessage {
  readonly role: string;
  readonly engine_turn_id?: string;
  readonly provider_item_id?: string;
  readonly steered?: boolean;
}
export interface EngineHistoryForkRequest {
  readonly ref: string;
  readonly cwd: string;
  readonly retained: readonly EngineHistoryMessage[] | null;
  /** null means fork; a non-null prefix identifies the edited turn. */
  readonly removed: readonly EngineHistoryMessage[] | null;
  readonly whole_turn: boolean;
}
export interface EngineHistoryForkResult { readonly ref: string }
export interface EngineAdminMethods {
  readonly describe: { readonly input: Record<string, never>; readonly output: EngineDescription };
  readonly models: { readonly input: EngineModelsRequest; readonly output: EngineModelsResult };
  readonly auth: { readonly input: EngineAuthRequest; readonly output: EngineAuthResult };
  readonly quota: { readonly input: Record<string, never>; readonly output: EngineQuotaResult };
  readonly history_fork: { readonly input: EngineHistoryForkRequest; readonly output: EngineHistoryForkResult };
}
