import { useCallback, useEffect, useState } from "react";

export type WorkspaceBrowserNavigationRequest = {
  url: string;
  reuseKey: string;
  requestID: number;
};

let nextWorkspaceBrowserNavigationID = 1;
const workspaceBrowserNavigationListeners = new Set<
  (request: WorkspaceBrowserNavigationRequest) => void
>();
let latestWorkspaceBrowserNavigation:
  | WorkspaceBrowserNavigationRequest
  | undefined;

export function requestWorkspaceBrowserNavigation(input: {
  url: string;
  reuseKey: string;
}): WorkspaceBrowserNavigationRequest {
  const request: WorkspaceBrowserNavigationRequest = {
    url: input.url,
    reuseKey: input.reuseKey,
    requestID: nextWorkspaceBrowserNavigationID,
  };
  nextWorkspaceBrowserNavigationID += 1;
  latestWorkspaceBrowserNavigation = request;
  for (const listener of workspaceBrowserNavigationListeners) {
    listener(request);
  }
  return request;
}

export function subscribeWorkspaceBrowserNavigation(
  listener: (request: WorkspaceBrowserNavigationRequest) => void,
): () => void {
  workspaceBrowserNavigationListeners.add(listener);
  if (latestWorkspaceBrowserNavigation) {
    listener(latestWorkspaceBrowserNavigation);
  }
  return () => {
    workspaceBrowserNavigationListeners.delete(listener);
  };
}

export function useWorkspaceBrowserNavigationRequest():
  | WorkspaceBrowserNavigationRequest
  | undefined {
  const [request, setRequest] = useState(latestWorkspaceBrowserNavigation);
  useEffect(() => subscribeWorkspaceBrowserNavigation(setRequest), []);
  return request;
}

export function useWorkspaceBrowserNavigationConsumer(): (
  requestID: number,
) => void {
  return useCallback((requestID: number) => {
    if (latestWorkspaceBrowserNavigation?.requestID === requestID) {
      latestWorkspaceBrowserNavigation = undefined;
    }
  }, []);
}

export function resetWorkspaceBrowserNavigationForTests(): void {
  nextWorkspaceBrowserNavigationID = 1;
  latestWorkspaceBrowserNavigation = undefined;
  workspaceBrowserNavigationListeners.clear();
}
