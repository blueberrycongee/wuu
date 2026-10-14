import type { WorkspaceFileTarget } from "./protocol";
type Chunk = { data: string; offset: number; size: number; version: string; mime: string; target?: WorkspaceFileTarget };
type Request = <T>(method: string, params?: unknown) => Promise<T>;

export async function downloadWorkspaceFile(request: Request, path: string, root?: string, target?: WorkspaceFileTarget, initialVersion = ""): Promise<Blob> {
  const parts: ArrayBuffer[] = [];
  let offset = 0, size: number | undefined, version = initialVersion, mime = "";
  let boundTarget = target;
  do {
    const chunk = await request<Chunk>('workspace/file/chunk', { path, root, offset, version, ...(boundTarget ? { target: boundTarget, thread_id: boundTarget.thread_id } : {}) });
    if (!Number.isSafeInteger(chunk.size) || chunk.size < 0 || chunk.size > 64 * 1024 * 1024 ||
        chunk.offset !== offset || !chunk.version || (size !== undefined && (chunk.size !== size || chunk.version !== version))) {
      throw new Error('文件传输状态改变，请重新下载');
    }
    if (boundTarget && (chunk.target?.thread_id !== boundTarget.thread_id ||
        (boundTarget.environment_id && chunk.target.environment_id !== boundTarget.environment_id) ||
        (boundTarget.connection_id && chunk.target.connection_id !== boundTarget.connection_id))) {
      throw new Error("Execution workspace changed; restart the download");
    }
    boundTarget = chunk.target ?? boundTarget;
    if (version && chunk.version !== version) throw new Error("File changed; restart the download");
    const raw = atob(chunk.data);
    if (raw.length > 256 * 1024 || offset + raw.length > chunk.size || (!raw.length && offset < chunk.size)) throw new Error('文件数据不完整');
    const data = new Uint8Array(raw.length);
    for (let index = 0; index < raw.length; index++) data[index] = raw.charCodeAt(index);
    parts.push(data.buffer);
    offset += data.length; size = chunk.size; version = chunk.version; mime = chunk.mime;
  } while (offset < size);
  return new Blob(parts, { type: mime || 'application/octet-stream' });
}
