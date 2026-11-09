// App shell: three screens (pair → chats → thread) with hand-rolled
// navigation — the surface is small enough that a navigation library would
// outweigh the app. The controller is a module singleton; screens read the
// store via useSyncExternalStore.

import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react";
import { ActivityIndicator, Alert, BackHandler, Linking, StyleSheet, View } from "react-native";
import { StatusBar } from "expo-status-bar";

import { WuuMobile } from "./src/lib/connection";
import { deviceCredentialStore } from "./src/lib/credStore";
import { createDeepLinkGate, parseDeepLink } from "./src/lib/deepLink";
import { getInitialNotificationResponse } from "./src/lib/push";
import { threadDisplayTitle } from "./src/lib/threads";
import { usePalette } from "./src/theme";
import { PairScreen } from "./src/screens/PairScreen";
import { ChatsScreen } from "./src/screens/ChatsScreen";
import { ThreadScreen } from "./src/screens/ThreadScreen";

const controller = new WuuMobile(deviceCredentialStore);

type Route =
  | { name: "boot" }
  | { name: "pair" }
  | { name: "chats" }
  | { name: "thread"; threadId: string };

/** Drives navigation from a parsed deep-link. The route is the only piece of
 *  state in the App shell, so the only thing the controller needs to tell
 *  us is "go to this thread"; opening the chat history is then handled by
 *  the existing controller.openThread path so the open chat refills. */
function applyDeepLink(
  link: ReturnType<typeof parseDeepLink>,
  setRoute: (route: Route) => void,
): void {
  if (!link) return;
  if (link.kind === "thread") {
    setRoute({ name: "thread", threadId: link.threadId });
    void controller.openThread(link.threadId).catch(() => {});
  } else if (link.kind === "home") {
    controller.closeThread();
    setRoute({ name: "chats" });
  }
}

export default function App(): React.JSX.Element {
  const palette = usePalette();
  const [route, setRoute] = useState<Route>({ name: "boot" });
  const [refreshing, setRefreshing] = useState(false);
  const snapshot = useSyncExternalStore(controller.store.subscribe, controller.store.getSnapshot);
  const deepLinkGateRef = useRef<ReturnType<typeof createDeepLinkGate> | null>(null);
  if (!deepLinkGateRef.current) {
    deepLinkGateRef.current = createDeepLinkGate((link) => applyDeepLink(link, setRoute));
  }
  const deepLinkGate = deepLinkGateRef.current;

  useEffect(() => {
    void controller
      .startFromStoredCredentials()
      .then((hadCredentials) => {
        setRoute(hadCredentials ? { name: "chats" } : { name: "pair" });
        deepLinkGate.completeStartup(hadCredentials);
      })
      .catch(() => {
        setRoute({ name: "pair" });
        deepLinkGate.completeStartup(false);
      });
  }, []);

  // Deep-link wiring: cold start (initial URL or initial notification
  // response), and warm activations (Linking events + notification taps
  // forwarded by the controller). We only honor deep-links after we have
  // credentials, so a freshly-installed user that taps a push gets routed
  // to pair instead of an empty thread.
  useEffect(() => {
    controller.onDeepLink = (link) => deepLinkGate.receive(link);
    controller.startPushListeners();
    let cancelled = false;
    (async () => {
      const initialUrl = await Linking.getInitialURL().catch(() => null);
      if (cancelled) return;
      deepLinkGate.receive(parseDeepLink(initialUrl));
      const initialPush = await getInitialNotificationResponse();
      if (cancelled || !initialPush) return;
      const data = initialPush.notification.request.content.data as { url?: unknown } | undefined;
      if (typeof data?.url === "string") {
        deepLinkGate.receive(parseDeepLink(data.url));
      }
    })();
    const sub = Linking.addEventListener("url", (event) => {
      deepLinkGate.receive(parseDeepLink(event.url));
    });
    return () => {
      cancelled = true;
      controller.onDeepLink = null;
      sub.remove();
    };
  }, []);

  // Android hardware back mirrors the header back button.
  useEffect(() => {
    const sub = BackHandler.addEventListener("hardwareBackPress", () => {
      if (route.name === "thread") {
        controller.closeThread();
        setRoute({ name: "chats" });
        return true;
      }
      return false;
    });
    return () => sub.remove();
  }, [route.name]);

  const openThread = useCallback((threadId: string) => {
    setRoute({ name: "thread", threadId });
    void controller.openThread(threadId).catch(() => {});
  }, []);

  const refresh = useCallback(() => {
    setRefreshing(true);
    void controller
      .refreshThreads()
      .catch(() => {})
      .finally(() => setRefreshing(false));
  }, []);

  return (
    <View style={[styles.root, { backgroundColor: palette.paper }]}>
      <StatusBar style="auto" />
      {route.name === "boot" ? (
        <View style={styles.boot}>
          <ActivityIndicator color={palette.accent} />
        </View>
      ) : route.name === "pair" ? (
        <PairScreen
          onPair={async (uri) => {
            const creds = await controller.pairWithUri(uri, "手机");
            return creds.host_name ?? "";
          }}
          onDone={() => {
            deepLinkGate.markPaired();
            setRoute({ name: "chats" });
          }}
        />
      ) : route.name === "chats" ? (
        <ChatsScreen
          snapshot={snapshot}
          refreshing={refreshing}
          onRefresh={refresh}
          onOpenThread={(thread) => openThread(thread.id)}
          onLongPressThread={(thread) => {
            Alert.alert(threadDisplayTitle(thread), undefined, [
              {
                text: thread.pinned ? "取消置顶" : "置顶",
                onPress: () => void controller.togglePin(thread).catch(() => {}),
              },
              { text: "取消", style: "cancel" },
            ]);
          }}
        />
      ) : (
        <ThreadScreen
          snapshot={snapshot}
          threadId={route.threadId}
          onBack={() => {
            controller.closeThread();
            setRoute({ name: "chats" });
          }}
          onSend={(thread, text) => controller.sendMessage(thread, text)}
          onInterrupt={(threadId) => void controller.interrupt(threadId).catch(() => {})}
        />
      )}
    </View>
  );
}

const styles = StyleSheet.create({
  root: { flex: 1 },
  boot: { flex: 1, alignItems: "center", justifyContent: "center" },
});
