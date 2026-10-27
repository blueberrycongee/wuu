import { createContext, useContext, useSyncExternalStore, type ReactNode } from "react";
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
  const snapshot = useSyncExternalStore(service.subscribe, () => service.getSnapshot(source));
  const connection = useSyncExternalStore(application.subscribe, application.getSnapshot).connection;
  const connected = connection.status === "connected";
  return <PdfSelectionNavigationContext.Provider value={selection => service.openPdf({ target: source, source: selection })}>
    <FileSelectionContext.Provider value={!snapshot.enabled ? null : {
      ownerKey: snapshot.interactionOwnerKey,
      comments: snapshot.comments,
      addQuote(selection) {
        if (!service.quote({ target: source, source: selection })) return;
        const input = document.querySelector<HTMLTextAreaElement>(".conversation-split-pane.active textarea")
          ?? document.querySelector<HTMLTextAreaElement>(".workspace-document-composer textarea")
          ?? document.querySelector<HTMLTextAreaElement>("[data-main-conversation-composer] textarea");
        focusComposerTextarea(input, "end");
      },
      addComment: (selection, comment, id) => { service.comment({ target: source, source: selection, comment, id }); },
      removeComment: id => { service.removeComment({ target: source, id }); },
      edit: (selection, instruction) => connected ? service.edit({ target: source, source: selection, instruction }) : Promise.resolve(false),
      askSide: snapshot.canAskSide ? selection => { void service.askSide({ target: source, source: selection }).catch(showErrorToast); } : undefined,
      openFile: path => { void service.openFile({ target: source, path }).catch(showErrorToast); },
    }}>{children}</FileSelectionContext.Provider>
  </PdfSelectionNavigationContext.Provider>;
}
