import { EventEmitter } from "node:events";
import { describe, expect, it, vi } from "vitest";
import type { BrowserWindow } from "electron";
import { WebWorkspaceHost } from "./webWorkspaceHost";
import type { WebWorkspaceLibrary } from "./webWorkspaceLibrary";
const menus=vi.hoisted(()=>[] as Array<{popup:ReturnType<typeof vi.fn>;closePopup:ReturnType<typeof vi.fn>}>);
vi.mock("electron",()=>({BrowserWindow:class {},Menu:{buildFromTemplate:()=>{
  let done=()=>{}; const menu={popup:vi.fn((options:{callback:()=>void})=>{done=options.callback;}),closePopup:vi.fn(()=>done())}; menus.push(menu); return menu;
}},clipboard:{},dialog:{},shell:{},webContents:{}}));
vi.mock("./browserHostWindows",()=>({BROWSER_PARTITION:"test",BROWSER_WORKSPACE_TAB_PREFIX:"web:"}));
vi.mock("./browserView",()=>({createBrowserView:vi.fn()}));
vi.mock("./browserDisplayCapture",()=>({chooseBrowserDisplaySource:vi.fn()}));
it("dismisses an outstanding native menu on owner release without deleting restored tabs",async()=>{
  const owner=Object.assign(new EventEmitter(),{webContents:Object.assign(new EventEmitter(),{id:1,getZoomFactor:()=>1}),isDestroyed:()=>false,isVisible:()=>true,isMinimized:()=>false});
  const library={getSession:()=>({tabs:[{id:"tab",url:"https://example.com",title:"Example"}],activeTabId:"tab"}),saveSession:vi.fn(),flush:vi.fn(),removeSession:vi.fn()};
  const host=new WebWorkspaceHost(()=>{},()=>{},{library:library as unknown as WebWorkspaceLibrary});
  const state=host.initializeTabs(owner as unknown as BrowserWindow,[]);
  const pending=host.showOptions(owner as unknown as BrowserWindow,state.id,{x:1,y:1,labels:{find:"Find",zoomOut:"Out",resetZoom:"Reset",zoomIn:"In",reopenTab:"Reopen",openExternal:"Open"}});
  host.dismissOptions(owner as unknown as BrowserWindow,state.id);
  expect(await pending).toBeNull(); expect(menus.at(-1)?.closePopup).toHaveBeenCalledTimes(1); expect(library.removeSession).not.toHaveBeenCalled();
  host.dismissOptions(owner as unknown as BrowserWindow,state.id); expect(menus.at(-1)?.closePopup).toHaveBeenCalledTimes(1);
  host.destroyAll();
});
