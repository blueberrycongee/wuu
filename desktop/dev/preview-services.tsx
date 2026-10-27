import { DesktopControlsProvider } from "../src/renderer/DesktopControlsContext";
import type { ReactNode } from "react";
import { createPreviewApplication } from "../../packages/plugin-sdk/preview/application";
import type { ApplicationActionMapV1, ApplicationSnapshotV1, ApplicationServiceV1, UICompositionV1 } from "../../packages/plugin-sdk/src/ui";
import type { ApplicationPreviewTabV1 } from "../../packages/plugin-sdk/src/ui-preview";
import { ConversationControlsProvider } from "../src/renderer/ConversationControlsContext";
import { ArtifactsProvider } from "../src/renderer/ArtifactsContext";
import { SettingsControlsProvider } from "../src/renderer/SettingsControlsContext";
import { BrowserControlsProvider } from "../src/renderer/BrowserControlsContext";

export const fixtureSlots: UICompositionV1 = { Slot: ({fallback}) => fallback ?? null };

/** Each fixture owns synthetic state only; no preload or production controller. */
export function createFixtureApplication(initial: Partial<ApplicationSnapshotV1> = {}, readUsage?: () => Promise<ApplicationActionMapV1["conversation.read-usage"]["result"]>): ApplicationServiceV1 {
  const base = createPreviewApplication();
  let snapshot = {...base.getSnapshot(), previewWorkspace:{...base.getSnapshot().previewWorkspace, activeContext:{kind:"no_project" as const,cwd:"/preview"}, workspaceContext:{kind:"no_project" as const,cwd:"/preview"}}, ...initial};
  const listeners = new Set<() => void>();
  const unavailable = (): never => {throw new Error("This capability is unavailable in the synthetic preview");};
  const fileSelections = new Map<string, ReturnType<typeof base.fileSelection.getSnapshot>>();
  const environment = {actionBusy:false, contextKey:"preview", root:"/preview", capabilities:{review:false, checkout:false, createBranch:false, commit:false, pullRequest:false}};
  const update = (tabs: readonly ApplicationPreviewTabV1[], activeTabId?: string) => {
    snapshot = {...snapshot, revision: snapshot.revision + 1, previewWorkspace:{...snapshot.previewWorkspace, tabs, activeTabId}};
    listeners.forEach(listener => listener());
  };
  return {...base,
    getSnapshot: () => snapshot,
    subscribe: listener => {listeners.add(listener); return () => {listeners.delete(listener);};},
    fileSelection: {...base.fileSelection, getSnapshot: target => {
      let view = fileSelections.get(target.ownerTabId);
      if (!view) {view = base.fileSelection.getSnapshot(target); fileSelections.set(target.ownerTabId,view);}
      return view;
    }},
    environment: {getSnapshot: () => environment, subscribe: () => () => {}, listChanges: unavailable, readDiff: unavailable,
      checkoutBranch: unavailable, createAndCheckoutBranch: unavailable, commit: unavailable, generateCommitMessage: unavailable,
      createPullRequest: unavailable, scheduleStatusRefresh: () => {}},
    previewContent: {...base.previewContent,
      resolveResourceURL: ({source}) => /^(https?:|data:|blob:)/.test(source) ? source : undefined,
      listDirectory: async ({root, path}) => ({root, path, truncated:false, entries: path ? [] : ["README.md", "排版验收说明.md", ...Array.from({length:40}, (_, i) => `component-${i}.tsx`)].map(name => ({name, path:name, kind:"file" as const}))}),
    },
    settingsControls: {...base.settingsControls, readBackground: async () => null},
    async invoke<A extends keyof ApplicationActionMapV1>(action: A, input: ApplicationActionMapV1[A]["input"], options?: {readonly signal?: AbortSignal}): Promise<ApplicationActionMapV1[A]["result"]> {
      options?.signal?.throwIfAborted();
      const current = snapshot.previewWorkspace;
      let tab: ApplicationPreviewTabV1 | undefined;
      switch (action) {
        case "conversation.read-usage": return (readUsage ? await readUsage() : unavailable()) as ApplicationActionMapV1[A]["result"];
        case "preview.focus-tab": update(current.tabs, (input as ApplicationActionMapV1["preview.focus-tab"]["input"]).tabId); break;
        case "preview.close-tab": update(current.tabs.filter(tab => tab.id !== (input as ApplicationActionMapV1["preview.close-tab"]["input"]).tabId)); break;
        case "preview.new-tab": {const tabId = `new:${crypto.randomUUID()}`; update([...current.tabs, {id:tabId, kind:"new"}],tabId); return {tabId} as ApplicationActionMapV1[A]["result"]; }
        case "preview.open-tool": {const {toolId} = input as ApplicationActionMapV1["preview.open-tool"]["input"]; tab = {id:toolId, kind:toolId}; break;}
        case "preview.open-project": {const {projectId, title} = input as ApplicationActionMapV1["preview.open-project"]["input"]; tab = {id:`project:${projectId}`, kind:"project", projectID:projectId, title}; break;}
        case "preview.open-file": {const {context, path} = input as ApplicationActionMapV1["preview.open-file"]["input"]; tab = {id:`file:${path}`, kind:"file", context, path, title:path}; break;}
        default: return unavailable();
      }
      if (tab) update(current.tabs.some(item => item.id === tab.id) ? current.tabs : [...current.tabs, tab], tab.id);
      return undefined as ApplicationActionMapV1[A]["result"];
    },
  };
}

export function FixtureServices({application, children}: {application: ApplicationServiceV1; children: ReactNode}) {
  return <ConversationControlsProvider application={application} slots={fixtureSlots} slotOwner="preview-fixture">
    <ArtifactsProvider application={application}><SettingsControlsProvider controls={application.settingsControls}>
      <DesktopControlsProvider controls={application.desktop}><BrowserControlsProvider service={application.browser}>{children}</BrowserControlsProvider></DesktopControlsProvider>
    </SettingsControlsProvider></ArtifactsProvider>
  </ConversationControlsProvider>;
}
