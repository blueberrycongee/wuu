import { describe, expect, it } from "vitest";
import {
  DIRECTORY_POLL_BASE_MS,
  DIRECTORY_POLL_MAX_MS,
  nextDirectoryPollDelay,
} from "./ChannelDirectoryPoll";

describe("nextDirectoryPollDelay", () => {
  it("stays on the base cadence while the directory changes", () => {
    expect(nextDirectoryPollDelay(DIRECTORY_POLL_MAX_MS, true)).toBe(DIRECTORY_POLL_BASE_MS);
  });

  it("backs off while the directory stays the same, then stops at the ceiling", () => {
    expect(nextDirectoryPollDelay(DIRECTORY_POLL_BASE_MS, false)).toBe(4_000);
    expect(nextDirectoryPollDelay(16_000, false)).toBe(DIRECTORY_POLL_MAX_MS);
    expect(nextDirectoryPollDelay(DIRECTORY_POLL_MAX_MS, false)).toBe(DIRECTORY_POLL_MAX_MS);
  });
});
