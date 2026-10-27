import { createContext, useContext, useMemo, type ReactNode } from "react";
import type { ApplicationPreviewContentV1 } from "../../../packages/plugin-sdk/src/ui-preview-content";
import type { UICompositionV1 } from "../../../packages/plugin-sdk/src/ui";

const PreviewContentContext = createContext<{
  service: ApplicationPreviewContentV1;
  slots?: UICompositionV1;
} | undefined>(undefined);

export function PreviewContentProvider({ service, slots, children }: {
  service: ApplicationPreviewContentV1;
  slots?: UICompositionV1;
  children: ReactNode;
}): JSX.Element {
  const value = useMemo(() => ({ service, slots }), [service, slots]);
  return <PreviewContentContext.Provider value={value}>{children}</PreviewContentContext.Provider>;
}

export function usePreviewContent() {
  const context = useContext(PreviewContentContext);
  if (!context) throw new Error("Workspace file content requires PreviewContentProvider");
  return context;
}

export function useOptionalPreviewContent() { return useContext(PreviewContentContext); }
