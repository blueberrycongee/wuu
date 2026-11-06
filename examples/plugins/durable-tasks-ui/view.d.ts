import type {ConnectDesktopSessionV1, DesktopRemoteSessionRouteV1} from '@wuu/plugin-sdk';
/** Explicit remote-only mount. Disposing detaches this view, never remote work. */
export function mountDurableTasks(root: HTMLElement, options: {
  connectDesktopSession: ConnectDesktopSessionV1;
  route: DesktopRemoteSessionRouteV1;
  sessionId: string;
}): Promise<() => Promise<void>>;
