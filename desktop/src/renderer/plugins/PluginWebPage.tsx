import * as React from "react";
import type { PluginUIWebPageProps } from "../../shared/workbench";
import type { ApplicationWebWorkspaceV1 } from "../../../../packages/plugin-sdk/src/ui-web-workspace";
import { WebWorkspaceControlsProvider } from "../WebWorkspaceControlsContext";
import { WebWorkspacePanel } from "../WebWorkspace";

/** Only views owning a native page need to survive ordinary tab switches. */
export const PluginWebPageViewContext = React.createContext<{
  active: boolean;
  retain: () => () => void;
} | undefined>(undefined);

/** Bind the existing website surface to the extension generation's lifetime. */
export function createPluginWebPage(
  react: typeof React,
  signal: AbortSignal,
  reportError: (error: unknown) => void,
  controls: ApplicationWebWorkspaceV1,
): React.ComponentType<PluginUIWebPageProps> {
  const subscribe = (listener: () => void): (() => void) => {
    signal.addEventListener("abort", listener);
    return () => signal.removeEventListener("abort", listener);
  };
  const isActive = (): boolean => !signal.aborted;

  return function PluginWebPage({ url, title }: PluginUIWebPageProps): React.ReactNode {
    const [id] = react.useState(() => `plugin-web-page:${crypto.randomUUID()}`);
    const generationActive = react.useSyncExternalStore(subscribe, isActive, isActive);
    const view = react.useContext(PluginWebPageViewContext);
    const site = react.useMemo(() => ({ id, url, title }), [id, url, title]);

    react.useLayoutEffect(() => view?.retain(), [view?.retain]);

    const [browser, setBrowser] = react.useState<ApplicationWebWorkspaceV1>();
    react.useEffect(() => {
      if (signal.aborted) return;
      const owner = controls.fork();
      setBrowser(owner);
      return () => { void owner.dispose().catch(reportError); };
    }, []);

    return generationActive && browser ? react.createElement(WebWorkspaceControlsProvider, {
      controls: browser,
      children: react.createElement(WebWorkspacePanel, { site, active: view?.active ?? true }),
    }) : null;
  };
}
