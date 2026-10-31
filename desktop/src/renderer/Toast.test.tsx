import * as React from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  clearToasts,
  dismissToast,
  showActionErrorToast,
  showErrorToast,
  showToast,
  ToastViewport,
  toastErrorMessage,
} from "./Toast";
import { WuuUIRoot } from "./ui/layers/UILayerHost";
import { PluginHost } from "./plugins/PluginHost";
import { ApplicationController } from "./plugins/ApplicationController.test-support";
import type { ApplicationServiceV1 } from "../../../packages/plugin-sdk/src/ui";
import { PluginLifecycleCancellationError } from "../shared/PluginLifecycle";

let container: HTMLDivElement;
let root: Root | null = null;

beforeEach(() => {
  vi.useFakeTimers();
  clearToasts();
  container = document.createElement("div");
  document.body.appendChild(container);
  act(() => {
    root = createRoot(container);
    root.render(
      <WuuUIRoot>
        <ToastViewport />
      </WuuUIRoot>,
    );
  });
});

afterEach(() => {
  act(() => root?.unmount());
  root = null;
  container.remove();
  clearToasts();
  vi.useRealTimers();
});

describe("ToastViewport", () => {
  it("removes Electron IPC wrappers from operation errors", () => {
    expect(toastErrorMessage(new Error(
      "Error invoking remote method 'wuu:project-remove': Error: project cannot be removed while a session is running",
    ))).toBe("project cannot be removed while a session is running");
    expect(toastErrorMessage(
      "Error: Error invoking remote method 'wuu:project-remove': Error: save failed",
    )).toBe("save failed");
  });

  it("renders operation errors as global alert toasts", () => {
    act(() => {
      showErrorToast(new Error("save failed"));
    });

    const notice = container.querySelector('[role="alert"]');
    expect(notice?.textContent).toContain("save failed");
    expect(notice?.closest('[data-wuu-layer-host="true"]')).not.toBeNull();
  });

  it("does not present a retired host action rejected through the common error sink", async () => {
    const controller = new ApplicationController();
    let finish!: () => void;
    const invoke = vi.spyOn(controller, "invoke").mockImplementation(async () => new Promise<void>(resolve => { finish = resolve; }));
    const host = new PluginHost({ react: React, application: controller });
    let application!: ApplicationServiceV1;
    await host.activateGeneration({ pluginId: "shell", generation: "one", register: api => { application = api.application; } });
    let failure: unknown;
    const pending = application.invoke("navigation.mark-viewed", { threadIds: ["thread"] }).catch(error => {
      failure = error;
      return showErrorToast(error);
    });
    try {
      expect(finish).toBeTypeOf("function");
      await act(async () => { host.unload("shell"); finish(); await pending; });
      expect(invoke).toHaveBeenCalledOnce();
      expect(failure).toBeInstanceOf(PluginLifecycleCancellationError);
      expect(failure).toMatchObject({ name: "AbortError", code: "WUU_PLUGIN_SCOPE_RETIRED" });
      expect(container.querySelector('[role="alert"]')).toBeNull();
    } finally { host.unload("shell"); invoke.mockRestore(); }
  });

  it.each([
    new Error(new PluginLifecycleCancellationError().message),
    Object.assign(new Error("Provider aborted the operation"), { name: "AbortError" }),
    new Error("Replacement preparation failed"),
  ])("still presents failures without an owned retirement: %s", error => {
    act(() => { showErrorToast(error); });
    expect(container.querySelector('[role="alert"]')?.textContent).toContain(error.message);
  });

  it("does not hide an explicit failure when its rejection has no message", () => {
    act(() => { showActionErrorToast(undefined); });
    expect(container.querySelector('[role="alert"]')?.textContent?.trim()).toBeTruthy();
  });

  it("coalesces a failure already waiting in the queue and returns its dismissible handle", () => {
    let first: number | undefined;
    let duplicate: number | undefined;
    act(() => {
      showErrorToast("active failure");
      first = showErrorToast("queued failure");
      duplicate = showErrorToast("queued failure");
    });
    expect(duplicate).toBe(first);
    act(() => {
      dismissToast(duplicate!);
      vi.advanceTimersByTime(6_000);
    });
    expect(container.querySelector('[role="alert"]')).toBeNull();
  });

  it("queues distinct notices and suppresses repeated failures", () => {
    act(() => {
      showErrorToast("first failure");
      showErrorToast("first failure");
      showToast({ message: "second notice" });
    });
    expect(container.querySelector(".archive-tip-message")?.textContent).toBe("first failure");

    act(() => {
      vi.advanceTimersByTime(6_000);
    });
    expect(container.querySelector(".archive-tip-message")?.textContent).toBe("second notice");

    act(() => {
      vi.advanceTimersByTime(6_000);
    });
    expect(container.querySelector(".archive-tip")).toBeNull();
  });
});
