import { defineConfig, type Plugin } from "vite";
import { resolve } from "node:path";
import { recoveryPage } from "../../src/main/recoveryPage";

// Serves the real self-contained recovery document; only the CSP meta is
// relaxed so the fixture can attach a module script that feeds synthetic
// snapshots through the shared view layer.
function recoveryPagePlugin(): Plugin {
  return {
    name: "wuu-recovery-page",
    configureServer(server) {
      server.middlewares.use((req, res, next) => {
        if (!req.url || !/^\/(\?.*)?$/.test(req.url)) return next();
        const search = new URL(req.url, "http://fixture").searchParams;
        const theme = search.get("theme");
        const overrides = [
          theme === "dark" || theme === "light" ? `:root{color-scheme:${theme} !important}` : "",
          search.has("noanim") ? ".shell{animation:none}" : "",
        ].filter(Boolean).join("");
        const scheme = overrides ? `<style>${overrides}</style>` : "";
        const html = recoveryPage
          .replace(/<meta http-equiv="Content-Security-Policy"[^>]*>/, "")
          .replace("</body></html>", `${scheme}<script type="module" src="/main.ts"></script></body></html>`);
        res.setHeader("content-type", "text/html; charset=utf-8");
        res.end(html);
      });
    },
  };
}

export default defineConfig({
  cacheDir: resolve(__dirname, "../../node_modules/.vite/recovery"),
  plugins: [recoveryPagePlugin()],
});
