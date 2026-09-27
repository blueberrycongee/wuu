// Production Composer with synthetic attachments; no product bridge or user data.
import { useEffect, useRef } from "react";
import { createRoot } from "react-dom/client";
import { Composer, type CodexModelLoadState } from "../../src/renderer/ComposerView";
import { useComposerDraftState } from "../../src/renderer/ComposerDraftState";
import type { QueuedComposerMessage } from "../../src/renderer/ComposerMessages";
import { ImagePreviewProvider } from "../../src/renderer/ImagePreview";
import { I18nProvider } from "../../src/renderer/i18n";
import { ToastViewport } from "../../src/renderer/Toast";
import { applyMessageFlowFontSize } from "../../src/renderer/MessageFlowFontSizeSection";
import { startFocusModality } from "../../src/renderer/FocusModality";
import { WuuUIRoot } from "../../src/renderer/ui/layers/UILayerHost";
import "../../src/renderer/styles.css";
import "./fixture.css";

const params = new URLSearchParams(location.search);
document.documentElement.dataset.theme = params.get("theme") || "light";
startFocusModality();
applyMessageFlowFontSize(Number(params.get("size")) || 14);

const EMPTY_MODELS: CodexModelLoadState = { loading: false, error: "", models: [] };
const noop = () => {};
const queued: QueuedComposerMessage[] = params.has("queued")
  ? [{ id: "queued-1", text: "等这一轮结束后，再把导出的截图整理成对比表。", images: [], files: [] }]
  : [];

function canvasFile(name: string, width: number, height: number): Promise<File> {
  const canvas = document.createElement("canvas");
  canvas.width = width;
  canvas.height = height;
  const context = canvas.getContext("2d")!;
  const gradient = context.createLinearGradient(0, 0, width, height);
  gradient.addColorStop(0, "#dfe7e2");
  gradient.addColorStop(1, "#8fa5b8");
  context.fillStyle = gradient;
  context.fillRect(0, 0, width, height);
  context.fillStyle = "rgba(255,255,255,0.86)";
  for (let row = 0; row < 7; row += 1) {
    context.fillRect(width * 0.08, height * (0.12 + row * 0.11), width * (0.5 + (row % 3) * 0.12), height * 0.05);
  }
  context.fillStyle = "#3d5a73";
  context.beginPath();
  context.arc(width * 0.8, height * 0.3, height * 0.14, 0, Math.PI * 2);
  context.fill();
  return new Promise((resolve) => canvas.toBlob((blob) => resolve(new File([blob!], name, { type: "image/png" })), "image/png"));
}

function pdfFile(): File {
  const body = "%PDF-1.4\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj\n3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 612 792]>>endobj\ntrailer<</Root 1 0 R>>\n%%EOF\n";
  return new File([body.repeat(900)], "2026 年第三季度设计评审纪要（终版）.pdf", { type: "application/pdf" });
}

async function videoFile(): Promise<File> {
  const canvas = document.createElement("canvas");
  canvas.width = 640;
  canvas.height = 360;
  const context = canvas.getContext("2d")!;
  const recorder = new MediaRecorder(canvas.captureStream(30), { mimeType: "video/webm" });
  const chunks: Blob[] = [];
  recorder.ondataavailable = (event) => chunks.push(event.data);
  const stopped = new Promise<void>((resolve) => { recorder.onstop = () => resolve(); });
  recorder.start();
  const started = performance.now();
  await new Promise<void>((resolve) => {
    const draw = (now: number) => {
      const progress = (now - started) / 1400;
      context.fillStyle = "#1f2a33";
      context.fillRect(0, 0, 640, 360);
      context.fillStyle = "#e9b872";
      context.beginPath();
      context.arc(80 + progress * 480, 180, 56, 0, Math.PI * 2);
      context.fill();
      if (progress < 1) requestAnimationFrame(draw);
      else resolve();
    };
    requestAnimationFrame(draw);
  });
  recorder.stop();
  await stopped;
  return new File(chunks, "屏幕录制.webm", { type: "video/webm" });
}

function longText(): string {
  const lines = ["# 交接说明：输入框附件托盘", ""];
  for (let index = 1; index <= 86; index += 1) {
    lines.push(`${index}. 这一行用于模拟从文档里粘贴的大段文字，检查折叠后的卡片信息与动画是否清楚。`);
  }
  return lines.join("\n");
}

function paste(data: DataTransfer): void {
  const textarea = document.querySelector<HTMLTextAreaElement>(".composer textarea");
  if (!textarea) return;
  textarea.focus();
  textarea.dispatchEvent(new ClipboardEvent("paste", { clipboardData: data, bubbles: true, cancelable: true }));
}

function pasteFiles(files: File[]): void {
  const data = new DataTransfer();
  for (const file of files) data.items.add(file);
  paste(data);
}

function pasteText(text: string): void {
  const data = new DataTransfer();
  data.setData("text/plain", text);
  paste(data);
}

// Synthetic files take a moment to generate (the video records for 1.4s), so
// actions run in click order instead of racing each other.
let queue: Promise<void> = Promise.resolve();
function run(task: () => Promise<void> | void): Promise<void> {
  queue = queue.then(task, task);
  return queue;
}
Object.assign(window, { fixtureIdle: () => queue });

function Fixture(): JSX.Element {
  const draft = useComposerDraftState();
  const seeded = useRef(false);
  const hero = params.has("hero");

  useEffect(() => {
    if (!params.has("seed") || seeded.current) return;
    seeded.current = true;
    void run(async () => {
      pasteFiles([await canvasFile("screenshot.png", 2880, 1800)]);
      pasteFiles([pdfFile()]);
      pasteFiles([await videoFile()]);
      pasteText(longText());
    });
  }, []);

  return (
    <div className="fixture-shell" style={{ ["--fixture-width" as string]: `${Number(params.get("width")) || 760}px` }}>
      <div className="fixture-controls">
        <button onClick={() => void run(async () => pasteFiles([await canvasFile("screenshot.png", 2880, 1800)]))}>粘贴截图</button>
        <button onClick={() => void run(async () => pasteFiles(await Promise.all([
          canvasFile("a.png", 1200, 900), canvasFile("b.png", 900, 1200), canvasFile("c.png", 1600, 900),
        ])))}>粘贴三张图</button>
        <button onClick={() => void run(() => pasteFiles([pdfFile()]))}>粘贴 PDF</button>
        <button onClick={() => void run(async () => pasteFiles([await videoFile()]))}>粘贴视频</button>
        <button onClick={() => void run(() => pasteText(longText()))}>粘贴长文本</button>
        <button onClick={() => void run(() => { draft.setPrompt(""); draft.setComposerImages([]); draft.setComposerFiles([]); })}>模拟发送</button>
        <button onClick={() => pasteFiles([new File(["unsupported"], "archive.zip", { type: "application/zip" })])}>粘贴不支持的附件</button>
      </div>
      <main className={`fixture-pane${hero ? " fixture-pane-hero" : ""}`}>
        {hero ? null : (
          <div className="fixture-history">
            {Array.from({ length: 5 }, (_, index) => (
              <p key={index}>第 {index + 1} 段：示例对话内容，用来观察托盘浮在历史消息上方时的层次与遮挡。托盘展开只改变一次布局，之后的位移都在合成层上完成，输入框本身保持不动。</p>
            ))}
          </div>
        )}
        <Composer
          variant={hero ? "hero" : "dock"}
          prompt={draft.prompt}
          promptRevision={draft.promptRevision}
          setPrompt={draft.setPromptFromInput}
          files={draft.composerFiles}
          images={draft.composerImages}
          queuedMessages={queued}
          guideMessages={[]}
          running={false}
          runtimeControlsDisabled
          tokensPerSecond={0}
          status=""
          statusLiveProgress={false}
          readOnly={false}
          projects={[]}
          codexModels={EMPTY_MODELS}
          codexRuntimeMenu={null}
          codexRuntimeRef={{ current: null }}
          menuOpen={false}
          accessMenuOpen={false}
          branchMenuOpen={false}
          menuRef={{ current: null }}
          accessMenuRef={{ current: null }}
          workspaceFilter=""
          setWorkspaceFilter={noop}
          onToggleMenu={noop}
          onToggleAccessMenu={noop}
          onToggleBranchMenu={noop}
          onToggleCodexRuntimeMenu={noop}
          onSelectRuntimeModel={noop}
          onSelectRuntimeEffort={noop}
          onSelectPermissionMode={noop}
          onOpenSettings={noop}
          onOpenSkillsCatalog={noop}
          onSelectWorkspace={noop}
          onSelectNoProject={noop}
          onSelectGitBranch={noop}
          onCreateWorkspace={noop}
          onOpenWorkspace={noop}
          onStartNewThread={noop}
          onOpenWorkspaceTool={noop}
          onOpenInstructions={noop}
          onPasteAttachmentFiles={(files) => void draft.attachComposerAttachmentFiles(files)}
          onRemoveFile={draft.removeComposerFile}
          onRemoveImage={draft.removeComposerImage}
          onRemoveQueuedMessage={noop}
          onRemoveGuideMessage={noop}
          onGuideQueuedMessage={noop}
          onEditQueuedMessage={noop}
          onEditGuideMessage={noop}
          onSend={() => { draft.setPrompt(""); draft.setComposerImages([]); draft.setComposerFiles([]); }}
          onInterrupt={noop}
        />
      </main>
    </div>
  );
}

createRoot(document.getElementById("root")!).render(
  <I18nProvider><WuuUIRoot><ImagePreviewProvider><Fixture /></ImagePreviewProvider><ToastViewport /></WuuUIRoot></I18nProvider>,
);
