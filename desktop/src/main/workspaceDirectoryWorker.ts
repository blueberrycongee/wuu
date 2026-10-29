import { parentPort } from "node:worker_threads";
import { WorkspaceFileService } from "./workspaceFiles";
import type { DirectoryReadRequest, DirectoryReadResponse } from "./workspaceDirectoryReader";

if (!parentPort) throw new Error("directory worker requires a parent port");
const port = parentPort;
port.on("message", ({ id, root, path }: DirectoryReadRequest) => {
  let response: DirectoryReadResponse;
  try {
    const service = new WorkspaceFileService(() => ({ kind: "no_project", cwd: root }));
    response = { id, result: service.directoryList(path) };
  } catch (error) {
    const failure = error instanceof Error ? error : new Error(String(error));
    response = { id, error: { ...failure, name: failure.name, message: failure.message, stack: failure.stack } };
  }
  port.postMessage(response);
});
