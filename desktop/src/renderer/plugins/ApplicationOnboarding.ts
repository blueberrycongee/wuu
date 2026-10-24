import type { SetStateAction } from "react";
import type { ApplicationOnboardingV1, ApplicationOnboardingViewV1, ApplicationOnboardingWorkflowV1, OnboardingCallOptionsV1 } from "../../../../packages/plugin-sdk/src/ui-onboarding";
import type { AppState } from "../AppState";
import type { FirstRunOnboardingState } from "../FirstRunOnboarding";

function immutable<T>(value: T): T {
  const copy = structuredClone(value);
  const freeze = (item: unknown): void => {
    if (!item || typeof item !== "object") return;
    Object.values(item).forEach(freeze); Object.freeze(item);
  };
  freeze(copy); return copy;
}

/** Keeps setup continuity outside the selected renderer without owning a second application store. */
export class ApplicationOnboarding implements ApplicationOnboardingV1 {
  private disposed = false;
  private completing = false;
  private view?: ApplicationOnboardingViewV1;
  private workflow?: FirstRunOnboardingState;
  private readonly listeners = new Set<() => void>();
  constructor(private readonly host: {
    getState: () => AppState;
    updateState: (update: SetStateAction<AppState>) => void;
    selectImportedWorkspace: (projectID: string, signal?: AbortSignal) => Promise<void>;
  }) {}
  private check(options?: OnboardingCallOptionsV1): void {
    if (this.disposed) throw new Error("Onboarding application disposed");
    options?.signal?.throwIfAborted();
  }
  private source(): string {
    const state = this.host.getState();
    return JSON.stringify([state.activeContext, state.runtimeConnectionGeneration]);
  }
  private finish(source: string, options?: OnboardingCallOptionsV1): void {
    this.check(options);
    if (source !== this.source()) throw new Error("Onboarding source changed during the operation");
  }
  getSnapshot = (): ApplicationOnboardingViewV1 => {
    this.check(); const state = this.host.getState();
    const completed = state.onboardingComplete ?? window.wuu.initialOnboardingComplete ?? true;
    if (this.view && this.workflow === state.onboarding && this.view.completed === completed) return this.view;
    this.workflow = state.onboarding;
    const workflow: ApplicationOnboardingWorkflowV1 | undefined = state.onboarding ? {
      ...state.onboarding, selectedPluginIDs: [...state.onboarding.selectedPluginIDs], selectedPaths: [...state.onboarding.selectedPaths],
    } : undefined;
    this.view = immutable({ revision: (this.view?.revision ?? -1) + 1, completed, workflow, capabilities: {
      discoverWorkspaces: typeof window.wuu.discoverRecentWorkspaces === "function",
      chooseWorkspace: typeof window.wuu.chooseWorkspaceDirectory === "function",
      browserImport: window.wuu.platform === "darwin" && typeof window.wuu.listBrowserProfiles === "function" && typeof window.wuu.importBrowserCookies === "function",
    } });
    return this.view;
  };
  subscribe = (listener: () => void): (() => void) => {
    this.check(); this.listeners.add(listener); return () => { this.listeners.delete(listener); };
  };
  notifyChanged(): void {
    if (this.disposed) return;
    const previous = this.view; const next = this.getSnapshot();
    if (previous !== next) for (const listener of this.listeners) listener();
  }
  checkpoint(input: Parameters<ApplicationOnboardingV1["checkpoint"]>[0]): void {
    this.check();
    if (input.revision !== this.getSnapshot().revision) throw new Error("Onboarding revision changed; read the current setup flow");
    // Cloning removes readonly projection ownership before returning to the canonical host state.
    const onboarding = structuredClone({ ...input.workflow, selectedPluginIDs: new Set(input.workflow.selectedPluginIDs),
      selectedPaths: new Set(input.workflow.selectedPaths) }) as FirstRunOnboardingState;
    this.host.updateState(current => ({ ...current, onboarding })); this.notifyChanged();
  }
  async discoverWorkspaces(options?: OnboardingCallOptionsV1) {
    this.check(options); const source = this.source();
    if (!window.wuu.discoverRecentWorkspaces) throw new Error("Workspace discovery is unavailable");
    const result = await window.wuu.discoverRecentWorkspaces(); this.finish(source, options); return immutable(result);
  }
  async chooseWorkspace(options?: OnboardingCallOptionsV1) {
    this.check(options); const source = this.source();
    if (!window.wuu.chooseWorkspaceDirectory) throw new Error("Workspace selection is unavailable");
    const path = await window.wuu.chooseWorkspaceDirectory(); this.finish(source, options); return path ?? null;
  }
  async complete(input: Parameters<ApplicationOnboardingV1["complete"]>[0], options?: OnboardingCallOptionsV1): Promise<void> {
    this.check(options);
    if (this.completing) throw new Error("Onboarding completion is already pending");
    if (!window.wuu.completeOnboarding) throw new Error("Onboarding completion is unavailable");
    this.completing = true; const source = this.source();
    try {
      if (input.paths.length > 0) {
        if (!window.wuu.importWorkspaces) throw new Error("Workspace import is unavailable");
        const paths = [...input.paths]; const imported = await window.wuu.importWorkspaces(paths); this.finish(source, options);
        const first = imported.projects.find(project => project.path === paths[0]);
        if (!first) throw new Error("The selected workspace was not imported");
        await this.host.selectImportedWorkspace(first.id, options?.signal); this.check(options);
      }
      await window.wuu.completeOnboarding();
      if (!this.disposed) {
        this.host.updateState(current => ({ ...current, onboardingComplete: true, onboarding: undefined })); this.notifyChanged();
      }
      this.check(options);
    } finally { this.completing = false; }
  }
  dispose(): void { this.disposed = true; this.listeners.clear(); }
}
