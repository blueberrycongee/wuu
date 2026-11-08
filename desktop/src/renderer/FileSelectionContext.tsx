import { createContext, useContext, useMemo, useSyncExternalStore, type ReactNode } from "react";
import type { FileSelectionSource } from "../shared/protocol";
import type { ApplicationFileSelectionServiceV1, ApplicationFileSelectionTargetV1 } from "../../../packages/plugin-sdk/src/ui-file-selection";
import type { ApplicationSnapshotV1 } from "../../../packages/plugin-sdk/src/ui";
import type { FileSelectionPart } from "./FileSelectionParts";
import { focusComposerTextarea } from "./ComposerFocus";
import { showErrorToast } from "./Toast";
export { buildFileSelectionPart } from "./FileSelectionParts";
export type { FileSelectionPart } from "./FileSelectionParts";
export type { FileSelectionSource } from "../shared/protocol";

type FileSelectionActions = {
  ownerKey: string;
  comments: readonly FileSelectionPart[];
  addQuote(source: FileSelectionSource): void;
  addComment(source: FileSelectionSource, comment: string, id?: string): void;
  removeComment(id: string): void;
  edit(source: FileSelectionSource, instruction: string): Promise<boolean>;
  askSide?: (source: FileSelectionSource) => void;
  openFile(path: string): void;
};
const PdfSelectionNavigationContext = createContext<((source: FileSelectionSource) => Promise<boolean>) | undefined>(undefined);
export const usePdfSelectionNavigation = () => useContext(PdfSelectionNavigationContext);
const FileSelectionContext = createContext<FileSelectionActions | null>(null);
export const useFileSelectionActions = () => useContext(FileSelectionContext);

/** Explicitly exclude workspace selection authority from independent transports. */
export function FileSelectionIsolation({ children }: { children: ReactNode }): JSX.Element {
  return <PdfSelectionNavigationContext.Provider value={undefined}><FileSelectionContext.Provider value={null}>{children}</FileSelectionContext.Provider></PdfSelectionNavigationContext.Provider>;
}

export interface FileSelectionApplication {
  readonly fileSelection: ApplicationFileSelectionServiceV1;
  getSnapshot(): Pick<ApplicationSnapshotV1, "connection">;
  subscribe(listener: () => void): () => void;
}

/** The selected UI owns only selection menus and focus, never composer content. */
export function FileSelectionProvider({ application, source, children }: {
  application: FileSelectionApplication;
  source: ApplicationFileSelectionTargetV1;
  children: ReactNode;
}): JSX.Element {
  const service = application.fileSelection;
  // This provider can wrap the whole shell. Context values must change only with
  // their inputs: a fresh value on every publication makes React rescan every
  // mounted turn for consumers, a cost that grows with the conversation.
  const { ownerTabId } = source;
  const target = useMemo(() => ({ ownerTabId }), [ownerTabId]);
  const snapshot = useSyncExternalStore(service.subscribe, () => service.getSnapshot(target));
  const connected = useSyncExternalStore(application.subscribe, () => application.getSnapshot().connection.status === "connected");
  const openPdf = useMemo(() => (selection: FileSelectionSource) => service.openPdf({ target, source: selection }), [service, target]);
  const actions = useMemo<FileSelectionActions | null>(() => !snapshot.enabled ? null : {
    ownerKey: snapshot.interactionOwnerKey,
    comments: snapshot.comments,
    addQuote(selection) {
      if (!service.quote({ target, source: selection })) return;
      const input = document.querySelector<HTMLTextAreaElement>(".conversation-split-pane.active textarea")
        ?? document.querySelector<HTMLTextAreaElement>(".workspace-document-composer textarea")
        ?? document.querySelector<HTMLTextAreaElement>("[data-main-conversation-composer] textarea");
      focusComposerTextarea(input, "end");
    },
    addComment: (selection, comment, id) => { service.comment({ target, source: selection, comment, id }); },
    removeComment: id => { service.removeComment({ target, id }); },
    edit: (selection, instruction) => connected ? service.edit({ target, source: selection, instruction }) : Promise.resolve(false),
    askSide: snapshot.canAskSide ? selection => { void service.askSide({ target, source: selection }).catch(showErrorToast); } : undefined,
    openFile: path => { void service.openFile({ target, path }).catch(showErrorToast); },
  }, [service, target, snapshot, connected]);
  return <PdfSelectionNavigationContext.Provider value={openPdf}>
    <FileSelectionContext.Provider value={actions}>{children}</FileSelectionContext.Provider>
  </PdfSelectionNavigationContext.Provider>;
}
