import type { AppLocale, SystemPermission } from "./protocol";

export type SystemPermissionGuideConfig = {
  permission: SystemPermission;
  appName: string;
  appIcon: string;
  locale: AppLocale;
  theme: "light" | "dark";
  fontSize: number;
  reducedMotion: boolean;
  phase: "source" | "lifting" | "guiding" | "returning";
  granted: boolean;
};

export type SystemPermissionGuideAPI = {
  config: () => Promise<SystemPermissionGuideConfig>;
  onConfig: (handler: (config: SystemPermissionGuideConfig) => void) => () => void;
  ready: () => void;
  drag: () => void;
  reveal: () => void;
  resize: (height: number) => void;
  close: () => void;
  onError: (handler: () => void) => () => void;
};
