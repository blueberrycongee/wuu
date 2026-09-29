const { contextBridge } = require("electron");
const fs = require("node:fs");
const threads = JSON.parse(fs.readFileSync(process.env.WUU_IMAGE_LAYOUT_FIXTURE, "utf8"));
const expose = contextBridge.exposeInMainWorld.bind(contextBridge);
contextBridge.exposeInMainWorld = (key, api) => expose(key, key !== "wuu" ? api : {
  ...api,
  listThreads: async () => ({ threads }),
  listArchivedThreads: async () => ({ threads: [] }),
  resumeThread: async id => ({ thread: threads.find(thread => thread.id === id) }),
});
require("./streaming-e2e-preload.cjs");
