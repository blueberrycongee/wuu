// The mobile thread view renders ordinary user and agent messages while
// keeping reasoning and tool activity out of the conversation surface.

import { useMemo, useRef, useState } from "react";
import {
  FlatList,
  KeyboardAvoidingView,
  Platform,
  Pressable,
  StyleSheet,
  Text,
  TextInput,
  View,
} from "react-native";
import type { Thread } from "@wuu/protocol";

import { Avatar } from "../components/Avatar";
import { ConnectionBanner } from "../components/ConnectionBanner";
import { chatRowsFromTurns, type ChatRow } from "../lib/chatModel";
import type { AppSnapshot, PendingSend } from "../lib/store";
import { isThreadRunning, threadDisplayTitle } from "../lib/threads";
import { usePalette } from "../theme";

type ListEntry =
  | { key: string; kind: "row"; row: ChatRow }
  | { key: string; kind: "pending"; send: PendingSend }
  | { key: string; kind: "running" };

export function ThreadScreen({
  snapshot,
  threadId,
  onBack,
  onSend,
  onInterrupt,
}: {
  snapshot: AppSnapshot;
  threadId: string;
  onBack: () => void;
  onSend: (thread: Thread, text: string) => Promise<void>;
  onInterrupt: (threadId: string) => void;
}): React.JSX.Element {
  const palette = usePalette();
  const [draft, setDraft] = useState("");
  const [sendError, setSendError] = useState("");
  const listRef = useRef<FlatList<ListEntry>>(null);

  const thread = snapshot.threads.find((candidate) => candidate.id === threadId);
  const running = thread ? isThreadRunning(thread) : false;
  const entries = useMemo<ListEntry[]>(() => {
    if (!thread) return [];
    const rows = chatRowsFromTurns(thread.turns ?? []).map<ListEntry>((row) => ({
      key: row.id,
      kind: "row",
      row,
    }));
    for (const send of snapshot.pending.filter((pending) => pending.threadId === threadId)) {
      rows.push({ key: `pending:${send.clientId}`, kind: "pending", send });
    }
    if (running) rows.push({ key: "running", kind: "running" });
    return rows.reverse();
  }, [running, snapshot.pending, thread, threadId]);

  const send = (): void => {
    if (!thread) return;
    const text = draft.trim();
    if (text === "") return;
    setDraft("");
    setSendError("");
    onSend(thread, text).catch((error: unknown) => {
      setSendError(error instanceof Error ? error.message : String(error));
      setDraft(text);
    });
  };

  if (!thread) {
    return (
      <View style={[styles.page, styles.missing, { backgroundColor: palette.paper }]}>
        <Text style={{ color: palette.inkMuted }}>会话不存在</Text>
        <Pressable onPress={onBack}>
          <Text style={{ color: palette.accent, marginTop: 12 }}>返回</Text>
        </Pressable>
      </View>
    );
  }

  return (
    <KeyboardAvoidingView
      style={[styles.page, { backgroundColor: palette.paper }]}
      behavior={Platform.OS === "ios" ? "padding" : undefined}
    >
      <View style={[styles.header, { borderColor: palette.hairline }]}>
        <Pressable onPress={onBack} hitSlop={12} style={styles.backButton}>
          <Text style={[styles.backGlyph, { color: palette.ink }]}>‹</Text>
        </Pressable>
        <View style={styles.headerTitleCell}>
          <Text style={[styles.headerTitle, { color: palette.inkStrong }]} numberOfLines={1}>
            {threadDisplayTitle(thread)}
          </Text>
        </View>
      </View>
      <ConnectionBanner phase={snapshot.phase} syncError={snapshot.syncError} />

      <FlatList
        ref={listRef}
        inverted
        data={entries}
        keyExtractor={(entry) => entry.key}
        contentContainerStyle={styles.listContent}
        renderItem={({ item }) => <Entry entry={item} />}
      />

      {sendError ? (
        <Text style={[styles.sendError, { color: palette.danger }]}>{sendError}</Text>
      ) : null}

      <View style={[styles.composer, { borderColor: palette.hairline }]}>
        <TextInput
          style={[
            styles.input,
            { backgroundColor: palette.overlay4, color: palette.ink, borderColor: palette.hairline },
          ]}
          value={draft}
          onChangeText={setDraft}
          placeholder="输入消息"
          placeholderTextColor={palette.inkFaint}
          multiline
        />
        {running ? (
          <Pressable
            style={[styles.sendButton, { backgroundColor: palette.danger }]}
            onPress={() => onInterrupt(thread.id)}
            accessibilityLabel="停止"
          >
            <View style={styles.stopGlyph} />
          </Pressable>
        ) : (
          <Pressable
            style={[
              styles.sendButton,
              { backgroundColor: draft.trim() === "" ? palette.inkFaint : palette.accent },
            ]}
            onPress={send}
            disabled={draft.trim() === ""}
            accessibilityLabel="发送"
          >
            <Text style={styles.sendGlyph}>↑</Text>
          </Pressable>
        )}
      </View>
    </KeyboardAvoidingView>
  );
}

function Entry({ entry }: { entry: ListEntry }): React.JSX.Element {
  const palette = usePalette();

  if (entry.kind === "pending") {
    return (
      <View style={styles.userRow}>
        <View style={[styles.userBubble, styles.pendingBubble, { backgroundColor: palette.userBubble }]}>
          <Text style={[styles.bubbleText, { color: palette.ink }]}>{entry.send.text}</Text>
        </View>
        <Text style={[styles.pendingHint, { color: palette.inkMuted }]}>发送中…</Text>
      </View>
    );
  }

  if (entry.kind === "running") {
    return (
      <View style={styles.runningRow}>
        <Text style={[styles.runningText, { color: palette.inkMuted }]}>正在回复…</Text>
      </View>
    );
  }

  if (entry.row.kind === "user") {
    return (
      <View style={styles.userRow}>
        <View style={[styles.userBubble, { backgroundColor: palette.userBubble }]}>
          <Text style={[styles.bubbleText, { color: palette.ink }]} selectable>
            {entry.row.item.text ?? ""}
          </Text>
        </View>
      </View>
    );
  }

  return (
    <View style={styles.agentRow}>
      <Avatar id={entry.row.item.agent_id ?? "wuu"} name="wuu" size={28} />
      <View style={[styles.agentBubble, { backgroundColor: palette.overlay4 }]}>
        <Text style={[styles.bubbleText, { color: palette.ink }]} selectable>
          {entry.row.item.text ?? ""}
        </Text>
      </View>
    </View>
  );
}

const styles = StyleSheet.create({
  page: { flex: 1 },
  missing: { alignItems: "center", justifyContent: "center" },
  header: {
    flexDirection: "row",
    alignItems: "center",
    minHeight: 52,
    paddingTop: 8,
    paddingHorizontal: 12,
    borderBottomWidth: StyleSheet.hairlineWidth,
  },
  backButton: { width: 36, height: 36, alignItems: "center", justifyContent: "center" },
  backGlyph: { fontSize: 34, lineHeight: 36, fontWeight: "300" },
  headerTitleCell: { flex: 1, alignItems: "center", paddingRight: 36 },
  headerTitle: { fontSize: 16, fontWeight: "600" },
  listContent: { paddingHorizontal: 14, paddingVertical: 16, gap: 12 },
  userRow: { alignItems: "flex-end", gap: 3 },
  userBubble: {
    maxWidth: "72%",
    paddingHorizontal: 12,
    paddingVertical: 8,
    borderTopLeftRadius: 12,
    borderTopRightRadius: 12,
    borderBottomRightRadius: 3,
    borderBottomLeftRadius: 12,
  },
  pendingBubble: { opacity: 0.65 },
  pendingHint: { fontSize: 11 },
  agentRow: { flexDirection: "row", gap: 8, alignItems: "flex-start" },
  agentBubble: {
    maxWidth: "88%",
    paddingHorizontal: 12,
    paddingVertical: 8,
    borderRadius: 12,
  },
  bubbleText: { fontSize: 15, lineHeight: 23 },
  runningRow: { alignItems: "flex-start", paddingLeft: 36 },
  runningText: { fontSize: 12 },
  sendError: { fontSize: 12, textAlign: "center", paddingVertical: 4 },
  composer: {
    flexDirection: "row",
    alignItems: "flex-end",
    gap: 8,
    paddingHorizontal: 12,
    paddingTop: 8,
    paddingBottom: 28,
    borderTopWidth: StyleSheet.hairlineWidth,
  },
  input: {
    flex: 1,
    minHeight: 38,
    maxHeight: 120,
    borderRadius: 18,
    borderWidth: StyleSheet.hairlineWidth,
    paddingHorizontal: 14,
    paddingTop: 9,
    paddingBottom: 9,
    fontSize: 15,
  },
  sendButton: {
    width: 34,
    height: 34,
    borderRadius: 17,
    alignItems: "center",
    justifyContent: "center",
  },
  sendGlyph: { color: "#ffffff", fontSize: 18, fontWeight: "700" },
  stopGlyph: { width: 12, height: 12, borderRadius: 2, backgroundColor: "#ffffff" },
});
