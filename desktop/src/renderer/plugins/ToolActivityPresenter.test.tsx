import * as React from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { ThreadItem } from "../../shared/protocol";
import type { ViewHostAPI } from "../../shared/workbench";
import { PluginHost } from "./PluginHost";
import { ToolActivityPresenter, toToolActivitySnapshot } from "./ToolActivityPresenter";
import { WorkbenchController } from "./Workbench";

let root: Root | undefined;
let container: HTMLDivElement | undefined;
let controller: WorkbenchController | undefined;

afterEach(() => {
  act(() => root?.unmount());
  container?.remove();
  controller?.dispose();
  root = undefined;
  container = undefined;
  controller = undefined;
});

describe("ToolActivityPresenter", () => {
  it("adapts only public fields, preserves raw arguments, and deeply freezes structured data", () => {
    const item = toolItem({
      seq: 9,
      source_id: "private-source",
      arguments: '{"unfinished":',
      result: "done",
      result_detail: {
        content: [{ type: "text", text: "ok", resource: { nested: [1] } }],
        structured_content: { answer: [42] },
        meta: { trace: "public-result-metadata" },
        activity: { id: "job", kind: "process", thread_id: "thread" },
      },
    });
    const snapshot = toToolActivitySnapshot(item);

    expect(snapshot).toEqual({
      contractVersion: 1,
      id: "call",
      toolName: "echo",
      capability: "tool.echo",
      kind: "test",
      status: "running",
      argumentsText: '{"unfinished":',
      resultText: "done",
      structuredResult: {
        content: [{ type: "text", text: "ok", resource: { nested: [1] } }],
        structuredContent: { answer: [42] },
        metadata: { trace: "public-result-metadata" },
        activity: { id: "job", kind: "process", threadId: "thread" },
      },
    });
    expect("seq" in snapshot).toBe(false);
    expect("source_id" in snapshot).toBe(false);
    expect(Object.isFrozen(snapshot)).toBe(true);
    expect(Object.isFrozen(snapshot.structuredResult?.structuredContent)).toBe(true);
    expect(Object.isFrozen((snapshot.structuredResult?.content?.[0].resource as { nested: number[] }).nested)).toBe(true);
  });

  it("uses capability before tool name and reacts to registration and unload", async () => {
    const host = new PluginHost({ react: React });
    let presenterHost: ViewHostAPI | undefined;
    setup(host);
    render(host, toolItem(), <span data-fallback>native</span>);
    expect(container?.querySelector("[data-fallback]")).toBeTruthy();

    await act(async () => host.activateGeneration({
      pluginId: "presenter",
      generation: "one",
      register(api) {
        api.registerToolActivityPresenter({
          id: "by-name",
          key: "echo",
          render: () => <span data-name>name</span>,
        });
        api.registerToolActivityPresenter({
          id: "by-capability",
          key: "tool.echo",
          render: ({ activity, host: apiHost }) => {
            presenterHost = apiHost;
            return <section data-custom-root>{activity.argumentsText}</section>;
          },
        });
      },
    }));
    expect(container?.querySelector("[data-custom-root]")?.textContent).toBe('{"value":1}');
    expect(container?.querySelector("[data-name]")).toBeNull();

    act(() => host.unload("presenter"));
    expect(container?.querySelector("[data-fallback]")).toBeTruthy();
    await expect(presenterHost?.getSetting("enabled")).rejects.toThrow("no longer active");
  });

  it("uses the exact fallback after a presenter render error without unloading it", async () => {
    const host = new PluginHost({ react: React });
    setup(host);
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => undefined);
    await host.activateGeneration({
      pluginId: "broken",
      generation: "one",
      register(api) {
        api.registerToolActivityPresenter({
          id: "broken",
          key: "tool.echo",
          render: () => { throw new Error("render failed"); },
        });
      },
    });
    render(host, toolItem(), <aside data-fallback>native</aside>);
    expect(container?.querySelector("aside[data-fallback]")?.textContent).toBe("native");
    expect(host.getToolActivityPresenter("tool.echo")?.pluginId).toBe("broken");
    consoleError.mockRestore();
  });
});

function setup(host: PluginHost): void {
  controller = new WorkbenchController(host);
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
}

function render(host: PluginHost, item: ThreadItem, fallback: React.ReactNode): void {
  act(() => root!.render(
    <ToolActivityPresenter item={item} fallback={fallback} host={host} controller={controller} />,
  ));
}

function toolItem(overrides: Partial<ThreadItem> = {}): ThreadItem {
  return {
    id: "call",
    type: "tool_call",
    status: "in_progress",
    name: "echo",
    arguments: '{"value":1}',
    display: { capability: "tool.echo", kind: "test" },
    ...overrides,
  };
}
