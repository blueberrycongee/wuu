// Sent-message pasted-text design draft. The "current" section mounts the
// production ThreadItemView; option sections reuse production classes where
// they exist and add fixture-scoped .mpt-* rules for the proposed markup.
import { useMemo, useState, type ReactNode } from "react";
import { createRoot } from "react-dom/client";
import { ArtifactThreadContext } from "../../src/renderer/ArtifactPreviewContext";
import { fileNameParts, formatFileSize } from "../../src/renderer/AttachmentFormat";
import { imageSource } from "../../src/renderer/ComposerMessages";
import { ConversationControlsTestProvider } from "../../src/renderer/ConversationControlsTestProvider";
import { I18nProvider, useI18n } from "../../src/renderer/i18n";
import { ImagePreviewProvider } from "../../src/renderer/ImagePreview";
import { applyMessageFlowFontSize } from "../../src/renderer/MessageFlowFontSizeSection";
import { MessageFileList, MessageImageGrid } from "../../src/renderer/MessageActions";
import { startFocusModality } from "../../src/renderer/FocusModality";
import { ThreadItemView } from "../../src/renderer/ThreadItemView";
import { ToastViewport } from "../../src/renderer/Toast";
import { WuuUIRoot } from "../../src/renderer/ui/layers/UILayerHost";
import { ChevronDown, ChevronUp, FileText } from "../../src/renderer/WuuIcons";
import type { InputFile, InputImage, MessageContentPart, ThreadItem } from "../../src/shared/protocol";
import type { UICompositionV1 } from "../../../packages/plugin-sdk/src/ui";
import "../../src/renderer/styles.css";
import "./fixture.css";

const params = new URLSearchParams(location.search);
document.documentElement.dataset.theme = params.get("theme") || "light";
if (params.get("style") === "im") document.documentElement.dataset.messageStyle = "im";
startFocusModality();
applyMessageFlowFontSize(Number(params.get("size")) || 15);

const slots: UICompositionV1 = { Slot: ({ fallback }) => fallback ?? null };
const noop = () => {};

/* ———————— Mock data ———————— */

const PASTE_DOC = `背景：Wuu 桌面端 canvas 类 shell skin 会把网点图（dot screen tone）铺在整个工作台底层，不同主题只是换色板。
现状：输入框里的长文本折叠卡是 240px 文档卡——文件图标、伪装成 *.txt 的第一行、行数元信息，右侧留出大片空白。
问题：第一行被当成了文件名，截断后信息密度低；折叠粘贴与图片、文件卡片也不像一家人。
目标：让折叠粘贴在发送前后保持同一种视觉语言——“卡片即页面”。
方案：方块页面缩略图直接展示开头几行；悬停出现移除角标；点击重新展开回文本框。
保留：reveal / remove 交互不变，标题只进 aria-label，不再渲染成文件名。
验证：light / dark 主题、15px / 20px 字号、窄窗口、与图片和文件混排。`;

const PASTE_LOG = `22:41:03 turn.start thread=wuu-dev mode=full-access
22:41:03 composer.submit parts=2 pasted_text=1 text=1
22:41:04 input.persist working-copy=input-attachments/text-1762….txt bytes=8,412
22:41:05 engine.attach codex profile=default model=gpt-5
22:41:05 turn.plan items=6 tools=["read","grep","edit","exec"]
22:41:08 tool.read ok path=desktop/src/renderer/ThreadItemView.tsx lines=989
22:41:11 tool.grep ok pattern="user-message-pasted-text" hits=15
22:41:14 edit.prepare file=styles/turns.css hunks=2
22:41:15 turn.waiting reason=user-input`;

const PASTE_TITLED = `Submitted artifact context (JSON):
{
  "threadId": "9f2c81",
  "turnId": "turn-41",
  "itemId": "final-1",
  "artifactId": "artifact:2",
  "title": "发布检查清单",
  "values": { "channel": "stable", "notes": "跳过夜间窗口" }
}`;

type PasteSpec = { text: string; title?: string; expanded?: boolean };
type ImageSpec = { image: InputImage; src: string };
type ScenarioSpec = {
  key: string;
  label: string;
  pastes: PasteSpec[];
  text?: string;
  images?: ImageSpec[];
  files?: InputFile[];
};

function sampleImage(seed: number): ImageSpec {
  const canvas = document.createElement("canvas");
  canvas.width = 960;
  canvas.height = 720;
  const ctx = canvas.getContext("2d")!;
  const gradient = ctx.createLinearGradient(0, 0, 960, 720);
  gradient.addColorStop(0, ["#dfe7e2", "#e8dfd2", "#dfe2ea"][seed % 3]);
  gradient.addColorStop(1, ["#8fa5b8", "#b08c6d", "#7d8494"][seed % 3]);
  ctx.fillStyle = gradient;
  ctx.fillRect(0, 0, 960, 720);
  ctx.fillStyle = "rgba(255,255,255,0.8)";
  for (let row = 0; row < 7; row += 1) ctx.fillRect(80, 90 + row * 78, 480 + (row % 3) * 110, 34);
  ctx.fillStyle = "#3d5a73";
  ctx.beginPath();
  ctx.arc(760, 210, 96, 0, Math.PI * 2);
  ctx.fill();
  const src = canvas.toDataURL("image/png");
  return { image: { media_type: "image/png", data: src.slice(src.indexOf(",") + 1), width: 960, height: 720 }, src };
}

const IMAGES = [sampleImage(0), sampleImage(1)];
const FILES: InputFile[] = [
  { filename: "接口约定 v3.pdf", media_type: "application/pdf", data: btoa("wuu fixture pdf ".repeat(900)) },
];

const SCENARIOS: ScenarioSpec[] = [
  { key: "only", label: "只有粘贴文本", pastes: [{ text: PASTE_DOC }] },
  { key: "text", label: "粘贴 + 一句正文", pastes: [{ text: PASTE_DOC }], text: "按这个思路把消息侧也改掉" },
  { key: "mixed", label: "粘贴 + 图片 × 2 + 文件 + 正文", pastes: [{ text: PASTE_DOC }], text: "素材都在这，对比一下", images: IMAGES, files: FILES },
  { key: "two", label: "两段粘贴 + 正文", pastes: [{ text: PASTE_DOC }, { text: PASTE_LOG }], text: "上面是背景，下面是日志" },
  { key: "titled", label: "带标题的粘贴（插件 submitContent）", pastes: [{ text: PASTE_TITLED, title: "Interactive response" }], text: "帮我处理这个回复" },
  { key: "open", label: "展开态（点击卡片试试）", pastes: [{ text: PASTE_DOC, expanded: true }], text: "按这个思路把消息侧也改了" },
];

/* ———————— Shared helpers ———————— */

function firstLine(text: string): string {
  return text.split(/\r\n|\r|\n/).map((line) => line.trim()).find(Boolean) ?? "";
}

function previewLines(text: string, count: number): { lines: string[]; truncated: boolean } {
  const all = text.trimStart().split(/\r\n|\r|\n/);
  return { lines: all.slice(0, count), truncated: all.length > count };
}

function useOpenSet(pastes: PasteSpec[]): [Set<number>, (index: number) => void] {
  const [open, setOpen] = useState<Set<number>>(() => new Set(pastes.flatMap((p, index) => (p.expanded ? [index] : []))));
  const toggle = (index: number): void =>
    setOpen((prev) => {
      const next = new Set(prev);
      if (next.has(index)) next.delete(index);
      else next.add(index);
      return next;
    });
  return [open, toggle];
}

function MockMessage({ scenario, attachments, bubble }: { scenario: ScenarioSpec; attachments?: ReactNode; bubble?: ReactNode }) {
  return (
    <div className="fixture-turn">
      <div className="user-message-block" data-wuu-component="message" data-wuu-variant="user">
        <div className="user-message-motion" data-message-arrival>
          {attachments ? (
            <div className="user-message-attachments" data-wuu-component="message-attachments">{attachments}</div>
          ) : null}
          {bubble !== undefined && bubble !== "" ? (
            <div className="message user-message" data-wuu-component="message-bubble" data-wuu-variant="user">{bubble}</div>
          ) : null}
        </div>
      </div>
    </div>
  );
}

function FileChip({ file }: { file: InputFile }) {
  const { stem, extension } = fileNameParts(file.filename?.trim() || "file");
  const kind = extension ? extension.slice(1).toUpperCase() : file.media_type.split("/").pop()?.toUpperCase();
  return (
    <div className="message-file">
      <FileText className="icon" aria-hidden="true" />
      <span className="message-file-details">
        <span className="message-file-name">
          <span className="message-file-name-stem">{stem}</span>
          {extension ? <span className="message-file-name-extension">{extension}</span> : null}
        </span>
        <span className="message-file-meta">{kind ? `${kind} · ` : ""}{formatFileSize(file.data)}</span>
      </span>
    </div>
  );
}

/** Production expanded card, with the raw first line instead of the fake *.txt name. */
function PasteExpandedCard({ label, text, onCollapse }: { label: string; text: string; onCollapse: () => void }) {
  return (
    <section className="user-message-pasted-text expanded">
      <button type="button" className="user-message-pasted-text-toggle" aria-expanded="true" onClick={onCollapse}>
        <span className="user-message-pasted-text-icon" aria-hidden="true"><FileText /></span>
        <strong className="user-message-pasted-text-title">{label}</strong>
        <ChevronUp aria-hidden="true" />
      </button>
      <pre className="user-message-pasted-text-content">{text}</pre>
    </section>
  );
}

/* ———————— Current: real ThreadItemView ———————— */

function CurrentMessage({ s }: { s: ScenarioSpec }) {
  const item = useMemo<ThreadItem>(() => {
    const parts: MessageContentPart[] = [
      ...s.pastes.map((p): MessageContentPart => (p.title
        ? { type: "pasted_text", text: p.text, title: p.title }
        : { type: "pasted_text", text: p.text })),
      ...(s.text ? [{ type: "text" as const, text: s.text }] : []),
    ];
    return {
      id: `fixture-${s.key}`,
      type: "user_message",
      status: "completed",
      text: parts.map((part) => part.text).join(""),
      content_parts: parts,
      images: s.images?.map((entry) => entry.image),
      files: s.files,
    };
  }, [s]);
  return (
    <div className="fixture-turn">
      <ArtifactThreadContext.Provider value="fixture-thread">
        <ThreadItemView turnID={`turn-${s.key}`} turnStatus="completed" item={item} streaming={false} onStreamFrame={noop} />
      </ArtifactThreadContext.Provider>
    </div>
  );
}

/* ———————— Option A: compact chip ———————— */

function OptionA({ s }: { s: ScenarioSpec }) {
  const { t } = useI18n();
  const [open, toggle] = useOpenSet(s.pastes);
  const collapsed = s.pastes.map((p, index) => ({ p, index })).filter(({ index }) => !open.has(index));
  const expanded = s.pastes.map((p, index) => ({ p, index })).filter(({ index }) => open.has(index));
  return (
    <MockMessage
      scenario={s}
      attachments={<>
        {s.images?.length ? <MessageImageGrid images={s.images.map((entry) => entry.image)} collapsedLimit={4} /> : null}
        {(s.files?.length || collapsed.length) ? (
          <div className="mpt-chip-row">
            {s.files?.map((file, index) => <FileChip key={`f-${index}`} file={file} />)}
            {collapsed.map(({ p, index }) => (
              <button key={`p-${index}`} type="button" className="mpt-chip" title={p.title || firstLine(p.text)} onClick={() => toggle(index)}>
                <FileText className="mpt-chip-icon" aria-hidden="true" />
                <span className="mpt-chip-label">{p.title || firstLine(p.text) || t("composer.longText")}</span>
                <ChevronDown className="mpt-chip-chevron" aria-hidden="true" />
              </button>
            ))}
          </div>
        ) : null}
        {expanded.map(({ p, index }) => (
          <PasteExpandedCard key={`x-${index}`} label={p.title || firstLine(p.text) || t("composer.longText")} text={p.text} onCollapse={() => toggle(index)} />
        ))}
      </>}
      bubble={s.text}
    />
  );
}

/* ———————— Option B: page preview card ———————— */

function OptionB({ s }: { s: ScenarioSpec }) {
  const [open, toggle] = useOpenSet(s.pastes);
  return (
    <MockMessage
      scenario={s}
      attachments={<>
        {s.images?.length ? <MessageImageGrid images={s.images.map((entry) => entry.image)} collapsedLimit={4} /> : null}
        {s.files?.length ? <MessageFileList files={s.files} collapsedLimit={3} /> : null}
        {s.pastes.map((p, index) => {
          const expanded = open.has(index);
          const { lines, truncated } = previewLines(p.text, 4);
          return (
            <section key={index} className={`mpt-page${expanded ? " expanded" : ""}`}>
              <button type="button" className="mpt-page-main" aria-expanded={expanded} onClick={() => toggle(index)}>
                {p.title ? <strong className="mpt-page-name">{p.title}</strong> : null}
                {expanded
                  ? (p.title ? null : <span className="mpt-page-firstline">{firstLine(p.text)}</span>)
                  : (
                    <span className={`mpt-page-preview${truncated ? " mpt-page-faded" : ""}`} aria-hidden="true">
                      {lines.map((line, lineIndex) => <span key={lineIndex}>{line || " "}</span>)}
                    </span>
                  )}
                <span className="mpt-page-chevron" aria-hidden="true">{expanded ? <ChevronUp /> : <ChevronDown />}</span>
              </button>
              {expanded ? <pre className="user-message-pasted-text-content">{p.text}</pre> : null}
            </section>
          );
        })}
      </>}
      bubble={s.text}
    />
  );
}

/* ———————— Option C: in-bubble quote block ———————— */

function OptionC({ s }: { s: ScenarioSpec }) {
  const { t } = useI18n();
  const [open, toggle] = useOpenSet(s.pastes);
  const hasMedia = Boolean(s.images?.length || s.files?.length);
  return (
    <MockMessage
      scenario={s}
      attachments={hasMedia ? (<>
        {s.images?.length ? <MessageImageGrid images={s.images.map((entry) => entry.image)} collapsedLimit={4} /> : null}
        {s.files?.length ? <MessageFileList files={s.files} collapsedLimit={3} /> : null}
      </>) : undefined}
      bubble={<>
        {s.pastes.map((p, index) => {
          const expanded = open.has(index);
          const { truncated } = previewLines(p.text, 4);
          return (
            <div key={index} className="mpt-quote">
              {p.title ? <div className="mpt-quote-name">{p.title}</div> : null}
              <div className={`mpt-quote-text${expanded ? " expanded" : ""}`}>{p.text}</div>
              {truncated || expanded ? (
                <button type="button" className="user-message-expand-toggle" aria-expanded={expanded} onClick={() => toggle(index)}>
                  <span>{expanded ? t("common.collapse") : t("common.showMore")}</span>
                  {expanded ? <ChevronUp aria-hidden="true" /> : <ChevronDown aria-hidden="true" />}
                </button>
              ) : null}
            </div>
          );
        })}
        {s.text}
      </>}
    />
  );
}

/* ———————— Option D: square page thumbnail in the media row ———————— */

function PasteThumb({ spec, index, open, toggle, lines, className }: {
  spec: PasteSpec; index: number; open: boolean; toggle: (index: number) => void; lines: number; className: string;
}) {
  return (
    <button type="button" className={className} title={spec.title || firstLine(spec.text)}
      aria-expanded={open} onClick={() => toggle(index)}>
      <span className="mpt-thumb-lines" aria-hidden="true">
        {previewLines(spec.text, lines).lines.map((line, lineIndex) => <span key={lineIndex}>{line || " "}</span>)}
      </span>
    </button>
  );
}

function OptionD({ s }: { s: ScenarioSpec }) {
  const { t } = useI18n();
  const [open, toggle] = useOpenSet(s.pastes);
  return (
    <MockMessage
      scenario={s}
      attachments={<>
        <div className="mpt-media-row">
          {s.images?.map((entry, index) => (
            <div className="message-image-frame" key={`i-${index}`}><img className="message-image" src={entry.src} alt="" /></div>
          ))}
          {s.pastes.map((p, index) => (
            <PasteThumb key={`p-${index}`} spec={p} index={index} open={open.has(index)} toggle={toggle} lines={9} className="mpt-thumb" />
          ))}
        </div>
        {s.files?.length ? <MessageFileList files={s.files} collapsedLimit={3} /> : null}
        {s.pastes.map((p, index) => (open.has(index)
          ? <PasteExpandedCard key={`x-${index}`} label={p.title || firstLine(p.text) || t("composer.longText")} text={p.text} onCollapse={() => toggle(index)} />
          : null))}
      </>}
      bubble={s.text}
    />
  );
}

/* ———— Option D+: one standardized 72px media row (image / page / file) ———— */

function FileTile({ file }: { file: InputFile }) {
  const { stem, extension } = fileNameParts(file.filename?.trim() || "file");
  const kind = extension ? extension.slice(1).toUpperCase() : file.media_type.split("/").pop()?.toUpperCase();
  return (
    <div className="mpt-file-tile">
      <FileText className="mpt-file-tile-icon" aria-hidden="true" />
      <span className="message-file-details">
        <span className="message-file-name">
          <span className="message-file-name-stem">{stem}</span>
          {extension ? <span className="message-file-name-extension">{extension}</span> : null}
        </span>
        <span className="message-file-meta">{kind ? `${kind} · ` : ""}{formatFileSize(file.data)}</span>
      </span>
    </div>
  );
}

function OptionDPlus({ s, tile }: { s: ScenarioSpec; tile?: (file: InputFile, index: number) => ReactNode }) {
  const { t } = useI18n();
  const [open, toggle] = useOpenSet(s.pastes);
  return (
    <MockMessage
      scenario={s}
      attachments={<>
        <div className="mpt-media-row">
          {s.images?.map((entry, index) => (
            <div className="message-image-frame" key={`i-${index}`}><img className="message-image" src={entry.src} alt="" /></div>
          ))}
          {s.pastes.map((p, index) => (
            <PasteThumb key={`p-${index}`} spec={p} index={index} open={open.has(index)} toggle={toggle} lines={9} className="mpt-thumb mpt-thumb-page" />
          ))}
          {s.files?.map((file, index) => tile ? tile(file, index) : <FileTile key={`f-${index}`} file={file} />)}
        </div>
        {s.pastes.map((p, index) => (open.has(index)
          ? <PasteExpandedCard key={`x-${index}`} label={p.title || firstLine(p.text) || t("composer.longText")} text={p.text} onCollapse={() => toggle(index)} />
          : null))}
      </>}
      bubble={s.text}
    />
  );
}

/* ———— File tile variants for the D+ media row ———— */

function fileParts(file: InputFile): { stem: string; extension: string; kind: string } {
  const { stem, extension } = fileNameParts(file.filename?.trim() || "file");
  const kind = (extension ? extension.slice(1) : file.media_type.split("/").pop() || "").toUpperCase();
  return { stem, extension, kind };
}

/** F1 · 横向卡微调：图标缩到 16px 字重降一档，宽度随内容弹性收缩。 */
function FileTileHorizontal({ file }: { file: InputFile }) {
  const { stem, extension, kind } = fileParts(file);
  return (
    <div className="mpt-file-tile mpt-file-h">
      <FileText className="mpt-file-tile-icon" aria-hidden="true" />
      <span className="message-file-details">
        <span className="message-file-name">
          <span className="message-file-name-stem">{stem}</span>
          {extension ? <span className="message-file-name-extension">{extension}</span> : null}
        </span>
        <span className="message-file-meta">{kind ? `${kind} · ` : ""}{formatFileSize(file.data)}</span>
      </span>
    </div>
  );
}

/** F2 · 竖版文档卡：上面一行是居中类型徽标，底部是文件名 + 大小两行。 */
function FileTileVertical({ file }: { file: InputFile }) {
  const { stem, extension, kind } = fileParts(file);
  return (
    <div className="mpt-file-tile mpt-file-v">
      <span className="mpt-file-v-badge" aria-hidden="true">{kind.slice(0, 4)}</span>
      <span className="mpt-file-v-name">
        <span className="mpt-file-v-stem">{stem}</span>{extension}
      </span>
      <span className="mpt-file-v-meta">{formatFileSize(file.data)}</span>
    </div>
  );
}

/** F3 · 页面剪影：折角页 + 文件名小字三行截断（与生产 .message-files-tile 一致）。 */
function FileTilePage({ file }: { file: InputFile }) {
  const { stem, extension } = fileParts(file);
  return (
    <div className="mpt-file-tile mpt-file-p" title={file.filename}>
      <span className="mpt-file-p-name">
        <span>{stem}</span>{extension}
      </span>
    </div>
  );
}

const FILE_VARIANTS: { id: string; name: string; render: (file: InputFile, index: number) => ReactNode }[] = [
  { id: "f1", name: "F1 · 横向卡（图标 + 两行）", render: (file, index) => <FileTileHorizontal key={index} file={file} /> },
  { id: "f2", name: "F2 · 竖版文档卡（徽标 + 名称底条）", render: (file, index) => <FileTileVertical key={index} file={file} /> },
  { id: "f3", name: "F3 · 页面剪影（文件名三行）", render: (file, index) => <FileTilePage key={index} file={file} /> },
];

const LONG_FILES: InputFile[] = [
  { filename: "2026-冬季-release-检查清单-final-v3.pdf", media_type: "application/pdf", data: btoa("wuu fixture pdf ".repeat(900)) },
];

const MULTI_FILES: InputFile[] = [
  { filename: "接口约定 v3.pdf", media_type: "application/pdf", data: btoa("wuu fixture pdf ".repeat(900)) },
  { filename: "screenshot-notes.txt", media_type: "text/plain", data: btoa("wuu fixture txt ".repeat(200)) },
  { filename: "archive.zip", media_type: "application/zip", data: btoa("wuu fixture zip ".repeat(500)) },
];

const FILE_SCENARIOS: ScenarioSpec[] = [
  { key: "mixed", label: "粘贴 + 图片 × 2 + 文件 + 正文", pastes: [{ text: PASTE_DOC }], text: "素材都在这，对比一下", images: IMAGES, files: FILES },
  { key: "long", label: "长文件名", pastes: [{ text: PASTE_DOC }], text: "看下截断", files: LONG_FILES },
  { key: "multi", label: "多文件混排", pastes: [{ text: PASTE_DOC }], images: [IMAGES[0]], files: MULTI_FILES },
];

/* ———————— Page ———————— */

const OPTIONS: { id: string; name: string; desc: string; scenarios?: ScenarioSpec[]; render: (s: ScenarioSpec) => ReactNode }[] = [
  {
    id: "current",
    name: "现状 · 附件行卡片",
    desc: "当前实现（真实 ThreadItemView）：48px 整宽行，28px FileText 图标块 + 伪装成 *.txt 的首行标题 + chevron；点击展开全文 <pre>。问题：假文件名、一行信息量、右侧大片空白，与输入框的页面缩略图完全两种语言。",
    render: (s) => <CurrentMessage s={s} />,
  },
  {
    id: "opt-a",
    name: "A · 紧凑 chip",
    desc: "和文件 chip 同族的小胶囊，宽度随内容收缩（上限 320px），与文件、图片同排右对齐；点击后展开为整宽文本卡。最省空间，但折叠态仍只有一行信息。",
    render: (s) => <OptionA s={s} />,
  },
  {
    id: "opt-b",
    name: "B · 页面预览卡",
    desc: "「卡片即页面」：折叠态直接展示粘贴内容的前 4 行（底部渐隐 + 右下 chevron），一眼能认出贴的是什么；展开态首行变标题、正文滚动。信息密度最高，与输入框「看到内容」的语言一致。",
    render: (s) => <OptionB s={s} />,
  },
  {
    id: "opt-c",
    name: "C · 气泡内引用块",
    desc: "粘贴内容回到消息气泡里，作为 inset 引用块（左侧竖条 + 浅底），超 4 行给「显示更多」。最接近「发送了什么就看到什么」，但附件感最弱，多段粘贴 + 正文时全部挤在一个气泡里。",
    render: (s) => <OptionC s={s} />,
  },
  {
    id: "opt-d",
    name: "D · 页面缩略图",
    desc: "和输入框同款的页面缩略图（竖版 74×96），与图片一起排进媒体行；点击后在下方展开整宽文本卡。视觉家族最统一，代价是缩略图不可读、只能做识别锚点。",
    render: (s) => <OptionD s={s} />,
  },
  {
    id: "opt-d2",
    name: "D+ · 统一 72px 媒体行",
    desc: "在 D 基础上把文件也收进媒体行：行高统一 72px——图片 96×72 不变、粘贴 = 56×72 竖版页面、文件 = 同高定宽文档卡（图标 + 两行文字）。三类附件共用边框/圆角/阴影语言，一行排齐。",
    render: (s) => <OptionDPlus s={s} />,
  },
  ...FILE_VARIANTS.map((variant) => ({
    id: `opt-${variant.id}`,
    name: variant.name,
    desc: "D+ 媒体行下文件卡的打磨变体：同一套 72px 行高、2px 边框和圆角阴影，区别在内部布局——横向图标文字 / 竖版徽标名称 / 页面剪影。",
    scenarios: FILE_SCENARIOS,
    render: (s: ScenarioSpec) => <OptionDPlus s={s} tile={variant.render} />,
  })),
];

function paramLink(key: string, value: string | null): string {
  const next = new URLSearchParams(location.search);
  if (value === null) next.delete(key);
  else next.set(key, value);
  const query = next.toString();
  return query ? `?${query}` : "?";
}

function Fixture(): JSX.Element {
  const dark = document.documentElement.dataset.theme === "dark";
  const im = params.get("style") === "im";
  const narrow = params.get("width") === "560";
  const only = params.get("only");
  const sections = only ? OPTIONS.filter((option) => option.id === only) : OPTIONS;
  const scenes = params.get("scene") ? SCENARIOS.filter((s) => s.key === params.get("scene")) : SCENARIOS;
  return (
    <>
      <header className="fixture-top">
        <strong className="fixture-title">Pasted text · 已发送消息</strong>
        <nav>
          {OPTIONS.map((option) => <a key={option.id} href={`#${option.id}`}>{option.name}</a>)}
        </nav>
        <span className="fixture-spacer" />
        <a href={paramLink("theme", dark ? "light" : "dark")}>{dark ? "切换 light" : "切换 dark"}</a>
        <a href={paramLink("style", im ? null : "im")}>{im ? "默认消息样式" : "im 消息样式"}</a>
        <a href={paramLink("width", narrow ? null : "560")}>{narrow ? "正常宽度" : "窄窗口 560"}</a>
      </header>
      <main className="fixture-body">
        {sections.map((option) => (
          <section key={option.id} id={option.id} className="fixture-section" style={{ ["--fixture-w" as string]: `${Number(params.get("width")) || 920}px` }}>
            <h2>{option.name}</h2>
            <p className="fixture-desc">{option.desc}</p>
            {(option.scenarios ?? scenes).map((s) => (
              <div key={s.key} className="fixture-scenario">
                <p className="fixture-label">{s.label}</p>
                <div className="fixture-column">{option.render(s)}</div>
              </div>
            ))}
          </section>
        ))}
      </main>
    </>
  );
}

createRoot(document.getElementById("root")!).render(
  <I18nProvider>
    <WuuUIRoot>
      <ConversationControlsTestProvider composition={slots}>
        <ImagePreviewProvider>
          <Fixture />
          <ToastViewport />
        </ImagePreviewProvider>
      </ConversationControlsTestProvider>
    </WuuUIRoot>
  </I18nProvider>,
);
