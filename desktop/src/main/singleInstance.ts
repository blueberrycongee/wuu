import { app } from "electron";

// The desktop instance owns its user data directory and every app-server it
// spawns for the registered workspaces, all sharing the same ~/.wuu state.
// A second normal instance — for example a bare launch of the packaged
// executable — would start duplicate app-servers for identical workspace ids
// and contend for that shared state while the first instance is using it.
// Keep exactly one normal instance: latecomers exit before creating windows
// or app-servers, and their launch is forwarded here so the existing window
// surfaces instead.
export function installSingleInstanceGuard(reveal: () => void): boolean {
  if (!app.requestSingleInstanceLock()) {
    console.warn("[desktop] another instance is already running; exiting");
    app.quit();
    return false;
  }
  app.on("second-instance", () => reveal());
  return true;
}
