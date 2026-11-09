import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import type { SubscriptionQuota } from "../shared/protocol";
import { ConnectionUsageSummary } from "./ConnectionUsageSummary";
import { translate } from "./i18n";

const now = Date.parse("2026-10-18T12:00:00Z");
let container: HTMLDivElement;
let root: Root;
beforeEach(() => {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
});
afterEach(() => { act(() => root.unmount()); container.remove(); });
function render(overrides?: Partial<SubscriptionQuota>) {
  const quota: SubscriptionQuota | undefined = overrides && {
    status: "available", checked_at: "2026-10-18T12:00:00Z", ...overrides,
  };
  act(() => root.render(<ConnectionUsageSummary quota={quota} now={now} />));
}

describe("ConnectionUsageSummary account allowance contract", () => {
  it("keeps short and weekly allowances and their reset instants separate", () => {
    render({ windows: [
      { id: "short", window_minutes: 300, used_percent: 25, resets_at: "2026-10-18T14:00:00Z" },
      { id: "weekly", window_minutes: 10080, used_percent: 80, resets_at: "2026-10-23T12:00:00Z" },
    ] });
    const meters = [...container.querySelectorAll("meter")];
    expect(meters.map(meter => meter.value)).toEqual([75, 20]);
    expect(meters[0].parentElement?.textContent).toContain("75");
    expect(meters[0].getAttribute("aria-valuetext")).toContain("75");
    expect(meters[1].parentElement?.textContent).toContain("20");
    expect(meters[1].getAttribute("aria-valuetext")).toContain("20");
    expect(meters[0].getAttribute("aria-label")).not.toBe(meters[1].getAttribute("aria-label"));
    expect(meters[0].parentElement?.textContent).toMatch(/75%.*5h/);
    expect(meters[1].parentElement?.textContent).toMatch(/20%.*7d/);
    const resetTimes = [...container.querySelectorAll("time")];
    for (const time of resetTimes) {
      const localDate = new Intl.DateTimeFormat("zh-CN", { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" }).format(new Date(time.dateTime));
      expect(time.textContent).toContain(localDate);
    }
    expect(resetTimes.map(time => time.dateTime)).toEqual([
      "2026-10-18T14:00:00Z", "2026-10-23T12:00:00Z",
    ]);
  });

  it("never presents missing, invalid or unlimited allowances as a finite meter", () => {
    render();
    expect(container.childElementCount).toBe(0);
    expect(container.querySelector("meter")).toBeNull();
    render({ windows: [undefined, NaN, Infinity, -1].map((used_percent, index) => ({ id: String(index), used_percent })) });
    expect(container.querySelector("meter")).toBeNull();
    expect(container.childElementCount).toBe(0);
    render({ windows: [{ id: "unlimited", used_percent: 0, unlimited: true }] });
    expect(container.querySelector("meter")).toBeNull();
  });

  it("preserves valid zero-use and exhausted observations without inventing capacity", () => {
    render({ windows: [{ id: "unused", used_percent: 0 }, { id: "over", used_percent: 120 }] });
    expect([...container.querySelectorAll("meter")].map(meter => meter.value)).toEqual([100, 0]);
  });

  it("omits unsupported, unavailable and sign-in snapshots even with cached values", () => {
    for (const status of ["unavailable", "unsupported", "sign_in"] as const) {
      render({ status, windows: [{ id: "short", used_percent: 20 }] });
      expect(container.childElementCount).toBe(0);
    }
    render({});
    expect(container.childElementCount).toBe(0);
    act(() => root.render(<ConnectionUsageSummary loading now={now} />));
    expect(container.childElementCount).toBe(0);
  });

  it("distinguishes failed refreshes, expired snapshots and past resets from live usage", () => {
    const windows = [{ id: "short", used_percent: 95 }];
    for (const quota of [
      { status: "stale" as const, windows },
      { windows, expires_at: "2026-10-18T11:59:00Z" },
      { windows: [{ ...windows[0], resets_at: "2026-10-18T11:59:00Z" }] },
    ]) {
      render(quota);
      expect(container.querySelector("meter")).toBeNull();
      expect(container.textContent).toContain(translate("zh-CN", "settings.subscriptionLastKnownRemaining", { percent: "5" }));
      expect(container.querySelector('[data-warning="true"], [data-exhausted="true"]')).toBeNull();
    }
    render({ status: "stale", windows });
    expect(container.textContent).toContain(translate("zh-CN", "settings.subscriptionQuotaRefreshFailed"));
    render({ windows, expires_at: "2026-10-18T11:59:00Z" });
    expect(container.textContent).toContain(translate("zh-CN", "settings.subscriptionQuotaStale"));
  });

  it("restricts warning and exhausted signals to fresh meters", () => {
    render({ windows: [79, 80, 100, 120].map(used_percent => ({ id: String(used_percent), used_percent })) });
    const meters = [...container.querySelectorAll("meter")];
    expect(meters.map(meter => meter.dataset.warning)).toEqual(["false", "true", "true", "true"]);
    expect(meters.map(meter => meter.dataset.exhausted)).toEqual(["false", "false", "true", "true"]);
    expect(meters.every(meter => !meter.parentElement?.hasAttribute("data-exhausted"))).toBe(true);
    expect(meters.map(meter => meter.value)).toEqual([21, 20, 0, 0]);
    expect(container.textContent).not.toContain("120%");
  });

  it("preserves supplied balance precision, display and model scope without guessing a percentage", () => {
    render({ balances: [{ currency: "USD", amount: "0.0123456789" }], windows: [
      { id: "requests", display: "7 / 40 requests", model: "special-model", scope: "organization" },
    ] });
    expect(container.textContent).toContain("0.0123456789");
    expect(container.textContent).toContain("7 / 40 requests");
    expect(container.textContent).toContain("special-model");
    expect(container.textContent).toContain("organization");
    expect(container.querySelector("meter")).toBeNull();
  });
});
