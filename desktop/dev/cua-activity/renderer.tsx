import { createRoot } from "react-dom/client";
import type { ThreadItem } from "../../src/shared/protocol";
import { ProcessSurface } from "../../src/renderer/ProcessSurface";
import { ToolActivityRow } from "../../src/renderer/ToolActivity";
import { I18nProvider } from "../../src/renderer/i18n";
import "../../src/renderer/styles.css";

const query = new URLSearchParams(location.search);
document.documentElement.dataset.theme = query.get("theme") || "light";
document.documentElement.dataset.platform = "darwin";
document.documentElement.style.setProperty("--conversation-message-font-size", `${query.get("size") || 15}px`);

function call(id: string, title: string, action: string, status: ThreadItem["status"] = "completed"): ThreadItem {
  return {
    id, type: "tool_call", name: "mcp_plugin_cua_mac_computer_computer", status,
    arguments: JSON.stringify({ display_title: title, action, app: "Wuu Dev" }),
    result: status === "completed" ? 'Window: "wuu", App: Wuu Dev.\n0 standard window wuu\n  1 container\n    2 text field 搜索' : undefined,
  };
}
const observe = call("observe", "观察当前 Wuu Dev 的原生焦点与地址栏", "observe");
const failed = { ...call("failed", "点击 Wuu Dev 的搜索框", "activate_control", "failed"), error: "未找到匹配的控件，请重新观察窗口。" };
const long = call("long", "观察当前窗口中的搜索结果、侧栏与编辑区域，检查长标题在窄窗口和大字号下是否保持清晰并且展开按钮可用", "observe", "in_progress");
createRoot(document.getElementById("root")!).render(
  <I18nProvider>
    <main className="conversation-pane" style={{ padding: 32, height: "100vh", overflow: "auto" }}>
      <div style={{ maxWidth: 820, margin: "auto", display: "grid", gridTemplateColumns: "minmax(0, 1fr)", gap: 24 }}>
        <p>下面查看 Wuu Dev 当前窗口，确认焦点和地址栏的位置。</p>
        <div data-preview="single"><ProcessSurface processItems={[observe]} streaming={false} /></div>
        <div data-preview="timeline"><ProcessSurface processItems={[observe, call("click", "点击 Wuu Dev 的搜索框", "activate_control")]} streaming={false} /></div>
        <div data-preview="failure"><ToolActivityRow items={[failed]} /></div>
        <div data-preview="long"><ToolActivityRow items={[long]} /></div>
      </div>
    </main>
  </I18nProvider>,
);
