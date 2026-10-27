import { ipcRenderer } from "electron";

// A page with user edits must not be reclaimed, even when the value later
// leaves the DOM (SPA editors and submitted forms can retain pending state).
let reported = false;
for (const type of ["input", "change", "submit"]) {
  document.addEventListener(type, event => {
    if (reported || !event.isTrusted) return;
    reported = true;
    ipcRenderer.send("wuu:web-workspace-page-edited");
  }, true);
}
