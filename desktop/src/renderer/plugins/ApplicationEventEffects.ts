import type { ApplicationEnvironmentV1 } from "../../../../packages/plugin-sdk/src/ui-environment";
import type { ServerEvent, StreamNotice, TurnEventNotification, UserQuestionRequest } from "../../shared/protocol";
import { activeThreadIDForState, serverEventShouldRefreshGit, serverEventTargetsActiveContext, type AppState } from "../AppState";
import { turnTelemetryStore } from "../TurnTelemetryStore";
import { externalAgentActivityStore } from "../ExternalAgentActivityStore";
import { showErrorToast, showToast } from "../Toast";
import { translateCurrent } from "../i18n";

/** Effects share the controller's event ordering, without another subscriber or reducer. */
export class ApplicationEventEffects {
  private readonly lifetime = new AbortController();
  private readonly shownNotices = new Set<string>();

  constructor(private readonly host: {
    getState: () => AppState;
    getEnvironment: () => Pick<ApplicationEnvironmentV1, "scheduleStatusRefresh">;
    notify?: (input: { title: string; body: string }) => Promise<unknown>;
    reportError: (error: unknown) => void;
  }) {}

  dispose(): void {
    this.lifetime.abort();
  }

  receive(event: ServerEvent): void {
    if (this.lifetime.signal.aborted) return;
    // These stores also represent background workspaces; active-context routing
    // only limits the visible runtime's error notices and environment refresh.
    turnTelemetryStore.ingest(event);
    externalAgentActivityStore.ingest(event);
    const state = this.host.getState();
    if (event.kind === "notification") {
      if (event.message.method === "user-question/requested") {
        const request = (event.message.params as { request?: UserQuestionRequest } | undefined)?.request;
        const thread = request && state.threads.find(item => item.id === request.thread_id);
        if (request && !thread?.parent_id && !thread?.ephemeral) {
          void this.notify({ title: translateCurrent("notification.questionTitle"), body: translateCurrent("notification.questionBody") });
        }
      } else if (event.message.method === "turn/completed") {
        const params = event.message.params as { thread_id?: string } | undefined;
        const thread = state.threads.find(item => item.id === params?.thread_id);
        if (thread && !thread.parent_id && !thread.ephemeral) {
          void this.notify({ title: translateCurrent("notification.turnCompletedTitle"), body: translateCurrent("notification.turnCompletedBody") });
        }
      }
    }
    if (!serverEventTargetsActiveContext(event, state)) return;
    // Before initialization the launch view already presents these failures.
    if (state.initialized) {
      if (event.kind === "server-error") {
        showErrorToast(event.message, translateCurrent("error.server"));
      } else if (event.kind === "server-exit") {
        showErrorToast(event.message, translateCurrent("appState.coreExited"), "app-server:exit");
      } else if (event.kind === "notification" && event.message.method === "config/error") {
        const message = (event.message.params as { message?: unknown } | undefined)?.message;
        if (typeof message === "string") showErrorToast(message, "", "app-server:config-error");
      } else if (event.kind === "notification" && event.message.method === "turn/event") {
        this.showEngineNotice(event.message.params as TurnEventNotification | undefined, state);
      }
    }
    // The existing predicate only accepts completed/error turns, never stream
    // deltas, so no second streaming ingestion/classification is necessary.
    if (serverEventShouldRefreshGit(event)) {
      this.host.getEnvironment().scheduleStatusRefresh(600, { signal: this.lifetime.signal });
    }
  }

  private showEngineNotice(params: TurnEventNotification | undefined, state: AppState): void {
    const notice = params?.event.notice;
    const threadID = params?.thread_id;
    if (!notice || !threadID || threadID !== activeThreadIDForState(state)) return;
    const key = `${threadID}:${notice.code || notice.message}`;
    if (this.shownNotices.has(key)) return;
    this.shownNotices.add(key);
    showToast({ message: engineNoticeText(notice), dedupeKey: `engine-notice:${key}` });
  }

  private async notify(input: { title: string; body: string }): Promise<void> {
    try {
      await this.host.notify?.(input);
    } catch (error) {
      if (!this.lifetime.signal.aborted) this.host.reportError(error);
    }
  }
}

function engineNoticeText(notice: StreamNotice): string {
  if (notice.code === "mcp_unavailable") return translateCurrent("runtime.mcpUnavailable");
  return notice.message;
}
