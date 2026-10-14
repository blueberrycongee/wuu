import type { AppLocale, SystemPermission } from "./protocol";

export type SystemPermissionGuideConfig = {
  permission: SystemPermission;
  appName: string;
  appIcon: string;
  locale: AppLocale;
  theme: "light" | "dark";
  fontSize: number;
};

export type SystemPermissionGuideAPI = {
  config: () => Promise<SystemPermissionGuideConfig>;
  drag: () => void;
  reveal: () => void;
  resize: (height: number) => void;
  close: () => void;
  onError: (handler: () => void) => () => void;
};
