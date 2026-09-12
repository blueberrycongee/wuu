import type { ServerEvent } from "../shared/protocol";

const listeners = new Set<(event: ServerEvent) => void>();
let disconnect: (() => void) | undefined;

// One bridge subscription preserves event identity and ordering across mounted
// conversations, including a session inspected outside its owning workspace.
export function subscribeServerEvents(listener: (event: ServerEvent) => void): () => void {
  listeners.add(listener);
  disconnect ??= window.wuu.onServerEvent?.((event) => {
    for (const receive of [...listeners]) receive(event);
  });
  return () => {
    listeners.delete(listener);
    if (listeners.size === 0) {
      disconnect?.();
      disconnect = undefined;
    }
  };
}
