import { beforeEach, describe, expect, it, vi } from "vitest";

let pushTokenListener: ((token: { data: string; type: string }) => void) | undefined;

vi.mock("expo-notifications", () => ({
  setNotificationHandler: vi.fn(),
  setNotificationChannelAsync: vi.fn().mockResolvedValue(undefined),
  getPermissionsAsync: vi.fn(),
  requestPermissionsAsync: vi.fn(),
  getExpoPushTokenAsync: vi.fn(),
  getDevicePushTokenAsync: vi.fn(),
  addPushTokenListener: vi.fn((listener) => {
    pushTokenListener = listener;
    return { remove: vi.fn() };
  }),
  addNotificationResponseReceivedListener: vi.fn(() => ({ remove: vi.fn() })),
  getLastNotificationResponseAsync: vi.fn().mockResolvedValue(null),
  AndroidImportance: { HIGH: 4 },
  PermissionStatus: { GRANTED: "granted", DENIED: "denied", UNDETERMINED: "undetermined" },
  IosAuthorizationStatus: { NOT_DETERMINED: 0, DENIED: 1, AUTHORIZED: 2, PROVISIONAL: 3, EPHEMERAL: 4 },
}));

vi.mock("expo-constants", () => ({
  default: { expoConfig: { extra: { eas: { projectId: "test-project" } } } },
}));

vi.mock("react-native", () => ({
  Platform: { OS: "ios" },
}));

import * as Notifications from "expo-notifications";
import {
  addPushTokenRefreshListener,
  fetchPushTokens,
  requestNotificationPermission,
} from "../src/lib/push";

describe("mobile push registration", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    pushTokenListener = undefined;
  });

  it("accepts iOS provisional permission without prompting again", async () => {
    vi.mocked(Notifications.getPermissionsAsync).mockResolvedValueOnce({
      status: "denied",
      granted: false,
      canAskAgain: true,
      expires: "never",
      ios: { status: Notifications.IosAuthorizationStatus.PROVISIONAL },
    } as never);

    await expect(requestNotificationPermission()).resolves.toBe(true);
    expect(Notifications.requestPermissionsAsync).not.toHaveBeenCalled();
  });

  it("uses the granted field from an iOS permission request", async () => {
    vi.mocked(Notifications.getPermissionsAsync).mockResolvedValueOnce({
      status: "undetermined",
      granted: false,
      canAskAgain: true,
      expires: "never",
    } as never);
    vi.mocked(Notifications.requestPermissionsAsync).mockResolvedValueOnce({
      status: "granted",
      granted: true,
      canAskAgain: true,
      expires: "never",
      ios: { status: Notifications.IosAuthorizationStatus.AUTHORIZED },
    } as never);

    await expect(requestNotificationPermission()).resolves.toBe(true);
  });

  it("returns one Expo-preferred token matching the RPC contract", async () => {
    vi.mocked(Notifications.getExpoPushTokenAsync).mockResolvedValueOnce({
      data: "ExponentPushToken[initial]",
      type: "expo",
    });

    await expect(fetchPushTokens()).resolves.toEqual({
      token: "ExponentPushToken[initial]",
      platform: "ios",
    });
    expect(Notifications.getDevicePushTokenAsync).not.toHaveBeenCalled();
  });

  it("re-mints an Expo token from the native rollover token", async () => {
    vi.mocked(Notifications.getExpoPushTokenAsync).mockResolvedValueOnce({
      data: "ExponentPushToken[refreshed]",
      type: "expo",
    });
    const listener = vi.fn();
    addPushTokenRefreshListener(listener);
    const nativeToken = { data: "native-new", type: "ios" };

    pushTokenListener?.(nativeToken);
    await vi.waitFor(() => expect(listener).toHaveBeenCalledOnce());

    expect(Notifications.getExpoPushTokenAsync).toHaveBeenCalledWith({
      projectId: "test-project",
      devicePushToken: nativeToken,
    });
    expect(listener).toHaveBeenCalledWith({
      token: "ExponentPushToken[refreshed]",
      platform: "ios",
    });
  });
});
