/** Public display data, independent of host state and mutable transport objects. */
export type ApplicationConversationThreadV1 = ConversationThreadData;
export type ApplicationConversationTurnV1 = ConversationTurnData;
export type ApplicationConversationThreadItemV1 = ConversationThreadItemData;
export type ApplicationConversationInputImageV1 = ConversationInputImageData;
export type ApplicationConversationMessageContentPartV1 = ConversationMessageContentPartData;
export type ApplicationConversationInputFileV1 = ConversationInputFileData;
export interface ApplicationConversationWorkspaceV1 {
    readonly turnStreamStatus: Readonly<Record<string, { readonly text: string; readonly liveProgress: boolean }>>;
    readonly primaryThreadId?: string;
    readonly secondaryThreadId?: string;
    readonly activePane: "primary" | "secondary";
    readonly threads: ReadonlyArray<ApplicationConversationThreadV1>;
}
export interface ApplicationConversationTextTargetV1 {
    readonly threadId: string;
    readonly turnId: string;
    readonly itemId: string;
    readonly field: "text" | "arguments" | "result";
}
/** Per-item live text; disposal removes subscriptions without stopping the host stream. */
export interface ApplicationConversationTextV1 {
    getSnapshot(target: ApplicationConversationTextTargetV1): string | undefined;
    subscribe(target: ApplicationConversationTextTargetV1, listener: () => void): () => void;
}
type ConversationThreadData = {
    readonly model_options?: Readonly<Record<string, string>>;
    readonly speed?: string;
    readonly session_control?: {
        readonly manager_id: string;
        readonly manager_name: string;
        readonly state: "active" | "paused" | "taken_over";
        readonly revision: number;
    };
    readonly id: string;
    readonly parent_id?: string;
    readonly agent_path?: string;
    readonly preview: string;
    readonly title?: string;
    readonly title_revision?: number;
    readonly source?: string;
    readonly project_id?: string;
    readonly project_exists?: boolean;
    readonly project_role?: "side" | "worker";
    readonly model_provider: string;
    readonly model: string;
    readonly model_variant?: string;
    readonly model_effort?: string;
    readonly engine_id?: string;
    readonly permission_mode?: string;
    readonly approve_for_me?: boolean;
    readonly cwd: string;
    readonly workspace_id?: string;
    readonly workspace_kind?: "project" | "scratch";
    readonly status: ConversationThreadStatusData;
    readonly orchestration_interrupted?: boolean;
    readonly read_only?: boolean;
    readonly ephemeral?: boolean;
    readonly pinned?: boolean;
    readonly folder_id?: string;
    readonly archived?: boolean;
    readonly archive_reason?: string;
    readonly forked_from_id?: string;
    readonly forked_from_turn_id?: string;
    readonly forked_from_item_id?: string;
    readonly worktree?: ConversationWorktreeInfoData;
    readonly created_at: string;
    readonly updated_at: string;
    readonly latest_completed_turn_id?: string;
    readonly turns: ReadonlyArray<ConversationTurnData>;
    readonly history_cursor?: string;
    readonly child_agents?: ReadonlyArray<ConversationAgentData>;
};
type ConversationThreadStatusData = "idle" | "in_progress";
type ConversationWorktreeInfoData = {
    readonly path: string;
    readonly base_head?: string;
    readonly base_repo?: string;
    readonly dirty?: boolean;
    readonly changed_files?: ReadonlyArray<string>;
};
type ConversationTurnData = {
    readonly id: string;
    readonly kind?: ConversationTurnKindData;
    readonly model_provider?: string;
    readonly model?: string;
    readonly items: ReadonlyArray<ConversationThreadItemData>;
    readonly items_view: ConversationTurnItemsViewData;
    readonly has_final_answer?: boolean;
    readonly status: ConversationTurnStatusData;
    readonly finish_reason?: string;
    readonly stop_reason?: string;
    readonly truncated?: boolean;
    readonly error?: ConversationTurnErrorData;
    readonly started_at?: string | null;
    readonly answer_ready_at?: string | null;
    readonly completed_at?: string | null;
    readonly duration_ms?: number;
    readonly input_tokens?: number;
    readonly output_tokens?: number;
    readonly context_tokens?: number;
    readonly cache_creation_tokens?: number;
    readonly cache_read_tokens?: number;
    readonly usage_model?: string;
};
type ConversationTurnKindData = "user" | "internal" | "compact";
type ConversationThreadItemData = {
    readonly activity?: {
        readonly last_item_id: string;
        readonly item_count: number;
        readonly tool_count: number;
        readonly failed_count: number;
    };
    readonly markdown_images?: ReadonlyArray<ConversationInputImageData>;
    readonly remote_content_ref?: string;
    readonly id: string;
    readonly seq?: number;
    readonly source_id?: string;
    readonly agent_id?: string;
    readonly type: ConversationThreadItemTypeData;
    readonly status?: ConversationThreadItemStatusData;
    readonly terminal?: boolean;
    readonly role?: string;
    readonly text?: string;
    readonly content_parts?: ReadonlyArray<ConversationMessageContentPartData>;
    readonly input_text?: string;
    readonly related_session_id?: string;
    readonly images?: ReadonlyArray<ConversationInputImageData>;
    readonly files?: ReadonlyArray<ConversationInputFileData>;
    readonly name?: string;
    readonly read_only?: boolean;
    readonly origin?: string;
    readonly origin_id?: string;
    readonly cause?: string;
    readonly presentation_kind?: string;
    readonly arguments?: string;
    readonly display?: ConversationToolCallDisplayData;
    readonly result?: string;
    readonly result_detail?: ConversationToolResultData;
    readonly error?: string;
    readonly reason?: string;
    readonly finish_reason?: string;
    readonly stop_reason?: string;
    readonly truncated?: boolean;
    readonly summary?: string;
    readonly retry_count?: number;
    readonly max_retries?: number;
    readonly retry_at_ms?: number;
};
type ConversationInputImageData = {
    readonly media_type: string;
    readonly data: string;
    readonly width?: number;
    readonly height?: number;
    readonly remote_ref?: string;
};
type ConversationThreadItemTypeData = "user_message" | "agent_message" | "reasoning" | "tool_call" | "process_summary" | "context_compaction" | "stream_reconnect" | "error";
type ConversationThreadItemStatusData = "in_progress" | "completed" | "failed";
type ConversationMessageContentPartData = {
    readonly type: "text";
    readonly text: string;
} | {
    readonly type: "pasted_text";
    readonly text: string;
    readonly title?: string;
    readonly local_path?: string;
} | {
    readonly type: "response_selection";
    readonly text: string;
    readonly selection: ConversationResponseSelectionData;
} | {
    readonly type: "file_selection";
    readonly text: string;
    readonly source: ConversationFileSelectionSourceData;
    readonly intent: "comment" | "edit" | "quote";
    readonly comment?: string;
    readonly id: string;
};
type ConversationResponseSelectionData = {
    readonly id: string;
    readonly text: string;
    readonly comment?: string;
    readonly source: {
        readonly thread_id: string;
        readonly turn_id: string;
        readonly item_id: string;
        readonly range_text?: string;
        readonly start_offset: number;
        readonly end_offset: number;
    };
};
type ConversationFileSelectionSourceData = {
    readonly workspace: string;
    readonly path: string;
    readonly start_line: number;
    readonly start_column: number;
    readonly end_line: number;
    readonly end_column: number;
    readonly quote: string;
    readonly revision: string;
    readonly pdf?: ConversationPdfSelectionReferenceData;
};
type ConversationPdfSelectionReferenceData = {
    readonly start_page: number;
    readonly end_page: number;
    readonly artifact_uri?: string;
    readonly artifact_sha256?: string;
    readonly artifact_thread_id?: string;
};
type ConversationInputFileData = {
    readonly media_type: string;
    readonly data: string;
    readonly remote_ref?: string;
    readonly filename?: string;
};
type ConversationToolCallDisplayData = {
    readonly kind?: string;
    readonly label?: string;
    readonly label_translations?: Readonly<Record<string, string>>;
    readonly text?: string;
    readonly capability?: string;
};
type ConversationToolResultData = {
    readonly content?: ReadonlyArray<ConversationToolResultContentPartData>;
    readonly structured_content?: ConversationJsonValueData;
    readonly meta?: ConversationJsonValueData;
    readonly is_error?: boolean;
    readonly activity?: ConversationToolResultActivityRefData;
};
type ConversationToolResultContentPartData = {
    readonly remote_ref?: string;
    readonly type: string;
    readonly text?: string;
    readonly data?: string;
    readonly mime_type?: string;
    readonly uri?: string;
    readonly name?: string;
    readonly resource?: ConversationJsonValueData;
    readonly artifact?: {
        readonly placement?: "inline" | "turn_end";
        readonly ref?: string;
        readonly sha256?: string;
        readonly size_bytes?: number;
    };
};
type ConversationJsonValueData = null | boolean | number | string | ReadonlyArray<ConversationJsonValueData> | {
    readonly [key: string]: ConversationJsonValueData;
};
type ConversationToolResultActivityRefData = {
    readonly id: string;
    readonly kind: string;
    readonly state?: string;
    readonly thread_id?: string;
    readonly preview_uri?: string;
};
type ConversationTurnItemsViewData = "full" | "conversation";
type ConversationTurnStatusData = "in_progress" | "completed" | "failed" | "interrupted";
type ConversationTurnErrorData = {
    readonly message: string;
    readonly code?: string;
    readonly category?: ConversationTurnErrorCategoryData;
    readonly provider?: string;
    readonly status_code?: number;
    readonly recovery?: ConversationStreamRecoveryInfoData;
};
type ConversationTurnErrorCategoryData = "cancelled" | "network" | "auth" | "provider" | "invalid_request" | "tool" | "local" | "internal";
type ConversationStreamRecoveryInfoData = {
    readonly operation_id?: string;
    readonly attempt_count: number;
    readonly retry_count: number;
    readonly max_attempts: number;
    readonly submission_count: number;
    readonly stop_reason: string;
    readonly failure_category: string;
    readonly budget_dimension?: string;
};
type ConversationAgentData = {
    readonly id: string;
    readonly type?: string;
    readonly task_name?: string;
    readonly agent_profile?: string;
    readonly agent_path?: string;
    readonly parent_id?: string;
    readonly description?: string;
    readonly status: string;
    readonly model_alias?: string;
    readonly model_alias_fallback?: boolean;
    readonly provider?: string;
    readonly model?: string;
    readonly api_model?: string;
    readonly effort?: string;
    readonly variant?: string;
    readonly result?: string;
    readonly error?: string;
    readonly input_tokens?: number;
    readonly output_tokens?: number;
    readonly cache_creation_tokens?: number;
    readonly cache_read_tokens?: number;
    readonly nested_count?: number;
    readonly nested_running_count?: number;
    readonly started_at?: string;
    readonly completed_at?: string | null;
    readonly pinned?: boolean;
    readonly archived?: boolean;
    readonly participant?: ConversationParticipantSummaryData;
};
type ConversationParticipantSummaryData = {
    readonly id: string;
    readonly name: string;
    readonly kind: string;
    readonly role?: string;
    readonly avatar_image?: string;
    readonly busy?: boolean;
};

type ConversationThreadContextCompositionResultData = {
    readonly thread_id: string;
    readonly available: boolean;
    readonly reason?: string;
    readonly mode?: string;
    readonly trace_path?: string;
    readonly turn_id?: string;
    readonly step_index?: number;
    readonly provider?: string;
    readonly model?: string;
    readonly context_window_tokens?: number;
    readonly input_limit_tokens?: number;
    readonly usable_input_tokens?: number;
    readonly compact_threshold_tokens?: number;
    readonly prompt_tokens?: number;
    readonly total_context_tokens?: number;
    readonly retained_tokens?: number;
    readonly input_tokens?: number;
    readonly output_tokens?: number;
    readonly cache_creation_tokens?: number;
    readonly cache_read_tokens?: number;
    readonly token_estimate_source?: string;
    readonly message_count?: number;
    readonly system_messages?: number;
    readonly hidden_messages?: number;
    readonly tool_count?: number;
    readonly stable_prefix?: number;
    readonly turn_prefix?: number;
    readonly dynamic_context_bytes?: number;
    readonly system_hash?: string;
    readonly stable_prefix_hash?: string;
    readonly turn_prefix_hash?: string;
    readonly tool_surface_hash?: string;
    readonly prompt_cache_key?: string;
    readonly categories?: ReadonlyArray<ConversationContextCompositionCategoryData>;
    readonly system_sections?: ReadonlyArray<ConversationContextCompositionSectionData>;
    readonly block_kind_bytes?: Readonly<Record<string, number>>;
    readonly segment_counts?: ConversationContextSegmentCountSummaryData;
};
type ConversationContextCompositionCategoryData = {
    readonly id: string;
    readonly label: string;
    readonly description?: string;
    readonly tone?: string;
    readonly bytes?: number;
    readonly tokens?: number;
    readonly contributes: boolean;
    readonly durable?: boolean;
    readonly cache_scope?: string;
    readonly request_only?: boolean;
    readonly deferred?: boolean;
};
type ConversationContextCompositionSectionData = {
    readonly key: string;
    readonly static: boolean;
    readonly bytes: number;
    readonly tokens?: number;
    readonly hash?: string;
};
type ConversationContextSegmentCountSummaryData = {
    readonly lifecycle?: Readonly<Record<string, number>>;
    readonly placement?: Readonly<Record<string, number>>;
    readonly cache_policy?: Readonly<Record<string, number>>;
};
type ConversationInstructionsListResultData = {
    readonly files: ReadonlyArray<ConversationInstructionFileData>;
};
type ConversationInstructionFileData = {
    readonly path: string;
    readonly name: string;
    readonly source: string;
    readonly scope: ConversationInstructionFileScopeData;
    readonly bytes: number;
    readonly content: string;
};
type ConversationInstructionFileScopeData = "global" | "project";

export type ApplicationConversationDetailV1 = {
  readonly id: string;
  readonly threadID: string;
  readonly title?: string;
  readonly loading: boolean;
  readonly error?: string;
} & (
  | { readonly kind: "context"; readonly afterTurnID?: string; readonly result?: ConversationThreadContextCompositionResultData }
  | { readonly kind: "instructions"; readonly result?: ConversationInstructionsListResultData }
);
export interface ApplicationConversationDetailsActionMapV1 {
  "conversation.open-context": { input: { readonly threadId: string }; result: { readonly entryId: string } };
  "conversation.open-instructions": { input: { readonly threadId: string }; result: { readonly entryId: string } };
  "conversation.dismiss-detail": { input: { readonly entryId: string }; result: void };
}
