// @vitest-environment jsdom
import * as React from "react";
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { WorkspaceFileReadResult } from "../../shared/protocol";
import type { PresentationHost, PresenterProps } from "../../shared/workbench";
import { FILE_PREVIEW_ACTIONS } from "../../shared/workbench";
import {
  dispatchFilePreviewAction,
  FilePreviewPresentation,
  filePreviewContentType,
  toFilePreviewSnapshot,
} from "./FilePreviewPresentation";
import { PreviewContentProvider } from "../PreviewContentContext";
import { ApplicationPreviewContent } from "./ApplicationPreviewContent";
import { PluginHost } from "./PluginHost";
import { WorkbenchController } from "./Workbench";

let container: HTMLDivElement;
let root: Root;
let host: PluginHost;
let controller: WorkbenchController;

beforeEach(() => {
  container = document.createElement("div");
  document.body.appendChild(container);
  root = createRoot(container);
  host = new PluginHost({ react: React });
  controller = new WorkbenchController(host);
});

afterEach(() => {
  act(() => root.unmount());
  container.remove();
});

describe("FilePreviewPresentation", () => {
  it("selects an exact MIME key and replaces the complete native root", async () => {
    let props: PresenterProps | undefined;
    await host.activateGeneration({ pluginId: "preview", generation: "one", register(api) {
      api.registerPresenter({ id: "typescript", target: "content.preview", key: "text/typescript", render(value) {
        props = value;
        return <main data-custom-root>custom preview</main>;
      } });
    } });

    renderPreview({ file: textFile(), text: "const ok = true;" });

    expect(container.querySelector("[data-custom-root]")?.textContent).toBe("custom preview");
    expect(container.querySelector("[data-native-root]")).toBeNull();
    expect(props?.key).toBe("text/typescript");
    expect(props?.snapshot).toMatchObject({ contentType: "text/typescript" });
  });

  it("creates deeply immutable sanitized text and binary snapshots", () => {
    const text = toFilePreviewSnapshot({
      workspaceRoot: "/private/repo",
      workspaceRelativePath: "src/index.ts",
      file: textFile(),
      text: "safe text",
      selection: { startLineNumber: 2, startColumn: 3 },
      loading: false,
    });
    expect(Object.isFrozen(text)).toBe(true);
    expect(Object.isFrozen(text.selection)).toBe(true);
    expect(text).toMatchObject({
      workspaceRelativePath: "src/index.ts",
      contentType: "text/typescript",
      text: "safe text",
      readOnly: true,
      dirty: false,
      selection: { startLine: 2, startColumn: 3 },
    });
    expect(JSON.stringify(text)).not.toContain("/private/repo");
    expect(text).not.toHaveProperty("absolute_path");

    const binary = toFilePreviewSnapshot({
      workspaceRoot: "/private/repo",
      workspaceRelativePath: "assets/logo.png",
      file: textFile({
        path: "assets/logo.png",
        absolute_path: "/private/repo/assets/logo.png",
        binary: true,
        text: "raw bytes",
        renderable_url: "wuu-file://local/safe_token",
        renderable_kind: "image",
      }),
      text: "raw bytes",
      loading: false,
    });
    expect(binary).toMatchObject({ contentType: "image/png", binary: true, safeHostUrl: "wuu-file://local/safe_token" });
    expect(binary.text).toBeUndefined();
    const unsafe = toFilePreviewSnapshot({
      workspaceRoot: "/repo", workspaceRelativePath: "image.png",
      file: textFile({ binary: true, renderable_url: "file:///private/image.png" }), loading: false,
    });
    expect(unsafe.safeHostUrl).toBeUndefined();
  });

  it("keeps the presenter boundary across loading, loaded, and error updates", async () => {
    await host.activateGeneration({ pluginId: "states", generation: "one", register(api) {
      api.registerPresenter({ id: "states", target: "content.preview", key: "text/typescript", render({ snapshot }) {
        const value = snapshot as { loading?: boolean; error?: string; text?: string };
        return <main data-state>{value.loading ? "loading" : value.error ?? value.text}</main>;
      } });
    } });
    renderPreview({ loading: true });
    expect(container.querySelector("[data-state]")?.textContent).toBe("loading");
    renderPreview({ file: textFile(), text: "loaded", loading: false });
    expect(container.querySelector("[data-state]")?.textContent).toBe("loaded");
    renderPreview({ error: "failed", loading: false });
    expect(container.querySelector("[data-state]")?.textContent).toBe("failed");
  });

  it("advertises only callbacks, dispatches safe actions, and rejects invalid input", async () => {
    let presentationHost: PresentationHost | undefined;
    await host.activateGeneration({ pluginId: "actions", generation: "one", register(api) {
      api.registerPresenter({ id: "actions", target: "content.preview", key: "text/typescript", render({ host: value }) {
        presentationHost = value;
        return <main />;
      } });
    } });
    const open = vi.fn();
    const reveal = vi.fn();
    const reload = vi.fn();
    renderPreview({ file: textFile(), open, reveal, reload });
    expect(presentationHost?.actions).toEqual([
      FILE_PREVIEW_ACTIONS.open, FILE_PREVIEW_ACTIONS.reveal, FILE_PREVIEW_ACTIONS.reload,
    ]);
    await presentationHost?.invoke(FILE_PREVIEW_ACTIONS.open, { path: "src/index.ts" });
    await presentationHost?.invoke(FILE_PREVIEW_ACTIONS.reveal);
    await presentationHost?.invoke(FILE_PREVIEW_ACTIONS.reload);
    expect(open).toHaveBeenCalledWith("src/index.ts");
    expect(reveal).toHaveBeenCalledWith("src/index.ts");
    expect(reload).toHaveBeenCalledOnce();
    await expect(presentationHost?.invoke(FILE_PREVIEW_ACTIONS.open, { path: "../secret" })).rejects.toThrow("must match");
    await expect(dispatchFilePreviewAction(FILE_PREVIEW_ACTIONS.select, { selection: { startLine: -1 } }, "src/index.ts", {
      select: vi.fn(),
    })).rejects.toThrow("Invalid file preview selection");
    await expect(presentationHost?.invoke(FILE_PREVIEW_ACTIONS.save, { text: "no" })).rejects.toThrow("not supported");
  });

  it("restores the exact native fallback after unload or presenter failure", async () => {
    await host.activateGeneration({ pluginId: "temporary", generation: "one", register(api) {
      api.registerPresenter({ id: "temporary", target: "content.preview", key: "text/typescript", render: () => <main data-custom-root /> });
    } });
    renderPreview({ file: textFile() });
    expect(container.querySelector("[data-custom-root]")).toBeTruthy();
    act(() => host.unload("temporary"));
    expect(container.querySelector("[data-native-root]")?.textContent).toBe("native");

    vi.spyOn(console, "error").mockImplementation(() => undefined);
    await act(async () => host.activateGeneration({ pluginId: "broken", generation: "one", register(api) {
      api.registerPresenter({ id: "broken", target: "content.preview", key: "text/typescript", render: () => { throw new Error("broken"); } });
    } }));
    expect(container.querySelector("[data-native-root]")?.textContent).toBe("native");
  });

  it("uses deterministic extension fallbacks when MIME metadata is unavailable", () => {
    expect(filePreviewContentType("README.md", false)).toBe("text/markdown");
    expect(filePreviewContentType("data.unknown", false)).toBe("text/plain");
    expect(filePreviewContentType("archive.unknown", true)).toBe("application/octet-stream");
  });
});

function renderPreview(overrides: Partial<React.ComponentProps<typeof FilePreviewPresentation>> = {}): void {
  act(() => root.render(
    <FilePreviewPresentation
      host={host}
      controller={controller}
      workspaceRoot="/repo"
      workspaceRelativePath="src/index.ts"
      loading={false}
      fallback={<section data-native-root>native</section>}
      {...overrides}
    />,
  ));
}

function textFile(overrides: Partial<WorkspaceFileReadResult> = {}): WorkspaceFileReadResult {
  return { ...textFileBase(), ...overrides };
}

function textFileBase(): WorkspaceFileReadResult {
  return {
    root: "/repo", path: "src/index.ts", absolute_path: "/repo/src/index.ts",
    size_bytes: 12, mtime_ms: 1, sha256: "a".repeat(64), binary: false,
    truncated: false, text: "const ok = true;",
  };
}

it("routes a provided public preview through its selected slot without the private presenter", () => {
  const service = new ApplicationPreviewContent({
    listWorkspaceDirectory: vi.fn(), readWorkspaceFile: vi.fn(), revealWorkspaceItem: vi.fn(), showWorkspaceItemMenu: vi.fn(),
  });
  const renderSlot = vi.fn(({ name, context }: import("../../../../packages/plugin-sdk/src/ui").UISlotProps) =>
    <div data-public-slot={name}>{String((context?.snapshot as { text: string }).text)}</div>);
  act(() => root.render(<PreviewContentProvider service={service} slots={{ Slot: renderSlot }}>
    <FilePreviewPresentation workspaceRoot="/repo" workspaceRelativePath="x.txt" text="public content"
      loading={false} fallback={<div>native fallback</div>} />
  </PreviewContentProvider>));
  expect(container.textContent).toBe("public content");
  expect(renderSlot.mock.calls[0][0].context?.resource).toEqual({ root: "/repo", path: "x.txt" });
  expect(renderSlot.mock.calls[0][0].context).not.toHaveProperty("dispatchAction");
});
