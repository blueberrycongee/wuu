import type { Thread } from "../shared/protocol";

export function userVisibleThreads(threads: Thread[]): Thread[] {
  return threads.filter((thread) => !thread.ephemeral);
}
