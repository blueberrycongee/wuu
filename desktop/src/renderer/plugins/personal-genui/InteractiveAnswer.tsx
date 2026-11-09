import * as React from "react";
import type { PluginUIInteractiveAnswerProps } from "../../../shared/workbench";
import { decodeSnapshot } from "./snapshot";
import type { ActionResult } from "../../../../../plugins/personal/src/genui/service";
import { PersonalGenUISurface } from "./PersonalGenUISurface";


export function InteractiveAnswer({ snapshot, onStateChange, onSync, onAction, onRefresh, onRetry, fallback }: PluginUIInteractiveAnswerProps & { fallback: string }): React.ReactNode {
  let initial: ActionResult;
  try { initial = decodeSnapshot(snapshot); } catch { return <p role="status">{fallback || "这个界面暂时无法显示，请用文字继续。"}</p>; }
  return <PersonalGenUISurface key={initial.surface.surfaceId} initial={initial} transport={{ onStateChange, sync: onSync ? async input => decodeSnapshot(await onSync(input)) : undefined, dispatch: async input => decodeSnapshot(await onAction(input)), read: async () => decodeSnapshot(await onRefresh()), retry: async resumeHeld => decodeSnapshot(await onRetry(resumeHeld)) }} />;
}
