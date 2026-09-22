import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, expect, it, vi } from "vitest";
import { dialog, type BrowserWindow, type Session } from "electron";
import { saveArtifactFile } from "./artifactSave";

vi.mock("electron", () => ({ dialog: { showSaveDialog: vi.fn() } }));
const parent = {} as BrowserWindow;
const session = { fetch: vi.fn() } as unknown as Session;
let directory: string | undefined;
afterEach(async () => {
  vi.resetAllMocks();
  if (directory) await rm(directory, { recursive: true, force: true });
});

it("saves original bytes to the chosen directory and derives the image extension", async () => {
  directory = await mkdtemp(join(tmpdir(), "wuu-artifact-save-"));
  const destination = join(directory, "renamed.gif");
  vi.mocked(dialog.showSaveDialog).mockResolvedValue({ canceled: false, filePath: destination });
  const bytes = Buffer.from("GIF89a original animation");
  await saveArtifactFile(parent, session, "Photo 1", `data:image/gif;base64,${bytes.toString("base64")}`);
  expect(await readFile(destination)).toEqual(bytes);
  expect(dialog.showSaveDialog).toHaveBeenCalledWith(parent, { defaultPath: "Photo 1.gif" });
});

it("keeps an existing file untouched when the save dialog is canceled", async () => {
  directory = await mkdtemp(join(tmpdir(), "wuu-artifact-save-"));
  const destination = join(directory, "existing.svg");
  await writeFile(destination, "keep");
  vi.mocked(dialog.showSaveDialog).mockResolvedValue({ canceled: true, filePath: destination });
  await saveArtifactFile(parent, session, "diagram", "data:image/svg+xml,%3Csvg%2F%3E");
  expect(await readFile(destination, "utf8")).toBe("keep");
});

it("uses the sender session for managed sources and reports failed reads before writing", async () => {
  const source = "wuu-artifact://workspace/thread/id/chart.png?sha256=hash";
  vi.mocked(session.fetch).mockResolvedValue(new Response("missing", { status: 404 }));
  await expect(saveArtifactFile(parent, session, "chart.png", source)).rejects.toThrow("404");
  expect(session.fetch).toHaveBeenCalledWith(source);
  expect(dialog.showSaveDialog).not.toHaveBeenCalled();
});

it("suggests only the filename of a URL and preserves the original remote bytes", async () => {
  directory = await mkdtemp(join(tmpdir(), "wuu-artifact-save-"));
  const destination = join(directory, "photo.webp");
  vi.mocked(dialog.showSaveDialog).mockResolvedValue({ canceled: false, filePath: destination });
  vi.mocked(session.fetch).mockResolvedValue(new Response("original", { headers: { "content-type": "image/webp" } }));
  await saveArtifactFile(parent, session, "https://example.com/images/photo.webp?token=private", "https://example.com/images/photo.webp");
  expect(dialog.showSaveDialog).toHaveBeenCalledWith(parent, { defaultPath: "photo.webp" });
  expect(await readFile(destination, "utf8")).toBe("original");
});

it.each(["file:///private/file", "javascript:alert(1)"])("rejects unsupported source %s", async source => {
  await expect(saveArtifactFile(parent, session, "image", source)).rejects.toThrow("Unsupported");
  expect(session.fetch).not.toHaveBeenCalled();
  expect(dialog.showSaveDialog).not.toHaveBeenCalled();
});
