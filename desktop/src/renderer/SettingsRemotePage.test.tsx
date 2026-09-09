/** Phone access toggle, pairing, and device revocation behavior. */
import { afterEach, describe, expect, it } from "vitest";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";

import { SettingsRemotePage, type RemoteStatusView, type SettingsRemotePageProps } from "./SettingsRemotePage";

let container: HTMLDivElement | null = null;
let root: Root | null = null;

function mount(props: SettingsRemotePageProps): void {
  if (container) unmount();
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  act(() => {
    root!.render(<SettingsRemotePage {...props} />);
  });
}

function unmount(): void {
  if (root) {
    act(() => root!.unmount());
    root = null;
  }
  container?.remove();
  container = null;
}

afterEach(unmount);

const baseStatus: RemoteStatusView = {
  fingerprint: "3a5ec62add99",
  host_name: "studio",
  relay_url: "ws://127.0.0.1:8787/v1/connect",
  store: "/home/user/.wuu/remote.json",
  devices: []
};

function baseProps(overrides: Partial<SettingsRemotePageProps> = {}): SettingsRemotePageProps {
  return {
    status: baseStatus,
    statusError: "",
    hostRunning: false,
    pairUri: null,
    busy: false,
    onToggleHost: () => {},
    onOpenPairing: () => {},
    onRemoveDevice: () => {},
    ...overrides
  };
}

async function flushAsync(): Promise<void> {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
}

describe("SettingsRemotePage relay + switch", () => {
  it("can disable saved access when startup failed", () => {
    const toggles: boolean[] = [];
    mount(baseProps({ hostEnabled: true, hostRunning: false, statusError: "LAN unavailable", onToggleHost: value => toggles.push(value) }));
    const toggle = container!.querySelector<HTMLButtonElement>('[role="switch"]')!;
    expect(toggle.getAttribute("aria-checked")).toBe("true");
    act(() => toggle.click());
    expect(toggles).toEqual([false]);
  });

  it("enables phone access without a relay configuration", () => {
    const toggles: boolean[] = [];
    mount(baseProps({ status: null, onToggleHost: value => toggles.push(value) }));
    const toggle = container!.querySelector<HTMLButtonElement>(".settings-switch")!;
    expect(toggle.disabled).toBe(false);
    act(() => toggle.click());
    expect(toggles).toEqual([true]);
  });

  it("toggles the host through onToggleHost", () => {
    const toggles: boolean[] = [];
    mount(baseProps({ hostRunning: true, onToggleHost: (enabled) => toggles.push(enabled) }));
    const toggle = container!.querySelector<HTMLButtonElement>(".settings-switch")!;
    expect(toggle.getAttribute("aria-checked")).toBe("true");
    act(() => toggle.click());
    expect(toggles).toEqual([false]);
  });
});

describe("SettingsRemotePage pairing", () => {
  it("gates the pairing button on a running host", () => {
    let opened = 0;
    mount(baseProps({ onOpenPairing: () => opened++ }));
    const button = [...container!.querySelectorAll<HTMLButtonElement>("button")].find(
      (b) => b.textContent === "显示配对二维码"
    )!;
    expect(button.disabled).toBe(true);

    mount(baseProps({ hostRunning: true, onOpenPairing: () => opened++ }));
    const enabled = [...container!.querySelectorAll<HTMLButtonElement>("button")].find(
      (b) => b.textContent === "显示配对二维码"
    )!;
    expect(enabled.disabled).toBe(false);
    act(() => enabled.click());
    expect(opened).toBe(1);
  });

  it("renders the QR svg and the copyable URI while pairing", async () => {
    const uri = "http://192.168.1.2:8787/#pair=wuu%3A%2F%2Fpair%3Ftest";
    mount(baseProps({ hostRunning: true, pairUri: uri }));

    await flushAsync();
    const qr = container!.querySelector('[data-testid="remote-pair-qr"]')!;
    expect(qr.innerHTML).toContain("<svg");
  });
});

describe("SettingsRemotePage devices", () => {
  it("shows the empty state", () => {
    mount(baseProps());
    expect(container!.textContent).toContain("尚未配对任何手机");
  });

  it("lists devices and revokes through onRemoveDevice", () => {
    const removed: string[] = [];
    mount(
      baseProps({
        status: {
          ...baseStatus,
          devices: [
            { pub: "PUB1", fingerprint: "2579e6ff1255", name: "我的手机", added_at: "2026-07-07T00:00:00Z" },
            { pub: "PUB2", fingerprint: "c8465106f505", name: "", added_at: "not-a-date" }
          ]
        },
        onRemoveDevice: (device) => removed.push(device.pub)
      })
    );
    expect(container!.textContent).toContain("我的手机");
    // Format the fixture instant the same way the page does so the expected
    // date follows the runtime timezone instead of hard-coding one zone's day.
    const pairedAt = new Intl.DateTimeFormat("zh-CN", {
      year: "numeric",
      month: "short",
      day: "numeric"
    }).format(new Date("2026-07-07T00:00:00Z"));
    expect(container!.textContent).toContain(`2579e6ff1255 · 配对于 ${pairedAt}`);
    expect(container!.textContent).toContain("未命名设备");
    expect(container!.textContent).toContain("not-a-date");
    const revoke = [...container!.querySelectorAll<HTMLButtonElement>("button")].filter(
      (b) => b.textContent === "吊销"
    );
    expect(revoke).toHaveLength(2);
    act(() => revoke[0].click());
    expect(removed).toEqual(["PUB1"]);
  });

  it("surfaces status errors", () => {
    mount(baseProps({ status: null, statusError: "wuu remote status failed" }));
    expect(container!.querySelector(".settings-error")!.textContent).toBe("wuu remote status failed");
  });
});

it("allows replacing a displayed pairing code", () => {
  let opened = 0;
  mount(baseProps({ hostRunning: true, pairUri: "http://192.168.1.2:8787/#pair=test", onOpenPairing: () => opened++ }));
  const buttons = container!.querySelectorAll<HTMLButtonElement>('[data-testid="remote-pair-panel"] button');
  act(() => buttons[buttons.length - 1].click());
  expect(opened).toBe(1);
});

it("keeps account access usable without advertising unsupported QR pairing", () => {
  let paired = false;
  mount(baseProps({ status: { ...baseStatus, account_server: "https://accounts.example" }, hostRunning:true, webUrl:"https://accounts.example", onOpenPairing:()=>{paired=true;} }));
  expect(container!.querySelector('[role="switch"]')?.getAttribute('aria-checked')).toBe('true');
  expect(container!.querySelector('[data-testid="remote-pair-panel"]')).toBeNull();
  expect(container!.querySelectorAll('button')).toHaveLength(1);
  expect(container!.querySelector('code')).toBeNull();
  expect(paired).toBe(false);
});
