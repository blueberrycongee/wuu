import {
  createContext,
  type ReactNode,
  useContext,
  useEffect,
  useState,
} from "react";
import { createPortal } from "react-dom";
import { startFocusModality } from "../../FocusModality";

const UILayerHostContext = createContext<HTMLElement | null>(null);

export interface WuuUIRootProps {
  children: ReactNode;
}

/**
 * Owns the protected DOM target for host overlays. It intentionally sits
 * outside replaceable plugin surfaces while retaining React context for
 * dialogs, menus, popovers, tooltips, and notices rendered through portals.
 */
export function WuuUIRoot({ children }: WuuUIRootProps): JSX.Element {
  const [layerHost, setLayerHost] = useState<HTMLDivElement | null>(null);
  useEffect(() => startFocusModality(), []);

  return (
    <UILayerHostContext.Provider value={layerHost}>
      {children}
      <div
        ref={setLayerHost}
        data-wuu-layer-host="true"
        data-wuu-component="layer-host"
      />
    </UILayerHostContext.Provider>
  );
}

/**
 * Standalone component tests and legacy secondary roots fall back to body.
 * The production renderer always provides WuuUIRoot.
 */
export function useUILayerHost(): HTMLElement {
  return useContext(UILayerHostContext) ?? document.body;
}

export type UILayerKind =
  | "dialog"
  | "menu"
  | "popover"
  | "tooltip"
  | "notice"
  | "navigation";

export interface UILayerPortalProps {
  children: ReactNode;
  layer: UILayerKind;
}

/**
 * Portal primitive for host-owned floating UI. The rendered component owns
 * its semantic data attributes so this helper never adds a layout wrapper.
 */
export function UILayerPortal({ children, layer }: UILayerPortalProps): ReactNode {
  const layerHost = useUILayerHost();
  return createPortal(children, layerHost, `wuu-layer:${layer}`);
}
