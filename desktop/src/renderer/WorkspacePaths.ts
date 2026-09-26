export function workspacePathToSlash(path: string, root: string): string {
  // Use the workspace's path syntax; a remote host can differ from this client.
  return /^(?:[a-z]:[\\/]|[\\/]{2})/i.test(root) ? path.replace(/\\/g, "/") : path;
}
