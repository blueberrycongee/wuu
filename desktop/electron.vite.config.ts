import { defineConfig } from "electron-vite";
import react from "@vitejs/plugin-react";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

// Pull the desktop's own version into every build at compile time so the
// renderer, preload, and main process can render an About row without a
// runtime fs read or a separate IPC call. The build date is fixed at
// config-eval time; the commit SHA is intentionally omitted because the
// desktop is shipped as a bundle and the build identity that matters is
// the version + date pair, not the source commit.
const desktopPackage = JSON.parse(
  readFileSync(resolve(__dirname, "package.json"), "utf8"),
) as { version: string };
const desktopDefine = {
  __WUU_ACCOUNT_SERVER__: JSON.stringify(process.env.WUU_ACCOUNT_SERVER || ''),
  __DESKTOP_VERSION__: JSON.stringify(desktopPackage.version),
  __DESKTOP_BUILD_DATE__: JSON.stringify(new Date().toISOString()),
};

export default defineConfig({
  main: {
    define: desktopDefine,
    build: {
      rollupOptions: {
        external: ["node-pty"],
        input: {
          index: resolve(__dirname, "src/main/index.ts"),
        },
      },
    },
  },
  preload: {
    define: desktopDefine,
    build: {
      rollupOptions: {
        input: {
          index: resolve(__dirname, "src/main/preload.ts"),
        },
        output: {
          format: "cjs",
        },
      },
    },
  },
  renderer: {
    root: ".",
    define: desktopDefine,
    plugins: [react()],
    resolve: {
      dedupe: ["react", "react-dom"],
    },
    server: {
      fs: {
        // Protocol types are imported from packages/protocol at the repo
        // root, outside the renderer root — allow the dev server to serve it.
        allow: [resolve(__dirname, "..")],
      },
    },
    build: {
      rollupOptions: {
        input: {
          index: resolve(__dirname, "index.html"),
        },
      },
    },
  },
});
