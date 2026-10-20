import { Blob as NodeBlob } from "node:buffer";
import { act, useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { ConversationAttachmentPreview } from "./ConversationAttachmentPreview";
const cleanups: (() => void)[] = [];
afterEach(() => cleanups.splice(0).reverse().forEach(fn => fn()));
async function mount(mediaType: string, read: () => Promise<Blob>) {
  const container = document.createElement("div"); document.body.append(container);
  const root = createRoot(container);
  cleanups.push(() => { act(() => root.unmount()); container.remove(); vi.restoreAllMocks(); });
  await act(async () => root.render(<ConversationAttachmentPreview resourceId="remote:one" name="file.html" mediaType={mediaType} size={5} read={read} />));
  return container;
}
it("shows HTML as text, never as an executable document", async () => {
  vi.stubGlobal("URL", { createObjectURL: () => "blob:test", revokeObjectURL: vi.fn() });
  cleanups.push(() => vi.unstubAllGlobals());
  const container = await mount("text/html", async () => new NodeBlob(['<script>alert(1)</script><h1>unsafe</h1>']) as unknown as Blob);
  await vi.waitFor(() => expect(container.querySelector("pre")?.textContent).toContain("<script>"));
  expect(container.querySelector("iframe,script,h1,object,embed")).toBeNull();
});
it("reads Markdown attachments with the shared document presentation", async () => {
  vi.stubGlobal("URL", { createObjectURL: () => "blob:test", revokeObjectURL: vi.fn() });
  cleanups.push(() => vi.unstubAllGlobals());
  const container = document.createElement("div"); document.body.append(container);
  const root = createRoot(container);
  cleanups.push(() => { act(() => root.unmount()); container.remove(); });
  await act(async () => root.render(<ConversationAttachmentPreview resourceId="remote:markdown" name="report.md" mediaType="text/plain" size={16} read={async () => new NodeBlob(["# Report\n\n**Ready**"]) as unknown as Blob} />));
  await vi.waitFor(() => expect(container.querySelector(".workspace-markdown-reading")).not.toBeNull());
  expect(container.querySelector(".rich-heading")?.textContent).toBe("Report");
  expect(container.querySelector("strong")?.textContent).toBe("Ready");
  expect(container.querySelector("pre")).toBeNull();
});
it("shows pending reads and errors without an external navigation fallback", async () => {
  let reject!: (error: Error) => void;
  const container = await mount("text/plain", () => new Promise((_resolve, fail) => { reject = fail; }));
  expect(container.querySelector('[role="status"]')).not.toBeNull();
  await act(async () => reject(new Error("Verified read failed")));
  expect(container.querySelector('[role="alert"]')?.textContent).toContain("Verified read failed");
  expect(container.querySelector("a")).toBeNull();
});
it("offers download only for unsupported files", async () => {
  vi.stubGlobal("URL", { createObjectURL: () => "blob:test", revokeObjectURL: vi.fn() });
  cleanups.push(() => vi.unstubAllGlobals());
  const container = await mount("application/zip", async () => new Blob(["hello"]));
  await vi.waitFor(() => expect(container.querySelector("a[download]")).not.toBeNull());
  expect(container.querySelector("iframe,object,embed,img,pre")).toBeNull();
});

it("opens card bodies internally while the download control stays independent", async () => {
  const { PluginConversationAttachment } = await import("./plugins/PluginConversation");
  const { ConversationAttachmentPreviewContext } = await import("./ConversationAttachmentPreview");
  const open = vi.fn(), load = vi.fn(async () => "blob:verified");
  vi.stubGlobal("fetch", vi.fn(async () => ({ ok: true, blob: async () => new Blob(["hello"]) })));
  cleanups.push(() => vi.unstubAllGlobals());
  const container = document.createElement("div"); document.body.append(container);
  const root = createRoot(container);
  cleanups.push(() => { act(() => root.unmount()); container.remove(); });
  await act(async () => root.render(<ConversationAttachmentPreviewContext.Provider value={open}><PluginConversationAttachment resourceId="agent:1:file:0:sha" name="report.txt" mediaType="text/plain" size={5} url="blob:verified" loadLabel="Read" downloadLabel="Download" onLoad={load} /></ConversationAttachmentPreviewContext.Provider>));
  const download = container.querySelector<HTMLAnchorElement>('a[download]')!;
  download.addEventListener("click", event => event.preventDefault());
  await act(async () => download.click());
  expect(open).not.toHaveBeenCalled();
  expect(load).not.toHaveBeenCalled();
  await act(async () => container.querySelector<HTMLButtonElement>('[data-action="open-attachment"]')!.click());
  expect(open).toHaveBeenCalledOnce();
  expect(open.mock.calls[0][0]).toMatchObject({ resourceId: "agent:1:file:0:sha", name: "report.txt" });
  expect(container.querySelector("button button,button a")).toBeNull();
});

it("keeps verified tab reads and retry independent of an unmounted attachment card", async () => {
  const { PluginConversationAttachment } = await import("./plugins/PluginConversation");
  const { ConversationAttachmentPreviewContext } = await import("./ConversationAttachmentPreview");
  const blob = new Blob(["verified"]);
  const read = vi.fn().mockRejectedValueOnce(new Error("Temporary transport failure")).mockResolvedValue(blob);
  const load = vi.fn(), open = vi.fn();
  const container = document.createElement("div"); document.body.append(container);
  const root = createRoot(container);
  cleanups.push(() => { act(() => root.unmount()); container.remove(); });
  await act(async () => root.render(<ConversationAttachmentPreviewContext.Provider value={open}><PluginConversationAttachment resourceId="output:sha" name="result.txt" mediaType="text/plain" size={8} loadLabel="Read" downloadLabel="Download" onLoad={load} onRead={read} /></ConversationAttachmentPreviewContext.Provider>));
  await act(async () => container.querySelector<HTMLButtonElement>('[data-action="open-attachment"]')!.click());
  const request = open.mock.calls[0][0];
  await act(async () => root.render(null));
  await expect(request.read()).rejects.toThrow("Temporary transport failure");
  await expect(request.read()).resolves.toBe(blob);
  await expect(request.read()).resolves.toBe(blob);
  expect(read).toHaveBeenCalledTimes(2);
  expect(load).not.toHaveBeenCalled();
});

it("retains the verified Blob URL reader for legacy attachment callers", async () => {
  const { PluginConversationAttachment } = await import("./plugins/PluginConversation");
  const { ConversationAttachmentPreviewContext } = await import("./ConversationAttachmentPreview");
  const blob = new Blob(["legacy"]), open = vi.fn(), load = vi.fn(async () => "blob:legacy-verified");
  const fetchBlob = vi.fn(async () => ({ ok: true, blob: async () => blob }));
  vi.stubGlobal("fetch", fetchBlob); cleanups.push(() => vi.unstubAllGlobals());
  const container = document.createElement("div"); document.body.append(container);
  const root = createRoot(container);
  cleanups.push(() => { act(() => root.unmount()); container.remove(); });
  await act(async () => root.render(<ConversationAttachmentPreviewContext.Provider value={open}><PluginConversationAttachment name="legacy.txt" mediaType="text/plain" size={6} loadLabel="Read" downloadLabel="Download" onLoad={load} /></ConversationAttachmentPreviewContext.Provider>));
  await act(async () => container.querySelector<HTMLButtonElement>('[data-action="open-attachment"]')!.click());
  await expect(open.mock.calls[0][0].read()).resolves.toBe(blob);
  expect(load).toHaveBeenCalledOnce();
  expect(fetchBlob).toHaveBeenCalledWith("blob:legacy-verified");
});

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((done, fail) => { resolve = done; reject = fail; });
  return { promise, resolve, reject };
}

it("waits for a legacy void loader's URL and captures bytes before opening unmounts its card", async () => {
  const { PluginConversationAttachment } = await import("./plugins/PluginConversation");
  const { ConversationAttachmentPreviewContext } = await import("./ConversationAttachmentPreview");
  const loaded = deferred<string>(), bytes = deferred<Blob>(), blob = new Blob(["legacy async"]);
  const revoked = vi.fn(), open = vi.fn(), onOpen = vi.fn();
  const fetchBlob = vi.fn(async () => ({ ok: true, blob: () => bytes.promise }));
  vi.stubGlobal("fetch", fetchBlob); cleanups.push(() => vi.unstubAllGlobals());
  const load = vi.fn<() => void>();
  function LegacyAttachment() {
    const [url, setURL] = useState<string>();
    useEffect(() => () => { if (url) revoked(url); }, [url]);
    load.mockImplementation(() => { void loaded.promise.then(setURL); });
    return <PluginConversationAttachment name="legacy.txt" mediaType="text/plain" size={12} url={url} loadLabel="Read" downloadLabel="Download" onLoad={load} onOpen={onOpen} />;
  }
  const container = document.createElement("div"); document.body.append(container);
  const root = createRoot(container);
  cleanups.push(() => { act(() => root.unmount()); container.remove(); });
  await act(async () => root.render(<ConversationAttachmentPreviewContext.Provider value={open}><LegacyAttachment /></ConversationAttachmentPreviewContext.Provider>));
  onOpen.mockImplementation(() => root.render(null));
  await act(async () => {
    const button = container.querySelector<HTMLButtonElement>('[data-action="open-attachment"]')!;
    button.click(); button.click();
  });
  expect(load).toHaveBeenCalledOnce();
  expect(open).not.toHaveBeenCalled();
  expect(onOpen).not.toHaveBeenCalled();
  await act(async () => loaded.resolve("blob:legacy-async"));
  expect(fetchBlob).toHaveBeenCalledWith("blob:legacy-async");
  expect(open).not.toHaveBeenCalled();
  expect(revoked).not.toHaveBeenCalled();
  await act(async () => bytes.resolve(blob));
  expect(onOpen).toHaveBeenCalledOnce();
  expect(open).toHaveBeenCalledOnce();
  expect(revoked).toHaveBeenCalledWith("blob:legacy-async");
  await expect(open.mock.calls[0][0].read()).resolves.toBe(blob);
  await expect(open.mock.calls[0][0].read()).resolves.toBe(blob);
  expect(fetchBlob).toHaveBeenCalledOnce();
});

it("lets legacy prop errors and failed byte reads retry without reopening early", async () => {
  const { PluginConversationAttachment } = await import("./plugins/PluginConversation");
  const { ConversationAttachmentPreviewContext } = await import("./ConversationAttachmentPreview");
  const open = vi.fn(), onOpen = vi.fn(), blob = new Blob(["recovered"]);
  const fetchBlob = vi.fn().mockRejectedValueOnce(new Error("Snapshot failed")).mockResolvedValue({ ok: true, blob: async () => blob });
  vi.stubGlobal("fetch", fetchBlob); cleanups.push(() => vi.unstubAllGlobals());
  let update!: (value: { url?: string; loading?: boolean; error?: string }) => void;
  const load = vi.fn(() => { update({ loading: true }); });
  function LegacyAttachment() {
    const [state, setState] = useState<{ url?: string; loading?: boolean; error?: string }>({});
    update = setState;
    return <PluginConversationAttachment name="retry.txt" mediaType="text/plain" size={9} {...state} loadLabel="Read" downloadLabel="Download" onLoad={load} onOpen={onOpen} />;
  }
  const container = document.createElement("div"); document.body.append(container);
  const root = createRoot(container);
  cleanups.push(() => { act(() => root.unmount()); container.remove(); });
  await act(async () => root.render(<ConversationAttachmentPreviewContext.Provider value={open}><LegacyAttachment /></ConversationAttachmentPreviewContext.Provider>));
  const click = async () => act(async () => container.querySelector<HTMLButtonElement>('[data-action="open-attachment"]')!.click());
  await click();
  await act(async () => update({ error: "Verification failed" }));
  expect(container.querySelector('[role="alert"]')?.textContent).toContain("Verification failed");
  expect(open).not.toHaveBeenCalled();
  expect(onOpen).not.toHaveBeenCalled();
  await click();
  expect(load).toHaveBeenCalledTimes(2);
  await act(async () => update({ error: "Verification failed" }));
  await click();
  expect(load).toHaveBeenCalledTimes(3);
  await act(async () => update({ url: "blob:retry" }));
  expect(container.querySelector('[role="alert"]')?.textContent).toContain("Snapshot failed");
  expect(open).not.toHaveBeenCalled();
  await click();
  expect(load).toHaveBeenCalledTimes(3);
  expect(fetchBlob).toHaveBeenCalledTimes(2);
  expect(onOpen).toHaveBeenCalledOnce();
  expect(open).toHaveBeenCalledOnce();
  await expect(open.mock.calls[0][0].read()).resolves.toBe(blob);
});

it("completes an async legacy download once without opening or reading a preview", async () => {
  const { PluginConversationAttachment } = await import("./plugins/PluginConversation");
  const { ConversationAttachmentPreviewContext } = await import("./ConversationAttachmentPreview");
  const loaded = deferred<string>(), open = vi.fn(), onOpen = vi.fn(), read = vi.fn(), fetchBlob = vi.fn();
  vi.stubGlobal("fetch", fetchBlob); cleanups.push(() => vi.unstubAllGlobals());
  const downloads: { href: string; name: string }[] = [];
  const download = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (this: HTMLAnchorElement) {
    downloads.push({ href: this.href, name: this.download });
  });
  cleanups.push(() => download.mockRestore());
  const load = vi.fn<() => void>();
  function LegacyAttachment() {
    const [url, setURL] = useState<string>();
    load.mockImplementation(() => { void loaded.promise.then(setURL); });
    return <PluginConversationAttachment name="download.txt" mediaType="text/plain" size={8} url={url} loadLabel="Read" downloadLabel="Download" onLoad={load} onRead={read} onOpen={onOpen} />;
  }
  const container = document.createElement("div"); document.body.append(container);
  const root = createRoot(container);
  cleanups.push(() => { act(() => root.unmount()); container.remove(); });
  await act(async () => root.render(<ConversationAttachmentPreviewContext.Provider value={open}><LegacyAttachment /></ConversationAttachmentPreviewContext.Provider>));
  await act(async () => {
    const button = container.querySelector<HTMLButtonElement>('[data-action="load-attachment"]')!;
    button.click(); button.click();
  });
  expect(load).toHaveBeenCalledOnce();
  await act(async () => loaded.resolve("blob:download"));
  expect(downloads).toEqual([{ href: "blob:download", name: "download.txt" }]);
  expect(open).not.toHaveBeenCalled();
  expect(onOpen).not.toHaveBeenCalled();
  expect(read).not.toHaveBeenCalled();
  expect(fetchBlob).not.toHaveBeenCalled();
});

it("does not open or download after a pending legacy load unmounts", async () => {
  const { PluginConversationAttachment } = await import("./plugins/PluginConversation");
  const { ConversationAttachmentPreviewContext } = await import("./ConversationAttachmentPreview");
  const loaded = deferred<string>(), open = vi.fn(), onOpen = vi.fn(), fetchBlob = vi.fn();
  vi.stubGlobal("fetch", fetchBlob); cleanups.push(() => vi.unstubAllGlobals());
  const download = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
  cleanups.push(() => download.mockRestore());
  const load = vi.fn(() => loaded.promise);
  const container = document.createElement("div"); document.body.append(container);
  const root = createRoot(container);
  cleanups.push(() => { act(() => root.unmount()); container.remove(); });
  await act(async () => root.render(<ConversationAttachmentPreviewContext.Provider value={open}><PluginConversationAttachment name="cancelled.txt" mediaType="text/plain" size={8} loadLabel="Read" downloadLabel="Download" onLoad={load} onOpen={onOpen} /></ConversationAttachmentPreviewContext.Provider>));
  await act(async () => {
    container.querySelector<HTMLButtonElement>('[data-action="open-attachment"]')!.click();
    container.querySelector<HTMLButtonElement>('[data-action="load-attachment"]')!.click();
  });
  expect(load).toHaveBeenCalledOnce();
  await act(async () => root.render(null));
  await act(async () => loaded.resolve("blob:unmounted"));
  expect(open).not.toHaveBeenCalled();
  expect(onOpen).not.toHaveBeenCalled();
  expect(fetchBlob).not.toHaveBeenCalled();
  expect(download).not.toHaveBeenCalled();
});

it.each(["return", "prop"] as const)("rejects unsafe legacy URLs supplied by %s and allows retry", async source => {
  const { PluginConversationAttachment } = await import("./plugins/PluginConversation");
  const { ConversationAttachmentPreviewContext } = await import("./ConversationAttachmentPreview");
  const open = vi.fn(), fetchBlob = vi.fn(async () => ({ ok: true, blob: async () => new Blob(["safe"]) }));
  vi.stubGlobal("fetch", fetchBlob); cleanups.push(() => vi.unstubAllGlobals());
  const load = vi.fn(async () => source === "return" ? "https://unverified.test/file.txt" : undefined);
  const container = document.createElement("div"); document.body.append(container);
  const root = createRoot(container);
  cleanups.push(() => { act(() => root.unmount()); container.remove(); });
  const render = async (url?: string) => act(async () => root.render(<ConversationAttachmentPreviewContext.Provider value={open}><PluginConversationAttachment name="safe.txt" mediaType="text/plain" size={4} url={url} loadLabel="Read" downloadLabel="Download" onLoad={load} /></ConversationAttachmentPreviewContext.Provider>));
  await render();
  await act(async () => container.querySelector<HTMLButtonElement>('[data-action="open-attachment"]')!.click());
  if (source === "prop") await render("https://unverified.test/file.txt");
  expect(open).not.toHaveBeenCalled();
  expect(fetchBlob).not.toHaveBeenCalled();
  expect(container.querySelector('[role="alert"]')).not.toBeNull();
  await render("blob:safe");
  await act(async () => container.querySelector<HTMLButtonElement>('[data-action="open-attachment"]')!.click());
  expect(open).toHaveBeenCalledOnce();
  expect(fetchBlob).toHaveBeenCalledWith("blob:safe");
});
