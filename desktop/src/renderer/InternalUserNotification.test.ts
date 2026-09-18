import { describe, expect, it } from "vitest";

import {
  AGENT_NOTIFICATION_NAME,
  PROCESS_NOTIFICATION_NAME,
  isInternalUserNotificationItem,
  isProcessNotificationItem,
  isProcessNotificationText,
} from "./InternalUserNotification";

const processNotificationText =
  '<process_notification>{"process_id":"proc-1"}</process_notification>';

describe("process notification classification", () => {
  it("keeps attributed session messages visible even when they quote internal envelopes", () => {
    for (const name of [PROCESS_NOTIFICATION_NAME, AGENT_NOTIFICATION_NAME]) {
      expect(isInternalUserNotificationItem({ name, text: processNotificationText, origin: "plugin", presentation_kind: "session_message" })).toBe(false);
    }
  });

  it("uses the protocol name as the primary signal", () => {
    expect(
      isProcessNotificationItem({
        name: PROCESS_NOTIFICATION_NAME,
        text: "unparseable historical payload",
      }),
    ).toBe(true);
  });

  it("recognizes complete legacy text envelopes without a name", () => {
    expect(isProcessNotificationText(`  ${processNotificationText}\n`)).toBe(true);
    expect(isProcessNotificationItem({ text: processNotificationText })).toBe(true);
  });

  it("does not classify incomplete envelopes or normal user text", () => {
    expect(
      isProcessNotificationText('<process_notification>{"process_id":"proc-1"}'),
    ).toBe(false);
    expect(isProcessNotificationItem({ text: "后台命令完成了吗？" })).toBe(false);
    expect(isProcessNotificationItem(undefined)).toBe(false);
  });

  it("classifies both process notifications and agent handoffs as internal", () => {
    expect(isInternalUserNotificationItem({ name: PROCESS_NOTIFICATION_NAME })).toBe(true);
    expect(isInternalUserNotificationItem({ name: AGENT_NOTIFICATION_NAME })).toBe(true);
    expect(isInternalUserNotificationItem({ text: "真实用户消息" })).toBe(false);
  });
});
