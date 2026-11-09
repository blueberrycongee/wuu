export interface DesignSession {
  token: string;
  base: string;
  root: string;
}

export interface DesignPage {
  path: string;
  bytes: number;
  sha256: string;
  mtime_ms: number;
}

export interface DesignSaveResult {
  status: "saved" | "conflict";
  sha256?: string;
  mtime_ms?: number;
  bytes?: number;
  disk_sha256?: string;
  error?: string;
}

export interface DesignServer {
  listen(port?: number): Promise<number>;
  open(root: string): Promise<DesignSession>;
  listPages(root: string): Promise<DesignPage[]>;
  save(root: string, input: { path: string; html: string; base_sha256?: string }): Promise<DesignSaveResult>;
  close(): Promise<void>;
}

export function createDesignServer(options: { bridgeSource: string }): DesignServer;
