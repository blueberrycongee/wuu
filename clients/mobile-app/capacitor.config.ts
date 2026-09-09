import type { CapacitorConfig } from "@capacitor/cli";
import { existsSync } from "node:fs";
if (process.env.VITE_WUU_PUSH_PLATFORMS?.split(',').includes('android') && !existsSync('android/app/google-services.json')) {
  throw new Error('Android push requires your own android/app/google-services.json before building');
}
const config: CapacitorConfig = {
  appId: "com.blueberrycongee.wuu",
  appName: "Wuu",
  webDir: "../mobile-web/dist",
  loggingBehavior: "none",
  server: { androidScheme: "https" },
  ios: { contentInset: "never" },
  plugins: { Keyboard: { resize: "native" }, PushNotifications: { presentationOptions: [] } },
};
export default config;
