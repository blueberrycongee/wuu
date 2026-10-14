export type SystemPermission = "screen" | "accessibility";

/** Current macOS grants, checked without requesting additional access. */
export type SystemPermissionsState = {
  available: boolean;
  screen: boolean;
  accessibility: boolean;
};
