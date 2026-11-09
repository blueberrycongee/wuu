import type { ToolDiffPreviewFileDiff } from "./ToolDiffPreview";

export type TurnFileDiffSelection = {
  artifactID?: string;
  path: string;
  cwd?: string;
  action?: "create" | "update" | "delete" | "rename";
  diff?: ToolDiffPreviewFileDiff;
  snapshotText?: string;
  afterSha?: string;
  additions: number;
  deletions: number;
  newFile: boolean;
};
