const THREAD_READ_STATE_KEY = "wuu.threads.lastViewedTurn.v1";

export function readThreadReadState(): Record<string, string> {
  try {
    const raw = window.localStorage.getItem(THREAD_READ_STATE_KEY);
    const parsed: unknown = raw ? JSON.parse(raw) : null;
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return {};
    return Object.fromEntries(
      Object.entries(parsed).filter(
        ([threadID, turnID]) => threadID.length > 0 && typeof turnID === "string" && turnID.length > 0,
      ),
    );
  } catch {
    return {};
  }
}

export function writeThreadReadState(lastViewedTurnByThreadID: Record<string, string>): void {
  try {
    window.localStorage.setItem(THREAD_READ_STATE_KEY, JSON.stringify(lastViewedTurnByThreadID));
  } catch (reason) {
    // Keep reading usable when storage is unavailable, but report the lost durability.
    console.warn("thread read state persistence failed", reason);
  }
}
