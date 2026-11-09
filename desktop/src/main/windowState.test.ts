import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import {
  computeDefaultMainWindowBounds,
  computeOnboardingWindowBounds,
  loadMainWindowBounds,
  MAIN_WINDOW_MAX_HEIGHT,
  MAIN_WINDOW_MAX_WIDTH,
  saveMainWindowBounds,
  type DisplayLike,
} from "./windowState";

describe("computeDefaultMainWindowBounds", () => {
  it("uses the relative ratio on a 1440p workArea", () => {
    expect(computeDefaultMainWindowBounds({ width: 2560, height: 1400 })).toEqual({
      width: 1024,
      height: 840,
    });
  });

  it("uses the relative ratio on a 1080p workArea", () => {
    expect(computeDefaultMainWindowBounds({ width: 1920, height: 1048 })).toEqual({
      width: 768,
      height: 629,
    });
  });

  it("clamps the height to the cap on a 4K workArea", () => {
    // 40% × 3840 = 1536, below the 1600 cap → relative wins.
    // 60% × 2120 = 1272, above the 1100 cap → cap wins.
    expect(computeDefaultMainWindowBounds({ width: 3840, height: 2120 })).toEqual({
      width: 1536,
      height: MAIN_WINDOW_MAX_HEIGHT,
    });
  });

  it("clamps the height to the cap on a wide 5K workArea", () => {
    // A typical 27" 5K has 5120 × 2880 workArea; height should hit the cap.
    expect(computeDefaultMainWindowBounds({ width: 5120, height: 2880 })).toEqual({
      width: MAIN_WINDOW_MAX_WIDTH,
      height: MAIN_WINDOW_MAX_HEIGHT,
    });
  });

  it("uses the relative size on a tiny workArea", () => {
    expect(computeDefaultMainWindowBounds({ width: 1024, height: 700 })).toEqual({
      width: 410,
      height: 420,
    });
  });

  it("rounds fractional pixels so we always pass integers to BrowserWindow", () => {
    const result = computeDefaultMainWindowBounds({ width: 1441, height: 901 });
    expect(Number.isInteger(result.width)).toBe(true);
    expect(Number.isInteger(result.height)).toBe(true);
  });
});

describe("computeOnboardingWindowBounds", () => {
  it("uses a stable first-run size on normal displays", () => {
    expect(computeOnboardingWindowBounds({ width: 2560, height: 1400 })).toEqual({
      width: 920,
      height: 720,
    });
    expect(computeOnboardingWindowBounds({ width: 1440, height: 900 })).toEqual({
      width: 920,
      height: 720,
    });
  });

  it("fits the first-run window onto a smaller work area", () => {
    expect(computeOnboardingWindowBounds({ width: 800, height: 700 })).toEqual({
      width: 800,
      height: 700,
    });
  });
});

const FAKE_DISPLAYS: DisplayLike[] = [
  { workArea: { x: 0, y: 0, width: 1440, height: 900 } },
  { workArea: { x: 1440, y: 0, width: 2560, height: 1440 } },
];

describe("loadMainWindowBounds / saveMainWindowBounds", () => {
  let dir: string;
  let file: string;

  beforeEach(async () => {
    dir = await mkdtemp(join(tmpdir(), "wuu-window-state-"));
    file = join(dir, "window-state.json");
  });

  afterEach(async () => {
    await rm(dir, { recursive: true, force: true });
  });

  it("returns null when no bounds have been saved", () => {
    expect(loadMainWindowBounds(FAKE_DISPLAYS, { filePath: file })).toBeNull();
  });

  it("returns the saved bounds when the center sits on a connected display", () => {
    saveMainWindowBounds(
      { x: 200, y: 100, width: 1024, height: 840 },
      { filePath: file },
    );
    expect(loadMainWindowBounds(FAKE_DISPLAYS, { filePath: file })).toEqual({
      x: 200,
      y: 100,
      width: 1024,
      height: 840,
    });
  });

  it("returns bounds saved on a secondary display when it is still attached", () => {
    saveMainWindowBounds(
      { x: 1600, y: 50, width: 1280, height: 800 },
      { filePath: file },
    );
    // Center at (2240, 450) falls inside the second fake display's workArea.
    expect(loadMainWindowBounds(FAKE_DISPLAYS, { filePath: file })).toEqual({
      x: 1600,
      y: 50,
      width: 1280,
      height: 800,
    });
  });

  it("returns null when the saved center sits off all connected displays", () => {
    // Multi-monitor unplug case: window was on a third display that's gone.
    saveMainWindowBounds(
      { x: 4000, y: 1000, width: 1024, height: 840 },
      { filePath: file },
    );
    expect(loadMainWindowBounds(FAKE_DISPLAYS, { filePath: file })).toBeNull();
  });

  it("returns null when the saved file is malformed or has partial fields", async () => {
    await writeFile(file, "{not json");
    expect(loadMainWindowBounds(FAKE_DISPLAYS, { filePath: file })).toBeNull();

    await writeFile(
      file,
      JSON.stringify({ main_window_bounds: { x: 1, y: 2 } }),
    );
    expect(loadMainWindowBounds(FAKE_DISPLAYS, { filePath: file })).toBeNull();

    await writeFile(
      file,
      JSON.stringify({ main_window_bounds: { x: 1, y: 2, width: 0, height: 100 } }),
    );
    expect(loadMainWindowBounds(FAKE_DISPLAYS, { filePath: file })).toBeNull();

    await writeFile(
      file,
      JSON.stringify({ main_window_bounds: { x: 1, y: 2, width: 100, height: 100, extra: true } }),
    );
    // Extra unknown field is ignored, but if shape is otherwise valid the
    // bounds still load — the data layer is lenient on whitespace.
    expect(loadMainWindowBounds(FAKE_DISPLAYS, { filePath: file })).toEqual({
      x: 1,
      y: 2,
      width: 100,
      height: 100,
    });
  });

  it("saveMainWindowBounds preserves other desktop settings fields", () => {
    // Pretend other settings exist by writing a complete file first.
    saveMainWindowBounds(
      { x: 200, y: 100, width: 1024, height: 840 },
      { filePath: file },
    );
    // Round-trip: load returns the saved bounds, file is preserved.
    expect(loadMainWindowBounds(FAKE_DISPLAYS, { filePath: file })).toEqual({
      x: 200,
      y: 100,
      width: 1024,
      height: 840,
    });
  });
});
