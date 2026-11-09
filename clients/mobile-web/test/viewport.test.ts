// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { startWebViewportSync } from "../src/lib/viewport";

let viewport: EventTarget & { height: number; scale: number };
let stop: (() => void) | undefined;
const height = () => document.documentElement.style.getPropertyValue("--web-viewport-height");

beforeEach(() => {
  vi.useFakeTimers();
  viewport = Object.assign(new EventTarget(), { height: 800, scale: 1 });
  vi.stubGlobal("visualViewport", viewport);
});
afterEach(() => {
  stop?.();
  stop = undefined;
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  vi.useRealTimers();
});

describe("workbench viewport", () => {
  it("coalesces keyboard opening and dismissal without resetting on blur", () => {
    const textarea = document.createElement("textarea");
    document.body.append(textarea);
    textarea.value = "unfinished draft";
    textarea.focus();
    stop = startWebViewportSync();
    expect(height()).toBe("800px");
    const write = vi.spyOn(document.documentElement.style, "setProperty");
    viewport.height = 600;
    viewport.dispatchEvent(new Event("resize"));
    window.dispatchEvent(new Event("resize"));
    viewport.height = 420;
    viewport.dispatchEvent(new Event("resize"));
    expect(write).not.toHaveBeenCalled();
    vi.advanceTimersToNextFrame();
    expect(height()).toBe("420px");
    expect(write).toHaveBeenCalledTimes(1);

    textarea.blur();
    expect(height()).toBe("420px");
    viewport.height = 650;
    viewport.dispatchEvent(new Event("resize"));
    viewport.height = 800;
    window.dispatchEvent(new Event("resize"));
    vi.advanceTimersToNextFrame();
    expect(height()).toBe("800px");
    expect(write).toHaveBeenCalledTimes(2);
    expect(textarea.value).toBe("unfinished draft");
    textarea.remove();
  });

  it("does not reflow pinch zoom or unusable viewport measurements", () => {
    stop = startWebViewportSync();
    viewport.scale = 2;
    viewport.height = 400;
    viewport.dispatchEvent(new Event("resize"));
    vi.advanceTimersToNextFrame();
    expect(height()).toBe("800px");
    viewport.scale = 1;
    viewport.height = 0;
    viewport.dispatchEvent(new Event("resize"));
    vi.advanceTimersToNextFrame();
    expect(height()).toBe("800px");
    viewport.height = 700;
    viewport.dispatchEvent(new Event("resize"));
    vi.advanceTimersToNextFrame();
    expect(height()).toBe("700px");
  });

  it("supports browsers without VisualViewport and refreshes restored pages", () => {
    vi.stubGlobal("visualViewport", undefined);
    vi.stubGlobal("innerHeight", 800);
    stop = startWebViewportSync();
    vi.stubGlobal("innerHeight", 420);
    window.dispatchEvent(new Event("resize"));
    vi.advanceTimersToNextFrame();
    expect(height()).toBe("420px");
    vi.stubGlobal("innerHeight", 800);
    window.dispatchEvent(new Event("pageshow"));
    vi.advanceTimersToNextFrame();
    expect(height()).toBe("800px");
  });

  it("does not write unchanged geometry or resurrect it after leaving the workbench", () => {
    stop = startWebViewportSync();
    const write = vi.spyOn(document.documentElement.style, "setProperty");
    viewport.dispatchEvent(new Event("resize"));
    vi.advanceTimersToNextFrame();
    expect(write).not.toHaveBeenCalled();
    viewport.height = 420;
    viewport.dispatchEvent(new Event("resize"));
    stop();
    stop = undefined;
    window.dispatchEvent(new Event("pageshow"));
    vi.advanceTimersToNextFrame();
    expect(write).not.toHaveBeenCalled();
    expect(height()).toBe("");
  });
});

it("waits for keyboard resizing to settle and scrolls only the form's remaining occlusion", () => {
  const form = document.createElement('main'); form.className = 'account-home';
  const input = document.createElement('input'); form.append(input); document.body.append(form);
  input.focus();
  let fieldTop = 520;
  vi.spyOn(form, 'getBoundingClientRect').mockImplementation(() => ({ top: 0, bottom: viewport.height } as DOMRect));
  vi.spyOn(input, 'getBoundingClientRect').mockImplementation(() => ({ top: fieldTop, bottom: fieldTop + 56 } as DOMRect));
  stop = startWebViewportSync();
  viewport.height = 500; viewport.dispatchEvent(new Event('resize')); vi.advanceTimersToNextFrame();
  vi.advanceTimersByTime(60);
  viewport.height = 420; viewport.dispatchEvent(new Event('resize')); vi.advanceTimersToNextFrame();
  expect(form.scrollTop).toBe(0);
  // The browser has already scrolled most of the way during the IME animation.
  fieldTop = 380;
  vi.advanceTimersByTime(120);
  expect(form.scrollTop).toBe(28);
  expect(document.documentElement.scrollTop).toBe(0);
  expect(document.activeElement).toBe(input);
  fieldTop = 300;
  viewport.height = 800; viewport.dispatchEvent(new Event('resize')); vi.advanceTimersToNextFrame();
  vi.advanceTimersByTime(120);
  expect(form.scrollTop).toBe(28);
  form.remove();
});

it("reveals a newly focused field with the keyboard already open and cancels pending work on stop", () => {
  const form = document.createElement('main'); form.className = 'account-home';
  const input = document.createElement('input'); form.append(input); document.body.append(form);
  vi.spyOn(form, 'getBoundingClientRect').mockReturnValue({ top: 0, bottom: 420 } as DOMRect);
  vi.spyOn(input, 'getBoundingClientRect').mockReturnValue({ top: -20, bottom: 36 } as DOMRect);
  form.scrollTop = 80;
  viewport.height = 420;
  stop = startWebViewportSync();
  input.focus();
  vi.advanceTimersByTime(120);
  expect(form.scrollTop).toBe(48);
  input.blur(); input.focus();
  stop(); stop = undefined;
  vi.advanceTimersByTime(120);
  expect(form.scrollTop).toBe(48);
  form.remove();
});
