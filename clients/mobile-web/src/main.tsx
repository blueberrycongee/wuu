import { createRoot } from "react-dom/client";

import App from "./AccountApp";
import { startNativeLifecycle } from "./lib/native";
import "./styles.css";

const root = document.getElementById("root");
if (!root) throw new Error("missing #root");
root.dataset.wuuUiRoot = "true";
root.dataset.wuuComponent = "ui-root";

void startNativeLifecycle().then(() => createRoot(root).render(<App />)).catch(error => { root.textContent = `手机初始化失败：${String(error)}`; });
