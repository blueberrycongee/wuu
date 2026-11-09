export type SystemPermission = "screen" | "accessibility";

/** Source row in renderer viewport coordinates, before Electron zoom. */
export type SystemPermissionPresentation = {
  requestId: string;
  sourceRect: { x: number; y: number; width: number; height: number } | null;
  reducedMotion: boolean;
  /** Actual source appearance; onboarding can differ from the saved preference. */
  theme: "light" | "dark";
};

export type SystemPermissionGuideState = {
  requestId: string;
  phase: "lifting" | "guiding" | "returning" | "closed";
  granted: boolean;
};

/** Current macOS grants, checked without requesting additional access. */
export type SystemPermissionsState = {
  available: boolean;
  screen: boolean;
  accessibility: boolean;
};
