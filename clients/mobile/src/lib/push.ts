// Push registration for the mobile app. The host's M2-1 push dispatch
// component needs a stable device token to ping the relay with; we obtain it
// via expo-notifications (Expo push for unified iOS/Android delivery) and
// also expose the native FCM/APNs token for deployments that prefer the raw
// provider. The register call is fire-and-forget — failure to obtain a token
// must never block the rest of the attach flow.
//
// Foreground notification handling (no banner, no sound, but do register the
// listener) is set at module load so a notification that arrives between JS
// bridge warm-up and the first attach is not dropped silently.

import { Platform } from "react-native";
import Constants from "expo-constants";
import * as Notifications from "expo-notifications";

// Quiet foreground banners: the phone's own banner is the OS notification
// surface, and the app already has the chats list as a heartbeat indicator.
Notifications.setNotificationHandler({
  handleNotification: async () => ({
    shouldPlaySound: false,
    shouldSetBadge: false,
    shouldShowBanner: true,
    shouldShowList: true,
  }),
});

export type PushPlatform = "ios" | "android";

export interface PushTokenBundle {
  token: string;
  platform: PushPlatform;
}

type PushTokenListener = (bundle: PushTokenBundle) => void;
type ResponseListener = (response: Notifications.NotificationResponse) => void;

const DEFAULT_CHANNEL = "default";

/** True on web (and other non-notification platforms): no token, no-op. */
function isUnsupported(): boolean {
  return Platform.OS !== "ios" && Platform.OS !== "android";
}

/** projectId is the EAS project id; on Android it is required to mint an
 *  Expo push token. Falls back to the runtime config so the dev build still
 *  works locally without a deployed EAS project. */
function resolveProjectId(): string | null {
  const cfg = (Constants as unknown as { expoConfig?: { extra?: { eas?: { projectId?: string } } } })
    .expoConfig;
  return cfg?.extra?.eas?.projectId ?? null;
}

/** Best-effort: create the Android channel so the permissions prompt is
 *  wired up before we ask for the token. Idempotent. */
async function ensureAndroidChannel(): Promise<void> {
  if (Platform.OS !== "android") return;
  try {
    await Notifications.setNotificationChannelAsync(DEFAULT_CHANNEL, {
      name: "wuu 新消息",
      importance: Notifications.AndroidImportance.HIGH,
      vibrationPattern: [0, 250, 250, 250],
      lightColor: "#FFFFFF",
    });
  } catch {
    // setNotificationChannelAsync throws on pre-26 devices; ignore — they
    // don't need a channel anyway and the token call below will still work.
  }
}

/** True when notifications can be delivered, including iOS provisional
 *  authorization. The general status is a string while ios.status is a
 *  numeric IosAuthorizationStatus; they must not be cast between types. */
function permissionGranted(status: Notifications.NotificationPermissionsStatus): boolean {
  return (
    status.granted ||
    (Platform.OS === "ios" &&
      status.ios?.status === Notifications.IosAuthorizationStatus.PROVISIONAL)
  );
}

/** Asks the OS for push permission if not already granted. Returns whether
 *  the user has permission at the end of the call (granted OR provisional). */
export async function requestNotificationPermission(): Promise<boolean> {
  if (isUnsupported()) return false;
  await ensureAndroidChannel();
  const existing = await Notifications.getPermissionsAsync();
  if (permissionGranted(existing)) return true;
  const next = await Notifications.requestPermissionsAsync();
  return permissionGranted(next);
}

/** Fetches one token matching the app-server registration contract. Expo is
 *  preferred for unified delivery; the native FCM/APNs token is the fallback
 *  when an Expo project id is unavailable or token minting fails. */
export async function fetchPushTokens(): Promise<PushTokenBundle | null> {
  if (isUnsupported()) return null;
  await ensureAndroidChannel();
  const projectId = resolveProjectId();
  let token: string | undefined;
  if (projectId) {
    try {
      const result = await Notifications.getExpoPushTokenAsync({ projectId });
      token = result.data;
    } catch {
      // Offline at the moment of register, or Expo service rejected: fall
      // back to the raw device token below.
    }
  }
  if (!token) {
    try {
      const native = await Notifications.getDevicePushTokenAsync();
      if (typeof native.data === "string") token = native.data;
    } catch {
      // Some dev clients do not support device tokens.
    }
  }
  if (!token) return null;
  return {
    token,
    platform: Platform.OS as PushPlatform,
  };
}

/** Subscribes to native push-token rollovers. Re-mint the corresponding Expo
 *  token without calling getDevicePushTokenAsync from inside the listener;
 *  Expo documents that doing so can recursively trigger this listener. */
export function addPushTokenRefreshListener(listener: PushTokenListener): () => void {
  if (isUnsupported()) return () => {};
  const sub = Notifications.addPushTokenListener((nativeToken) => {
    void refreshPushToken(nativeToken).then(listener).catch((error: unknown) => {
      console.warn("Failed to refresh push token", error);
    });
  });
  return () => sub.remove();
}

async function refreshPushToken(
  nativeToken: Notifications.DevicePushToken,
): Promise<PushTokenBundle> {
  let token =
    typeof nativeToken.data === "string"
      ? nativeToken.data
      : JSON.stringify(nativeToken.data);
  const projectId = resolveProjectId();
  if (projectId) {
    try {
      const expo = await Notifications.getExpoPushTokenAsync({
        projectId,
        devicePushToken: nativeToken,
      });
      token = expo.data;
    } catch {
      // Keep the native token so direct APNs/FCM hosts can still register it.
    }
  }
  return { token, platform: Platform.OS as PushPlatform };
}

/** Subscribes to notification responses (taps). The payload data.url
 *  field is the deep-link the host stuffed into the push. */
export function addNotificationResponseListener(listener: ResponseListener): () => void {
  if (isUnsupported()) return () => {};
  const sub = Notifications.addNotificationResponseReceivedListener(listener);
  return () => sub.remove();
}

/** Returns the response that launched the app (cold start from a tap).
 *  Resolves to null when the app was launched for any other reason. */
export async function getInitialNotificationResponse(): Promise<Notifications.NotificationResponse | null> {
  if (isUnsupported()) return null;
  try {
    return await Notifications.getLastNotificationResponseAsync();
  } catch {
    return null;
  }
}
