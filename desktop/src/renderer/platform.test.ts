import { afterEach, describe, expect, it } from "vitest";
import type { WuuDesktopApi } from "../shared/protocol";
import {
  applyPlatformStamp,
  desktopPlatform,
} from "./platform";

function setApiPlatform(platform: unknown): void {
  (window as { wuu?: Partial<WuuDesktopApi> }).wuu = {
    platform: platform as WuuDesktopApi["platform"],
  };
}

afterEach(() => {
  delete (window as { wuu?: unknown }).wuu;
  delete document.documentElement.dataset.platform;
  delete document.documentElement.dataset.hostKind;
});

describe("desktopPlatform", () => {
  it("trusts window.wuu.platform when it names a known platform", () => {
    setApiPlatform("win32");
    expect(desktopPlatform()).toBe("win32");
    setApiPlatform("darwin");
    expect(desktopPlatform()).toBe("darwin");
  });

  it("falls back to the user agent when the API value is absent or bogus", () => {
    setApiPlatform("beos");
    // jsdom's UA carries neither Windows nor Macintosh.
    expect(desktopPlatform()).toBe("linux");
    delete (window as { wuu?: unknown }).wuu;
    expect(desktopPlatform()).toBe("linux");
  });
});

describe("applyPlatformStamp", () => {
  it("stamps data-platform when missing and never overwrites the preload's stamp", () => {
    setApiPlatform("win32");
    applyPlatformStamp();
    expect(document.documentElement.dataset.platform).toBe("win32");

    document.documentElement.dataset.platform = "darwin";
    applyPlatformStamp();
    expect(document.documentElement.dataset.platform).toBe("darwin");
  });
});
