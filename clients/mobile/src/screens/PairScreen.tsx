// Pairing: the product's front door. Round-table hero (the one big
// decoration), scan the QR on the computer screen, manual URI paste as the
// always-available fallback, graduate moment on success.

import { useRef, useState } from "react";
import {
  ActivityIndicator,
  Image,
  KeyboardAvoidingView,
  Modal,
  Platform,
  Pressable,
  ScrollView,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import { CameraView, useCameraPermissions } from "expo-camera";

import { serifFamily, usePalette } from "../theme";

import HERO from "../../assets/art/roundtable-hero.jpg";
import SUCCESS from "../../assets/art/pair-success.png";

type Phase =
  | { name: "idle" }
  | { name: "scanning" }
  | { name: "pairing" }
  | { name: "success"; hostName: string }
  | { name: "error"; message: string };

export function PairScreen({
  onPair,
  onDone,
}: {
  /** Runs the pairing exchange; resolves with the host's display name. */
  onPair: (uri: string) => Promise<string>;
  onDone: () => void;
}): React.JSX.Element {
  const palette = usePalette();
  const [phase, setPhase] = useState<Phase>({ name: "idle" });
  const [manualUri, setManualUri] = useState("");
  const [permission, requestPermission] = useCameraPermissions();
  // The camera reports the same QR on consecutive frames; only the first
  // fire may start a pairing exchange (a second concurrent pair() would
  // leak a connection and race the success screen).
  const scanConsumed = useRef(false);

  const startPairing = (uri: string) => {
    const trimmed = uri.trim();
    if (trimmed === "") return;
    setPhase({ name: "pairing" });
    onPair(trimmed)
      .then((hostName) => setPhase({ name: "success", hostName }))
      .catch((err: unknown) =>
        setPhase({ name: "error", message: err instanceof Error ? err.message : String(err) }),
      );
  };

  const openScanner = async () => {
    if (!permission?.granted) {
      const result = await requestPermission();
      if (!result.granted) return; // paste fallback stays available
    }
    scanConsumed.current = false;
    setPhase({ name: "scanning" });
  };

  if (phase.name === "success") {
    return (
      <View style={[styles.page, styles.center, { backgroundColor: palette.paper }]}>
        <Image source={SUCCESS} style={styles.successArt} resizeMode="contain" />
        <Text style={[styles.serifTitle, { color: palette.ink }]}>配对完成</Text>
        <Text style={[styles.lede, { color: palette.inkMuted }]}>
          已连接 {phase.hostName || "你的电脑"}
        </Text>
        <Pressable
          style={[styles.primaryButton, { backgroundColor: palette.accent }]}
          onPress={onDone}
        >
          <Text style={styles.primaryButtonText}>开始聊天</Text>
        </Pressable>
      </View>
    );
  }

  return (
    <KeyboardAvoidingView
      style={[styles.page, { backgroundColor: palette.paper }]}
      behavior={Platform.OS === "ios" ? "padding" : undefined}
    >
      <ScrollView contentContainerStyle={styles.scroll} keyboardShouldPersistTaps="handled">
        <Image source={HERO} style={styles.hero} resizeMode="cover" />
        <Text style={[styles.serifTitle, { color: palette.ink }]}>你的 agent 团队，随身携带</Text>
        <Text style={[styles.lede, { color: palette.inkMuted }]}>
          在电脑上打开 wuu 的「设置 → 远程」，扫描屏幕上的配对二维码。
        </Text>

        {phase.name === "pairing" ? (
          <View style={styles.center}>
            <ActivityIndicator color={palette.accent} />
            <Text style={[styles.lede, { color: palette.inkMuted, marginTop: 10 }]}>配对中…</Text>
          </View>
        ) : (
          <>
            <Pressable
              style={[styles.primaryButton, { backgroundColor: palette.accent }]}
              onPress={() => void openScanner()}
            >
              <Text style={styles.primaryButtonText}>扫码配对</Text>
            </Pressable>

            <Text style={[styles.orLine, { color: palette.inkFaint }]}>或手动粘贴配对链接</Text>
            <TextInput
              style={[
                styles.input,
                { borderColor: palette.hairline, color: palette.ink, backgroundColor: palette.overlay4 },
              ]}
              value={manualUri}
              onChangeText={setManualUri}
              placeholder="wuu://pair?..."
              placeholderTextColor={palette.inkFaint}
              autoCapitalize="none"
              autoCorrect={false}
              onSubmitEditing={() => startPairing(manualUri)}
            />
            {manualUri.trim() !== "" ? (
              <Pressable
                style={[styles.ghostButton, { borderColor: palette.hairline }]}
                onPress={() => startPairing(manualUri)}
              >
                <Text style={[styles.ghostButtonText, { color: palette.ink }]}>用链接配对</Text>
              </Pressable>
            ) : null}
            {phase.name === "error" ? (
              <Text style={[styles.error, { color: palette.danger }]}>{phase.message}</Text>
            ) : null}
          </>
        )}
      </ScrollView>

      <Modal
        visible={phase.name === "scanning"}
        animationType="slide"
        onRequestClose={() => setPhase({ name: "idle" })}
      >
        <View style={styles.scannerPage}>
          <CameraView
            style={StyleSheet.absoluteFill}
            barcodeScannerSettings={{ barcodeTypes: ["qr"] }}
            onBarcodeScanned={(result) => {
              if (scanConsumed.current) return;
              if (!result.data.startsWith("wuu://pair?")) return;
              scanConsumed.current = true;
              startPairing(result.data);
            }}
          />
          <View style={styles.scannerOverlay}>
            <Text style={styles.scannerHint}>对准电脑屏幕上的二维码</Text>
            <Pressable
              style={[styles.ghostButton, styles.scannerCancel]}
              onPress={() => setPhase({ name: "idle" })}
            >
              <Text style={styles.scannerCancelText}>取消</Text>
            </Pressable>
          </View>
        </View>
      </Modal>
    </KeyboardAvoidingView>
  );
}

const styles = StyleSheet.create({
  page: { flex: 1 },
  scroll: { padding: 28, paddingTop: 72, gap: 16 },
  center: { alignItems: "center", justifyContent: "center", gap: 6, flexGrow: 1, padding: 28 },
  hero: {
    width: "100%",
    aspectRatio: 1448 / 1086,
    borderRadius: 12,
  },
  serifTitle: {
    fontFamily: serifFamily,
    fontSize: 26,
    fontWeight: "500",
    lineHeight: 34,
    textAlign: "center",
    marginTop: 10,
  },
  lede: { fontSize: 14, lineHeight: 22, textAlign: "center" },
  primaryButton: {
    marginTop: 14,
    height: 48,
    borderRadius: 10,
    alignItems: "center",
    justifyContent: "center",
  },
  primaryButtonText: { color: "#ffffff", fontSize: 16, fontWeight: "600" },
  ghostButton: {
    height: 44,
    borderRadius: 10,
    borderWidth: 1.5,
    alignItems: "center",
    justifyContent: "center",
  },
  ghostButtonText: { fontSize: 15, fontWeight: "500" },
  orLine: { fontSize: 12, textAlign: "center", marginTop: 18 },
  input: {
    height: 44,
    borderRadius: 10,
    borderWidth: StyleSheet.hairlineWidth,
    paddingHorizontal: 12,
    fontSize: 14,
  },
  error: { fontSize: 13, textAlign: "center" },
  successArt: { width: 200, height: 200 },
  scannerPage: { flex: 1, backgroundColor: "#000" },
  scannerOverlay: {
    position: "absolute",
    top: 0,
    left: 0,
    right: 0,
    bottom: 0,
    justifyContent: "flex-end",
    alignItems: "center",
    paddingBottom: 64,
    gap: 18,
  },
  scannerHint: { color: "#ffffff", fontSize: 15 },
  scannerCancel: { borderColor: "rgba(255,255,255,0.7)", paddingHorizontal: 28 },
  scannerCancelText: { color: "#ffffff", fontSize: 15 },
});
