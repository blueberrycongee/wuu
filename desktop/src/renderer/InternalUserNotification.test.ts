import { describe, expect, it } from "vitest";

import {
  AGENT_NOTIFICATION_NAME,
  PROCESS_NOTIFICATION_NAME,
  isAgentNotificationText,
  isInternalUserNotificationItem,
  isProcessNotificationItem,
  isProcessNotificationText,
} from "./InternalUserNotification";

const processNotificationText =
  '<process_notification>{"process_id":"proc-1"}</process_notification>';

describe("legacy agent notification classification", () => {
  const notification = '<subagent_notification>{"status":"completed"}</subagent_notification>';

  it("recognizes padded XML, routed objects and nested content envelopes", () => {
    for (const text of [
      notification,
      JSON.stringify({ author: "/root/worker", recipient: "/root", content: "Done" }),
      JSON.stringify({ content: notification }),
      JSON.stringify({ content: JSON.stringify({ content: notification }) }),
    ]) {
      expect(isAgentNotificationText(` \n${text}\t `)).toBe(true);
    }
  });

  it("keeps ordinary text, non-object JSON and malformed envelopes visible", () => {
    for (const text of [
      undefined, "", "真实用户消息", "Explain {this} code",
      "null", "true", "123", "[]", JSON.stringify(notification),
      JSON.stringify([{ content: notification }]),
      "{not valid JSON}", '{"content":',
      JSON.stringify({ content: "Ordinary user text" }),
      JSON.stringify({ author: "/root/worker", recipient: "/user" }),
      '<subagent_notification>{"status":"completed"}',
    ]) {
      expect(isAgentNotificationText(text)).toBe(false);
    }
  });
});

describe("process notification classification", () => {
  it.each(["host", "plugin"])("keeps %s session messages visible even when they quote internal envelopes", (origin) => {
    for (const name of [PROCESS_NOTIFICATION_NAME, AGENT_NOTIFICATION_NAME]) {
      expect(isInternalUserNotificationItem({ name, text: processNotificationText, origin, presentation_kind: "session_message" })).toBe(false);
      expect(isInternalUserNotificationItem({ name, text: processNotificationText, origin, presentation_kind: "query_bubble" })).toBe(true);
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
