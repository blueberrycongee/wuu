import type { CapacitorConfig } from "@capacitor/cli";
const config: CapacitorConfig = {
  appId: "com.blueberrycongee.wuu",
  appName: "Wuu",
  webDir: "../mobile-web/dist",
  loggingBehavior: "none",
  server: { androidScheme: "https" },
  ios: { contentInset: "never" },
  plugins: { Keyboard: { resize: "native" } },
};
export default config;
