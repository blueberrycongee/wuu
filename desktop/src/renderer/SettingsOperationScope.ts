import { useLayoutEffect, useMemo } from "react";
import { useSettingsControls } from "./SettingsControlsContext";

/** A retained form keeps its DOM, not the authority or target of an old operation. */
export function useSettingsOperationScope(target = "") {
  const controls = useSettingsControls();
  const scope = useMemo(() => ({ controls, active: false }), [controls, target]);
  useLayoutEffect(() => {
    scope.active = true;
    return () => { scope.active = false; };
  }, [scope]);
  return scope;
}
