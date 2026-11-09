// Conversation list: pinned section, then running-first two-band ordering
// (mirrors the desktop sidebar). Unread is the purely-local cursor; running
// threads pulse instead of marking unread.

import { useMemo } from "react";
import {
  FlatList,
  Pressable,
  RefreshControl,
  StyleSheet,
  Text,
  View,
} from "react-native";
import type { Thread } from "@wuu/protocol";

import type { AppSnapshot } from "../lib/store";
import { isThreadRunning, isThreadUnread, sortThreads, threadDisplayTitle } from "../lib/threads";
import { formatListTimestamp } from "../lib/format";
import { usePalette } from "../theme";
import { Avatar } from "../components/Avatar";
import { ConnectionBanner } from "../components/ConnectionBanner";
import { EmptyState } from "../components/EmptyState";

import EMPTY_MASCOT from "../../assets/art/empty-mascot.png";

type Row = { thread: Thread; pinnedSection: boolean };

export function ChatsScreen({
  snapshot,
  refreshing,
  onRefresh,
  onOpenThread,
  onLongPressThread,
}: {
  snapshot: AppSnapshot;
  refreshing: boolean;
  onRefresh: () => void;
  onOpenThread: (thread: Thread) => void;
  onLongPressThread: (thread: Thread) => void;
}): React.JSX.Element {
  const palette = usePalette();

  const rows = useMemo<Row[]>(() => {
    const pinned = snapshot.threads.filter((t) => t.pinned === true);
    const rest = snapshot.threads.filter((t) => t.pinned !== true);
    return [
      ...sortThreads(pinned).map((thread) => ({ thread, pinnedSection: true })),
      ...sortThreads(rest).map((thread) => ({ thread, pinnedSection: false })),
    ];
  }, [snapshot.threads]);

  return (
    <View style={[styles.page, { backgroundColor: palette.paper }]}>
      <View style={[styles.header, { borderColor: palette.hairline }]}>
        <Text style={[styles.headerTitle, { color: palette.inkStrong }]}>
          {snapshot.hostName || "wuu"}
        </Text>
      </View>
      <ConnectionBanner phase={snapshot.phase} syncError={snapshot.syncError} />
      <FlatList
        data={rows}
        keyExtractor={(row) => row.thread.id}
        refreshControl={
          <RefreshControl refreshing={refreshing} onRefresh={onRefresh} tintColor={palette.accent} />
        }
        ListEmptyComponent={
          <EmptyState
            image={EMPTY_MASCOT}
            title="还没有对话"
            hint="在电脑上开始一个会话，这里就会出现。"
          />
        }
        contentContainerStyle={rows.length === 0 ? styles.emptyFill : undefined}
        renderItem={({ item }) => (
          <ThreadRow
            thread={item.thread}
            pinned={item.pinnedSection}
            unread={isThreadUnread(item.thread, snapshot.lastViewed)}
            onPress={() => onOpenThread(item.thread)}
            onLongPress={() => onLongPressThread(item.thread)}
          />
        )}
      />
    </View>
  );
}

function ThreadRow({
  thread,
  pinned,
  unread,
  onPress,
  onLongPress,
}: {
  thread: Thread;
  pinned: boolean;
  unread: boolean;
  onPress: () => void;
  onLongPress: () => void;
}): React.JSX.Element {
  const palette = usePalette();
  const running = isThreadRunning(thread);

  return (
    <Pressable
      onPress={onPress}
      onLongPress={onLongPress}
      style={({ pressed }) => [
        styles.row,
        { borderColor: palette.hairline },
        pinned ? { backgroundColor: palette.overlay4 } : null,
        pressed ? { backgroundColor: palette.overlay6 } : null,
      ]}
    >
      <View style={styles.rowAvatar}>
        <Avatar
          id={thread.id}
          name={threadDisplayTitle(thread)}
          size={44}
          status={running ? "busy" : null}
        />
      </View>
      <View style={styles.rowBody}>
        <View style={styles.rowTop}>
          <Text style={[styles.rowTitle, { color: palette.ink }]} numberOfLines={1}>
            {threadDisplayTitle(thread)}
          </Text>
          <Text style={[styles.rowTime, { color: palette.inkFaint }]}>
            {formatListTimestamp(thread.updated_at ?? thread.created_at)}
          </Text>
        </View>
        <View style={styles.rowBottom}>
          <Text style={[styles.rowPreview, { color: palette.inkMuted }]} numberOfLines={1}>
            {running ? "正在回复…" : thread.preview?.trim() || " "}
          </Text>
          {unread ? <View style={[styles.unreadDot, { backgroundColor: palette.accent }]} /> : null}
        </View>
      </View>
    </Pressable>
  );
}

const styles = StyleSheet.create({
  page: { flex: 1 },
  header: {
    paddingTop: 58,
    paddingBottom: 12,
    paddingHorizontal: 20,
    borderBottomWidth: StyleSheet.hairlineWidth,
  },
  headerTitle: { fontSize: 17, fontWeight: "600" },
  emptyFill: { flexGrow: 1 },
  row: {
    flexDirection: "row",
    alignItems: "center",
    gap: 12,
    paddingHorizontal: 16,
    paddingVertical: 11,
    borderBottomWidth: StyleSheet.hairlineWidth,
  },
  rowAvatar: { width: 48, alignItems: "center" },
  rowBody: { flex: 1, gap: 3 },
  rowTop: { flexDirection: "row", alignItems: "baseline", gap: 8 },
  rowTitle: { flex: 1, fontSize: 15.5, fontWeight: "500" },
  rowTime: { fontSize: 11 },
  rowBottom: { flexDirection: "row", alignItems: "center", gap: 8 },
  rowPreview: { flex: 1, fontSize: 13 },
  unreadDot: { width: 8, height: 8, borderRadius: 4 },
});
