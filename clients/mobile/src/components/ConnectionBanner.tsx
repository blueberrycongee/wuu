// Connection-state strip under the header. Quiet by design: nothing renders
// while attached and synchronized; connecting/reconnecting or a sync failure
// shows a hairline status strip.

import { StyleSheet, Text, View } from "react-native";

import type { ConnectionPhase } from "../lib/store";
import { usePalette } from "../theme";

export function ConnectionBanner({
  phase,
  syncError,
}: {
  phase: ConnectionPhase;
  syncError: string | null;
}): React.JSX.Element | null {
  const palette = usePalette();
  if (!syncError && (phase === "attached" || phase === "idle")) return null;
  const label = syncError
    ? `同步失败：${syncError}`
    : phase === "connecting"
      ? "连接中…"
      : "重连中…";
  return (
    <View style={[styles.strip, { backgroundColor: palette.overlay4, borderColor: palette.hairline }]}>
      <Text style={[styles.label, { color: syncError ? palette.danger : palette.inkMuted }]}>
        {label}
      </Text>
    </View>
  );
}

const styles = StyleSheet.create({
  strip: {
    flexDirection: "row",
    alignItems: "center",
    justifyContent: "center",
    gap: 6,
    paddingVertical: 5,
    borderBottomWidth: StyleSheet.hairlineWidth,
  },
  label: { fontSize: 12 },
});
