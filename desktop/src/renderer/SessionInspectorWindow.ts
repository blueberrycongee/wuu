import { useCallback, useEffect, useRef, useState, type RefObject } from "react";
import { flushSync } from "react-dom";
import { useI18n } from "./i18n";
import { showErrorToast } from "./Toast";

export type SessionInspectionTarget = { roomID: string; sessionRef: string; name: string };
type Extension = { baseWidth: number; panelWidth: number; gutter: string; target?: SessionInspectionTarget };

export function useSessionInspectorWindow(root: RefObject<HTMLElement | null>) {
  const { t } = useI18n();
  const [extension, setExtension] = useState<Extension | null>(null);
  const current = useRef<Extension | null>(null);
  const desired = useRef<SessionInspectionTarget | null>(null);
  const queue = useRef<Promise<void>>(Promise.resolve());
  const mounted = useRef(true);
  const update = useCallback((next: Extension | null) => {
    current.current = next;
    if (mounted.current) flushSync(() => setExtension(next));
  }, []);
  const enqueue = useCallback((work: () => Promise<void>) => {
    queue.current = queue.current.catch(() => {}).then(work).catch((error) => { if (mounted.current) showErrorToast(error); });
    return queue.current;
  }, []);
  const close = useCallback(() => {
    desired.current = null;
    return enqueue(async () => {
      if (!current.current) return;
      await window.wuu?.setSessionInspectorExpansion?.({ open: false });
      update(null);
    });
  }, [enqueue, update]);
  const open = useCallback((target: SessionInspectionTarget) => {
    desired.current = target;
    return enqueue(async () => {
      if (!mounted.current || desired.current !== target) return;
      if (!window.wuu?.setSessionInspectorExpansion || window.innerWidth < 760) {
        throw new Error(t("channels.inspectorWindowUnavailable"));
      }
      const stream = root.current?.querySelector<HTMLElement>(".channel-message-stream");
      const base = current.current ?? {
        baseWidth: window.innerWidth, panelWidth: 480,
        gutter: stream ? getComputedStyle(stream).paddingLeft : "24px",
      };
      // Freeze the original workspace before Electron emits the resize event.
      update(base);
      try {
        const result = await window.wuu.setSessionInspectorExpansion({ open: true, width: 480 });
        if (!result.expanded) {
          update(null);
          throw new Error(t(result.reason === "insufficient-space" ? "channels.inspectorNoSpace" : "channels.inspectorWindowUnavailable"));
        }
        if (mounted.current && desired.current === target) update({ ...base, panelWidth: result.panelWidth, target });
      } catch (error) {
        if (!current.current?.target) update(null);
        throw error;
      }
    });
  }, [enqueue, root, t, update]);
  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; void close(); };
  }, [close]);
  return { extension, open, close };
}
