import { resolve } from 'node:path';
import type { RuntimeContext, WorkspaceFileSaveParams } from '../shared/protocol';
import { WorkspaceFileService } from './workspaceFiles';

/** Reuse desktop file conflict and symlink checks after selecting a known context. */
export function requestRemoteFiles(context: RuntimeContext, method: string, input: unknown): unknown {
 const params = (input ?? {}) as {root?:string; params?:WorkspaceFileSaveParams};
 if (params.root && resolve(params.root) !== resolve(context.cwd)) throw new Error('Unknown remote workspace');
 const files = new WorkspaceFileService(() => context);
 if (method === 'desktop/file/list') return files.fileTreeList();
 if (method === 'desktop/file/write') return files.writeFile(params.params!);
 throw new Error('Unknown remote file operation');
}
