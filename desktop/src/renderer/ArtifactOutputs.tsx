import { useArtifacts } from "./ArtifactsContext";
import type { ApplicationArtifactsV1 } from "../../../packages/plugin-sdk/src/ui-artifacts";
import { lazy, Suspense, useContext, useEffect, useLayoutEffect, useMemo, useRef, useState, type MouseEvent, type ReactNode } from "react";
import { Archive, ChevronRight, Download, Ellipsis, FileDiff, FileText, Film, Images, LayoutGrid, X } from "./WuuIcons";

import type { ThreadItem, ToolResultContentPart, Turn } from "../shared/protocol";
import { useImagePreview } from "./ImagePreview";
import { useImagePreviewRegistration } from "./ImagePreviewGallery";
import { useI18n } from "./i18n";
import { useConversationControls, useConversationTurnIdentity } from "./ConversationControlsContext";
import { RichContent } from "./RichContent";
import { isMarkdownDocument, MarkdownDocumentReading } from "./MarkdownDocumentReading";
import { AttachmentImage } from "./AttachmentImage";
import { fileNameParts, formatByteSize } from "./AttachmentFormat";
import { ProcessSurfaceFold } from "./ProcessSurfaceFold";
import {
  TURN_OUTPUT_SUMMARY_BATCH_SIZE,
  TurnOutputSummaryCard,
  TurnOutputSummaryChevron,
  TurnOutputSummaryMore,
  TurnOutputSummaryPresentation,
  turnOutputSummaryVisible,
} from "./TurnOutputSummaryCard";
import { Tooltip } from "./Tooltip";
import { ThreadContextMenu } from "./ThreadContextMenu";
import { showErrorToast } from "./Toast";
import { TruncatedText } from "./TruncatedText";
import { ArtifactPreviewContext, ArtifactThreadContext, restoreArtifactPreviewFocus } from "./ArtifactPreviewContext";
import { VideoPreview } from "./VideoPreview";
import { videoMimeType } from "../shared/videoMimeType";

const WorkspacePdfPreview = lazy(async () => ({
  default: (await import("./WorkspacePdfPreview")).WorkspacePdfPreview,
}));

export type TurnArtifact = Readonly<{
  id: string;
  itemId: string;
  index: number;
  type: ToolResultContentPart["type"];
  name: string;
  mimeType: string;
  data?: string;
  remoteRef?: string;
  text?: string;
  foldText?: boolean;
  foldPreview?: boolean;
  uri?: string;
  resource?: unknown;
  placement: "inline" | "turn_end";
  ref?: string;
  sha256?: string;
  sizeBytes?: number;
  delivered?: boolean;
}>;

export function collectTurnArtifacts(turn: Pick<Turn, "items">): readonly TurnArtifact[] {
  const artifacts: TurnArtifact[] = [];
  const presented = new Map<string, string>();
  for (const item of turn.items) {
    if (item.type !== "tool_call" || !item.result_detail?.content) continue;
    const content = item.result_detail.content;
    // Retain ordered mixed results here. Presentation decides which parts belong
    // in the image stream or document output, without modifying the tool result.
    if (!content.some((part) => part.type !== "text")) continue;
    content.forEach((part, index) => {
      const artifact = artifactFromContentPart(item, part, index, contentPartPlacement(content, index));
      if (!artifact) return;
      // Re-publishing an unchanged snapshot in the same turn should not repeat
      // its preview. Preserve ordered mixed results and different file versions.
      if (artifact.type !== "text" && artifact.sha256) {
        const key = JSON.stringify([artifact.sha256, artifact.name, artifact.mimeType, artifact.placement, artifact.delivered, artifact.foldPreview]);
        const owner = presented.get(key);
        if (owner && owner !== item.id) return;
        presented.set(key, item.id);
      }
      artifacts.push(artifact);
    });
  }
  return artifacts;
}

export function turnEndFileArtifacts(artifacts: readonly TurnArtifact[]): readonly TurnArtifact[] {
  return artifacts.filter((artifact) => artifact.placement === "turn_end" && artifact.type !== "text");
}

export function turnHasTurnEndArtifacts(turn: Turn): boolean {
  return turnEndFileArtifacts(collectTurnArtifacts(turn)).some((artifact) => artifact.delivered);
}

export function TurnArtifactSummaryPresentation({
  turn,
  isLatestTurn,
  cwd,
  onOpenFile,
  onCollapseComplete,
}: {
  turn: Turn;
  isLatestTurn: boolean;
  cwd?: string;
  onOpenFile?: (path: string) => void;
  onCollapseComplete?: () => void;
}): JSX.Element | null {
  const artifacts = collectTurnArtifacts(turn).filter((artifact) => artifact.delivered);
  return (
    <TurnOutputSummaryPresentation
      visible={turnOutputSummaryVisible(turn, isLatestTurn) && turnEndFileArtifacts(artifacts).length > 0}
      onCollapseComplete={onCollapseComplete}
    >
      <TurnEndArtifactOutputs
        artifacts={artifacts}
        cwd={cwd}
        onOpenFile={onOpenFile}
        compact={turn.status === "failed" || turn.status === "interrupted"}
        stopped={turn.status === "interrupted"}
      />
    </TurnOutputSummaryPresentation>
  );
}

function useArtifactPreview(
  fallback: (artifact: TurnArtifact) => void,
  cwd?: string,
): (artifact: TurnArtifact, origin?: HTMLElement) => void {
  const service = useArtifacts();
  const lifetime = useArtifactLifetime();
  const panel = useContext(ArtifactPreviewContext);
  const threadID = useContext(ArtifactThreadContext);
  const { openPreview } = useImagePreview();
  return (artifact, origin) => {
    if (artifact.mimeType.startsWith("image/")) {
      openPreview({
        src: service.resolveSource({...artifact, cwd}) ?? "", alt: artifact.name, title: artifact.name,
        loadSource: artifact.remoteRef && !artifact.data
          ? async () => `data:${artifact.mimeType};base64,${await service.readAttachment({remoteRef: artifact.remoteRef!}, {signal: lifetime.current.signal})}`
          : undefined,
      }, origin);
    } else if (panel && threadID) {
      panel({ threadID, cwd, artifact, origin: origin ? new WeakRef(origin) : undefined });
    } else {
      fallback(artifact);
    }
  };
}

export function TurnInlineArtifactOutputs({
  artifacts,
  cwd,
  onOpenFile,
  inspectionExpanded = false,
}: {
  artifacts: readonly TurnArtifact[];
  cwd?: string;
  onOpenFile?: (path: string) => void;
  inspectionExpanded?: boolean;
}): JSX.Element | null {
  const { t } = useI18n();
  const [preview, setPreview] = useState<TurnArtifact>();
  const [tiled, setTiled] = useState(false);
  const galleryRef = useRef<HTMLDivElement>(null);
  // The image stream is visual output, not another tool-result inspector.
  const openPreview = useArtifactPreview(setPreview, cwd);
  const inline = artifacts.filter((artifact) => artifact.placement === "inline" && artifact.type !== "text");
  const isGallery = inline.length > 0 && inline.every(artifact => artifact.mimeType.startsWith("image/") && !artifact.foldPreview);
  useEffect(() => {
    const gallery = galleryRef.current;
    if (!isGallery || tiled || !gallery) return;
    // Support mouse wheels without trapping page scrolling at either end.
    // Trackpad horizontal gestures and browser zoom retain native behavior.
    const onWheel = (event: WheelEvent) => {
      if (event.ctrlKey || event.metaKey || event.shiftKey || event.deltaX !== 0) return;
      const delta = event.deltaY * (event.deltaMode === 1 ? parseFloat(getComputedStyle(gallery).fontSize)
        : event.deltaMode === 2 ? gallery.clientWidth : 1);
      const previous = gallery.scrollLeft;
      gallery.scrollLeft = previous + delta;
      // Let the browser clamp fractional scroll limits before deciding whether
      // this wheel moved the gallery or should continue scrolling the page.
      if (gallery.scrollLeft !== previous) event.preventDefault();
    };
    gallery.addEventListener("wheel", onWheel, { passive: false });
    return () => gallery.removeEventListener("wheel", onWheel);
  }, [isGallery, tiled]);
  if (inline.length === 0) return null;
  const content = inline.map((artifact) => (
    <ArtifactRenderer
      artifact={artifact}
      cwd={cwd}
      key={artifact.id}
      onOpenFile={onOpenFile}
      onPreview={openPreview}
      variant="inline"
      inspectionExpanded={inspectionExpanded}
    />
  ));
  const toggleLabel = t(tiled ? "artifacts.carouselView" : "artifacts.gridView");
  return (
    <>
      <div className="turn-artifact-inline-list" data-wuu-component="turn-artifacts-inline">
        {isGallery ? <>
          {inline.length > 1 ? (
            <div className="turn-artifact-gallery-toolbar">
              <Tooltip content={toggleLabel}>
                <button type="button" className="icon-button" data-gallery-toggle=""
                  aria-label={toggleLabel} onClick={() => setTiled(value => !value)}>
                  {tiled ? <Images className="icon" /> : <LayoutGrid className="icon" />}
                </button>
              </Tooltip>
            </div>
          ) : null}
          <div ref={galleryRef}
            className={`turn-artifact-gallery-items scrollbar-hidden${tiled ? " is-tiled" : ""}`}
            data-scroll-fade={!tiled ? "inline" : undefined}
            tabIndex={!tiled && inline.length > 1 ? 0 : undefined}
            role="group" aria-label={t("artifacts.imageGallery")}>
            {content}
          </div>
        </> : content}
      </div>
      {preview ? (
        <ArtifactPreview artifact={preview} cwd={cwd} onClose={() => setPreview(undefined)} />
      ) : null}
    </>
  );
}

export function TurnEndArtifactOutputs({
  artifacts,
  cwd,
  onOpenFile,
  compact = false,
  stopped = false,
}: {
  artifacts: readonly TurnArtifact[];
  cwd?: string;
  onOpenFile?: (path: string) => void;
  compact?: boolean;
  stopped?: boolean;
}): JSX.Element | null {
  const { t, formatNumber } = useI18n();
  const service = useArtifacts();
  const [preview, setPreview] = useState<TurnArtifact>();
  const [visibleCount, setVisibleCount] = useState(TURN_OUTPUT_SUMMARY_BATCH_SIZE);
  const [expanded, setExpanded] = useState(false);
  const files = turnEndFileArtifacts(artifacts);
  const openPreview = useArtifactPreview(setPreview, cwd);
  const fileActions = useArtifactFileActions(cwd);

  useEffect(() => {
    if (preview && !files.some((artifact) => artifact.id === preview.id)) {
      setPreview(undefined);
    }
  }, [preview, files]);

  if (files.length === 0) return null;

  const openArtifact = (artifact: TurnArtifact, origin: HTMLElement): void => {
    openArtifactFile(service, artifact, origin, cwd, openPreview);
  };

  if (compact) {
    return (
      <div className="turn-edit-summary-compact">
        <button
          type="button"
          className="turn-edit-summary-toggle"
          aria-expanded={expanded}
          onClick={() => setExpanded((value) => !value)}
        >
          <span>
            {t(stopped ? "artifacts.retainedStopped" : "artifacts.retained", { count: formatNumber(files.length) })}
          </span>
          <ChevronRight className="icon-xs" aria-hidden="true" />
        </button>
        {expanded ? (
          <TurnEndArtifactOutputs artifacts={artifacts} cwd={cwd} onOpenFile={onOpenFile} />
        ) : null}
      </div>
    );
  }

  const visible = files.slice(0, visibleCount);
  const hiddenCount = Math.max(0, files.length - visibleCount);
  const nextCount = Math.min(TURN_OUTPUT_SUMMARY_BATCH_SIZE, hiddenCount);
  const single = files.length === 1 ? files[0] : undefined;

  return (
    <>
      {single ? (
        <TurnOutputSummaryCard
          component="turn-artifacts"
          icon={<ArtifactFileIcon artifact={single} />}
          title={single.name}
          subtitle={<ArtifactMetadata artifact={single} />}
          trailing={<TurnOutputSummaryChevron />}
          onOpen={(event) => openArtifact(single, event.currentTarget)}
          onContextMenu={(event) => fileActions.show(single, event)}
          openLabel={t("artifacts.openNamed", { name: single.name })}
          wrapOverview={(overview) => <Tooltip content={single.name}>{overview}</Tooltip>}
        />
      ) : (
        <TurnOutputSummaryCard
          component="turn-artifacts"
          rows={visible.map((artifact) => ({
            key: artifact.id,
            name: artifact.name,
            icon: <ArtifactFileIcon artifact={artifact} />,
            subtitle: <ArtifactMetadata artifact={artifact} />,
            trailing: <TurnOutputSummaryChevron />,
            tooltip: artifact.name,
            onOpen: (event) => openArtifact(artifact, event.currentTarget),
            onContextMenu: (event) => fileActions.show(artifact, event),
            openLabel: t("artifacts.openNamed", { name: artifact.name }),
          }))}
          footer={files.length > TURN_OUTPUT_SUMMARY_BATCH_SIZE ? (
            <TurnOutputSummaryMore
              totalCount={files.length}
              hiddenCount={hiddenCount}
              nextCount={nextCount}
              onShowMore={() =>
                setVisibleCount((current) => Math.min(current + TURN_OUTPUT_SUMMARY_BATCH_SIZE, files.length))
              }
            />
          ) : null}
        />
      )}
      {fileActions.menu}
      {preview ? (
        <ArtifactPreview
          artifact={preview}
          cwd={cwd}
          onClose={() => setPreview(undefined)}
        />
      ) : null}
    </>
  );
}

function ArtifactRenderer({
  artifact,
  cwd,
  onOpenFile,
  onPreview,
  variant,
  inspectionExpanded = false,
}: {
  artifact: TurnArtifact;
  cwd?: string;
  onOpenFile?: (path: string) => void;
  onPreview?: (artifact: TurnArtifact, origin?: HTMLElement) => void;
  variant: "inline" | "card";
  inspectionExpanded?: boolean;
}): JSX.Element {
  const { t } = useI18n();
  const [expanded, setExpanded] = useState(false);
  const fallback = artifact.type === "text" ? (
    <div className="turn-artifact-text-part">
      <ToolResultText text={artifact.text ?? ""} folded={artifact.foldText} cwd={cwd} onOpenFile={onOpenFile} />
    </div>
  ) : variant === "inline" && artifact.mimeType.startsWith("image/") ? (
    <InlineArtifact artifact={artifact} cwd={cwd} />
  ) : (
    <ArtifactCard
      artifact={artifact}
      cwd={cwd}
      onOpenFile={onOpenFile}
      onPreview={onPreview}
    />
  );
  const { slots, slotOwner } = useConversationControls();
  const { threadId, turnId } = useConversationTurnIdentity();
  const content = <slots.Slot name={`${slotOwner}/conversation.artifact`}
    context={{threadId, turnId, itemId: artifact.itemId, partId: artifact.id, variant}}
    fallback={fallback} />;
  if (variant === "inline" && artifact.foldPreview && !inspectionExpanded) {
    return (
      <div className="process-surface">
        <ProcessSurfaceFold
          open={expanded}
          onToggle={(event) => setExpanded(event.currentTarget.open)}
          summary={<span className="process-surface-summary-line">
            <TruncatedText className="process-surface-summary-text" text={t("toolActivity.viewTarget", { target: artifact.name })} />
          </span>}
        >
          {expanded ? content : null}
        </ProcessSurfaceFold>
      </div>
    );
  }
  return content;
}

function ToolResultText({ text, folded, cwd, onOpenFile }: { text: string; folded?: boolean; cwd?: string; onOpenFile?: (path: string) => void }): JSX.Element {
  const { t } = useI18n();
  const [expanded, setExpanded] = useState(false);
  const structured = useMemo(() => {
    try {
      const value: unknown = JSON.parse(text);
      return value !== null && typeof value === "object" ? JSON.stringify(value, null, 2) : undefined;
    } catch { return undefined; }
  }, [text]);
  // Tool observations are not answer prose, even when their metadata is plain
  // text. Explicitly presented artifacts can still carry visible captions.
  if (!folded && structured === undefined) return <RichContent text={text} cwd={cwd} onOpenFile={onOpenFile} />;
  return <div className="process-surface tool-result-data">
    <ProcessSurfaceFold open={expanded} onToggle={(event) => setExpanded(event.currentTarget.open)}
      summary={<span className="process-surface-summary-line">{t("artifacts.structuredData")}</span>}>
      {expanded ? <pre className="tool-result-data-json">{structured ?? text}</pre> : null}
    </ProcessSurfaceFold>
  </div>;
}

function InlineArtifact({ artifact, cwd }: { artifact: TurnArtifact; cwd?: string }): JSX.Element {
  const { t } = useI18n();
  const { openPreview } = useImagePreview();
  const [failedSource, setFailedSource] = useState<string>();
  const source = useArtifacts().resolveSource({...artifact, cwd});
  const register = useImagePreviewRegistration(source && failedSource !== source && artifact.mimeType.startsWith("image/")
    ? { src: source, alt: artifact.name, title: artifact.name } : null);
  let image: ReactNode;
  if (artifact.remoteRef && artifact.mimeType.startsWith("image/")) {
    image = (
      <AttachmentImage
        image={{ media_type: artifact.mimeType, data: artifact.data ?? "", remote_ref: artifact.remoteRef }}
        label={t("imagePreview.label")}
        previewTitle={artifact.name}
        onOpen={(src, origin) => openPreview({ src, alt: artifact.name, title: artifact.name }, origin)}
      />
    );
  } else if (!source || !artifact.mimeType.startsWith("image/")) {
    return <div className="turn-artifact-unavailable">{t("imagePreview.loadFailed")}</div>;
  } else {
    image = (
      <button
        type="button"
        ref={register}
        onClick={event => openPreview({ src: source, alt: artifact.name, title: artifact.name }, event.currentTarget)}
        aria-label={t("artifacts.previewNamed", { name: artifact.name })}
        disabled={failedSource === source}
      >
        {failedSource === source ? (
          <span className="turn-artifact-unavailable">{t("imagePreview.loadFailed")}</span>
        ) : (
          <img src={source} alt={artifact.name} loading="lazy" onError={() => setFailedSource(source)} />
        )}
      </button>
    );
  }
  return (
    <figure className="turn-artifact-inline-image">
      <div className="turn-artifact-image-preview">{image}</div>
    </figure>
  );
}

function ArtifactFileIcon({ artifact }: { artifact: TurnArtifact }): JSX.Element {
  const mimeType = artifact.mimeType.split(";", 1)[0].toLowerCase();
  const extension = mimeType === "application/octet-stream" || mimeType === "text/plain"
    ? fileNameParts(artifact.name).extension.toLowerCase() : "";
  const Icon = [".zip", ".tar", ".gz", ".tgz", ".bz2", ".xz", ".7z", ".rar"].includes(extension)
    || /(?:zip|compressed|archive|x-tar)$/.test(mimeType) ? Archive
    : [".patch", ".diff"].includes(extension) || /(?:patch|diff)$/.test(mimeType) ? FileDiff
    : mimeType.startsWith("image/") ? Images
    : mimeType.startsWith("video/") ? Film
    : FileText;
  return <Icon className="icon" aria-hidden="true" />;
}

function ArtifactMetadata({ artifact }: { artifact: TurnArtifact }): JSX.Element {
  const { t, formatNumber } = useI18n();
  const suffix = fileNameParts(artifact.name).extension;
  // Resource titles can contain dots without being filenames, such as v1.2.
  const extension = /^\.[a-z][a-z0-9]{0,9}$/i.test(suffix) ? suffix.slice(1).toUpperCase() : "";
  const mimeType = artifact.mimeType.split(";", 1)[0].toLowerCase();
  const genericMime = mimeType === "application/octet-stream" || mimeType === "text/plain";
  const mimeFormat = extensionForMimeType(mimeType).slice(1).toUpperCase()
    || mimeType.match(/^[^/]+\/(?:x-)?([a-z][a-z0-9-]{0,11})$/)?.[1].toUpperCase();
  const format = (genericMime ? extension : mimeFormat) || extension
    || (mimeType === "text/plain" ? "TXT" : t("artifacts.file"));
  const size = artifact.sizeBytes !== undefined && Number.isFinite(artifact.sizeBytes) && artifact.sizeBytes >= 0
    ? formatByteSize(artifact.sizeBytes, formatNumber)
    : undefined;
  return (
    <span className="turn-artifact-meta">
      <span>{format}</span>
      {size ? <><span aria-hidden="true">·</span><span>{size}</span></> : null}
    </span>
  );
}

function openArtifactFile(
  service: ApplicationArtifactsV1,
  artifact: TurnArtifact,
  origin: HTMLElement,
  cwd: string | undefined,
  onPreview: ((artifact: TurnArtifact, origin?: HTMLElement) => void) | undefined,
): void {
  const workspacePath = workspaceArtifactPath(artifact.uri, cwd);
  // Bare workspace references need the workspace reader. Managed deliveries
  // and embedded bytes must retain their own immutable preview source.
  if (workspacePath && cwd && !artifact.data && artifact.text === undefined && !service.resolveSource({...artifact, cwd})) {
    void service.openFile({...artifact, cwd, uri: workspacePath}).catch(showErrorToast);
  } else {
    onPreview?.(artifact, origin);
  }
}

function nativeArtifactActionsAvailable(service: ApplicationArtifactsV1, artifact: TurnArtifact, threadID: string | undefined): boolean {
  return Boolean(threadID && !artifact.remoteRef && /^wuu-artifact:/i.test(artifact.uri ?? "") && service.capabilities.nativeItemMenu);
}

function useArtifactLifetime(identity?: unknown) {
  const lifetime = useRef(new AbortController());
  useEffect(() => {
    lifetime.current = new AbortController();
    return () => lifetime.current.abort();
  }, [identity]);
  return lifetime;
}

function useArtifactFileActions(cwd?: string, sourceThreadID?: string): {
  show: (artifact: TurnArtifact, event: MouseEvent<HTMLElement>) => void;
  menu: ReactNode;
} {
  const service = useArtifacts();
  const lifetime = useArtifactLifetime(service);
  const contextThreadID = useContext(ArtifactThreadContext);
  const threadID = sourceThreadID ?? contextThreadID;
  const [menu, setMenu] = useState<{ artifact: TurnArtifact; x: number; y: number }>();
  const show = (artifact: TurnArtifact, event: MouseEvent<HTMLElement>): void => {
    event.preventDefault();
    event.stopPropagation();
    if (nativeArtifactActionsAvailable(service, artifact, threadID)) {
      setMenu(undefined);
      const signal = lifetime.current.signal;
      void service.showItemMenu({...artifact, cwd, threadId: threadID}, {signal})
        .catch(error => { if (!signal.aborted) showErrorToast(error); });
    } else {
      const bounds = event.currentTarget.getBoundingClientRect();
      setMenu({ artifact, x: event.clientX || bounds.left, y: event.clientY || bounds.bottom });
    }
  };
  return {
    show,
    menu: menu ? <ArtifactDownloadMenu key={menu.artifact.id} {...menu} cwd={cwd} threadID={threadID} onClose={() => setMenu(undefined)} /> : null,
  };
}

function ArtifactDownloadMenu({ artifact, cwd, threadID, x, y, onClose }: {
  artifact: TurnArtifact; cwd?: string; threadID?: string; x: number; y: number; onClose: () => void;
}): JSX.Element {
  const { t } = useI18n();
  const service = useArtifacts();
  const lifetime = useArtifactLifetime(artifact);
  const source = useArtifactPreviewSource(artifact, cwd);
  return <ThreadContextMenu x={x} y={y} onClose={onClose} items={[{
    label: t("artifacts.download"),
    icon: <Download className="icon" aria-hidden="true" />,
    disabled: !source,
    onSelect: () => {
      const signal = lifetime.current.signal;
      if (source) void service.save({...artifact, cwd, threadId: threadID}, {signal}).catch(error => { if (!signal.aborted) showErrorToast(error); });
    },
  }]} />;
}

function ArtifactCard({
  artifact,
  cwd,
  onOpenFile,
  onPreview,
}: {
  artifact: TurnArtifact;
  cwd?: string;
  onOpenFile?: (path: string) => void;
  onPreview?: (artifact: TurnArtifact, origin?: HTMLElement) => void;
}): JSX.Element {
  const { t } = useI18n();
  const service = useArtifacts();
  const fileActions = useArtifactFileActions(cwd);
  if (artifact.remoteRef && artifact.mimeType.startsWith("image/")) return <InlineArtifact artifact={artifact} cwd={cwd} />;

  return (
    <>
      <TurnOutputSummaryCard
        component="artifact"
        icon={<ArtifactFileIcon artifact={artifact} />}
        title={artifact.name}
        subtitle={<ArtifactMetadata artifact={artifact} />}
        trailing={<TurnOutputSummaryChevron />}
        onOpen={(event) => openArtifactFile(service, artifact, event.currentTarget, cwd, onPreview)}
        onContextMenu={(event) => fileActions.show(artifact, event)}
        openLabel={t("artifacts.openNamed", { name: artifact.name })}
        wrapOverview={(overview) => <Tooltip content={artifact.name}>{overview}</Tooltip>}
      />
      {fileActions.menu}
    </>
  );
}

export function ArtifactPreview({
  artifact,
  threadID,
  active = true,
  cwd,
  onClose,
  mode = "overlay",
  origin, initialPage, pageRequest,
}: {
  artifact: TurnArtifact;
  threadID?: string;
  active?: boolean;
  cwd?: string;
  onClose: () => void;
  mode?: "overlay" | "panel";
  origin?: WeakRef<HTMLElement>;
  initialPage?: number;
  pageRequest?: string;
}): JSX.Element {
  const { t } = useI18n();
  const service = useArtifacts();
  const lifetime = useArtifactLifetime(artifact);
  const source = useArtifactPreviewSource(artifact, cwd);
  const contextualThreadID = useContext(ArtifactThreadContext);
  const ownerThreadID = threadID ?? contextualThreadID;
  const fileActions = useArtifactFileActions(cwd, ownerThreadID);
  const nativeActions = nativeArtifactActionsAvailable(service, artifact, ownerThreadID);
  const [downloadError,setDownloadError] = useState("");
  const dialogRef = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    if (mode === "panel" && !active) restoreArtifactPreviewFocus(origin);
  }, [active, mode, origin]);
  useEffect(() => {
    if (mode === "panel") return;
    const previouslyFocused = document.activeElement instanceof HTMLElement
      ? document.activeElement
      : undefined;
    const focusable = (): HTMLElement[] => Array.from(
      dialogRef.current?.querySelectorAll<HTMLElement>(
        "button:not([disabled]), a[href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), iframe, audio[controls], video[controls], [tabindex]:not([tabindex='-1'])",
      ) ?? [],
    );
    focusable()[0]?.focus();
    const handleKeyDown = (event: KeyboardEvent): void => {
      if (event.key === "Escape") {
        event.preventDefault();
        onClose();
        return;
      }
      if (event.key !== "Tab") return;
      const targets = focusable();
      if (targets.length === 0) {
        event.preventDefault();
        dialogRef.current?.focus();
        return;
      }
      const first = targets[0];
      const last = targets.at(-1);
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last?.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    };
    window.addEventListener("keydown", handleKeyDown);
    return () => {
      window.removeEventListener("keydown", handleKeyDown);
      if (previouslyFocused?.isConnected) previouslyFocused.focus();
    };
  }, [onClose, mode]);

  const download = (): void => {
    const signal = lifetime.current.signal;
    if (source) void service.save({...artifact, cwd, threadId: ownerThreadID}, {signal}).catch(error => { if (!signal.aborted) setDownloadError(String(error)); });
  };

  let body: ReactNode;
  if (!source) {
    body = <div className="artifact-preview-empty">{t("artifacts.previewUnavailable")}</div>;
  } else if (artifact.mimeType === "application/pdf") {
    body = (
      <Suspense fallback={<div className="artifact-preview-empty">{t("imagePreview.loading")}</div>}>
        <WorkspacePdfPreview url={source} title={artifact.name} active={active} initialPage={initialPage} pageRequest={pageRequest}
          source={cwd && ownerThreadID && artifact.sha256 && artifact.uri?.startsWith("wuu-artifact:") ? {
            workspace: cwd, path: artifact.name, revision: `sha256:${artifact.sha256}`,
            artifact: { artifact_uri: artifact.uri, artifact_sha256: artifact.sha256, artifact_thread_id: ownerThreadID },
          } : undefined} />
      </Suspense>
    );
  } else if (isHtmlMimeType(artifact.mimeType)) {
    body = (
      <iframe
        className="artifact-preview-frame"
        src={source}
        sandbox=""
        title={artifact.name}
        referrerPolicy="no-referrer"
      />
    );
  } else if (artifact.mimeType.startsWith("audio/")) {
    body = <audio className="artifact-preview-audio" src={source} controls />;
  } else if (artifact.mimeType.startsWith("video/")) {
    body = <VideoPreview src={source} title={artifact.name} active={active} />;
  } else if (artifact.mimeType.startsWith("text/") || artifact.text !== undefined || isMarkdownDocument(artifact.name, artifact.mimeType)) {
    body = <ArtifactTextPreview artifact={artifact} source={source} />;
  } else {
    body = <div className="artifact-preview-empty">{t("artifacts.previewUnavailable")}</div>;
  }

  return (
    <div
      className={mode === "panel" ? "artifact-preview-panel" : "artifact-preview-overlay"}
      role={mode === "panel" ? "region" : "dialog"}
      aria-modal={mode === "overlay" ? true : undefined}
      aria-label={t("artifacts.previewNamed", { name: artifact.name })}
      onClick={(event) => {
        if (mode === "overlay" && event.target === event.currentTarget) onClose();
      }}
    >
      <div className={mode === "panel" ? "artifact-preview-content" : "artifact-preview-shell"} ref={dialogRef} tabIndex={-1}>
        <header className="artifact-preview-toolbar">
          <div>
            <TruncatedText as="strong" text={artifact.name} />
          </div>
          <div className="artifact-preview-actions">
            {nativeActions ? (
              <Tooltip content={t("artifacts.fileActions")}>
                <button type="button" aria-label={t("artifacts.fileActions")} onClick={event => fileActions.show(artifact, event)}>
                  <Ellipsis className="icon" aria-hidden="true" />
                </button>
              </Tooltip>
            ) : null}
            {source ? (
              <Tooltip content={t("artifacts.download")}>
                <button type="button" onClick={download} aria-label={t("artifacts.downloadNamed", { name: artifact.name })}>
                  <Download className="icon" aria-hidden="true" />
                </button>
              </Tooltip>
            ) : null}
            <Tooltip content={t("common.close")}>
              <button type="button" onClick={onClose} aria-label={t("common.close")}>
                <X className="icon" aria-hidden="true" />
              </button>
            </Tooltip>
          </div>
        </header>
        {downloadError && <p role="alert">{downloadError}</p>}
        <div className="artifact-preview-body">{body}</div>
        {fileActions.menu}
      </div>
    </div>
  );
}

function ArtifactTextPreview({ artifact, source }: { artifact: TurnArtifact; source: string }): JSX.Element {
  const { t } = useI18n();
  const service = useArtifacts();
  const [result, setResult] = useState<{ artifact: TurnArtifact; source: string; text?: string; failed?: boolean }>();
  useEffect(() => {
    const controller = new AbortController();
    void service.readText({...artifact, uri: source}, {signal: controller.signal}).then(text => {
      if (!controller.signal.aborted) setResult({artifact, source, text});
    }).catch(() => { if (!controller.signal.aborted) setResult({artifact, source, failed: true}); });
    return () => controller.abort();
  }, [service, artifact, source]);
  const current = result?.source === source && result.artifact === artifact ? result : undefined;
  if (current?.text === undefined) return <div className="artifact-preview-empty">{t(current?.failed ? "artifacts.previewUnavailable" : "imagePreview.loading")}</div>;
  return isMarkdownDocument(artifact.name, artifact.mimeType)
    ? <MarkdownDocumentReading text={current.text} /> : <pre className="artifact-preview-text">{current.text}</pre>;
}

function artifactFromContentPart(
  item: ThreadItem,
  part: ToolResultContentPart,
  index: number,
  placement: "inline" | "turn_end",
): TurnArtifact | undefined {
  const resource = recordValue(part.resource);
  const mimeType = firstString(part.mime_type, resource?.mime_type, resource?.mimeType)
    ?? inferMimeType(part.name ?? firstString(resource?.name, resource?.title), part.uri ?? firstString(resource?.uri), part.type);
  const uri = firstString(part.uri, resource?.uri);
  const data = firstString(part.data, resource?.blob, resource?.data);
  const text = typeof part.text === "string" ? part.text : firstString(resource?.text);
  const name = firstString(part.name, resource?.name, resource?.title)
    ?? artifactNameFromURI(uri)
    ?? `artifact-${index + 1}${extensionForMimeType(mimeType)}`;
  return Object.freeze({
    id: `${item.id}:artifact:${index}`,
    itemId: item.id,
    index,
    type: part.type,
    name,
    mimeType,
    remoteRef: part.remote_ref,
    data,
    text,
    foldText: part.type === "text" && !part.artifact?.placement,
    // Explicit presentation survives PTC/background forwarding; ordinary tool
    // images are inspection evidence and mount only when the user expands them.
    foldPreview: mimeType.startsWith("image/") && !part.artifact?.placement,
    uri,
    resource: part.resource,
    placement,
    ref: part.artifact?.ref,
    sha256: part.artifact?.sha256,
    sizeBytes: part.artifact?.size_bytes,
    delivered: part.artifact?.placement === "turn_end" && item.status === "completed" && !item.result_detail?.is_error,
  });
}

function contentPartPlacement(
  content: readonly ToolResultContentPart[],
  index: number,
): "inline" | "turn_end" {
  const current = content[index];
  if (current?.artifact?.placement) return current.artifact.placement;
  if (current && current.type !== "text") return defaultContentPartPlacement(current);
  for (let next = index + 1; next < content.length; next += 1) {
    if (content[next]?.type !== "text") return defaultContentPartPlacement(content[next]);
  }
  for (let previous = index - 1; previous >= 0; previous -= 1) {
    if (content[previous]?.type !== "text") return defaultContentPartPlacement(content[previous]);
  }
  return "inline";
}

function defaultContentPartPlacement(part: ToolResultContentPart): "inline" | "turn_end" {
  if (part.artifact?.placement) return part.artifact.placement;
  const resource = recordValue(part.resource);
  const mimeType = firstString(part.mime_type, resource?.mime_type, resource?.mimeType)
    ?? inferMimeType(part.name, part.uri, part.type);
  return mimeType.startsWith("image/") || part.type === "image" ? "inline" : "turn_end";
}

function useArtifactPreviewSource(artifact: TurnArtifact, cwd?: string): string | undefined {
  const service = useArtifacts();
  const direct = service.resolveSource({...artifact, cwd});
  const [source, setSource] = useState<{artifact: TurnArtifact; cwd?: string; url: string}>();
  useEffect(() => {
    if (direct) return;
    const abort = new AbortController();
    let leaseId: string | undefined;
    void service.acquireSource({...artifact, cwd}, {signal: abort.signal}).then(source => {
      if (abort.signal.aborted) { if (source.leaseId) service.releaseSource(source.leaseId); return; }
      leaseId = source.leaseId;
      setSource({artifact, cwd, url: source.url});
    }).catch(() => { /* Missing sources keep the existing unavailable presentation. */ });
    return () => { abort.abort(); if (leaseId) service.releaseSource(leaseId); };
  }, [service, artifact, cwd, direct]);
  return direct ?? (source?.artifact === artifact && source.cwd === cwd ? source.url : undefined);
}

function workspaceArtifactPath(uri: string | undefined, cwd?: string): string | undefined {
  if (!uri || /^(https?:|data:|blob:|wuu-file:|wuu-artifact:)/i.test(uri)) return undefined;
  const absolute = absoluteArtifactPath(uri, cwd);
  if (!absolute) return undefined;
  if (cwd && absolute.startsWith(`${cwd.replace(/\/$/, "")}/`)) {
    return absolute.slice(cwd.replace(/\/$/, "").length + 1);
  }
  return uri.startsWith("file:") ? absolute : uri;
}

function absoluteArtifactPath(uri: string, cwd?: string): string | undefined {
  if (uri.startsWith("file:")) {
    try {
      return decodeURIComponent(new URL(uri).pathname);
    } catch {
      return undefined;
    }
  }
  if (uri.startsWith("/")) return uri;
  if (!cwd || /^[a-z][a-z0-9+.-]*:/i.test(uri)) return undefined;
  const stack: string[] = [];
  for (const segment of `${cwd}/${uri}`.split("/")) {
    if (!segment || segment === ".") continue;
    if (segment === "..") stack.pop();
    else stack.push(segment);
  }
  return `/${stack.join("/")}`;
}

function recordValue(value: unknown): Record<string, unknown> | undefined {
  return typeof value === "object" && value !== null && !Array.isArray(value)
    ? value as Record<string, unknown>
    : undefined;
}

function firstString(...values: unknown[]): string | undefined {
  for (const value of values) {
    if (typeof value === "string" && value.trim()) return value.trim();
  }
  return undefined;
}

function inferMimeType(name: string | undefined, uri: string | undefined, type: string): string {
  const video = videoMimeType(name ?? "") ?? videoMimeType(artifactNameFromURI(uri) ?? "");
  if (video) return video;
  const target = `${name ?? ""} ${uri ?? ""}`.toLowerCase().split(/[?#]/, 1)[0];
  if (/\.html?\b/.test(target)) return "text/html";
  if (/\.pdf\b/.test(target)) return "application/pdf";
  if (/\.svg\b/.test(target)) return "image/svg+xml";
  if (/\.png\b/.test(target)) return "image/png";
  if (/\.jpe?g\b/.test(target)) return "image/jpeg";
  if (/\.webp\b/.test(target)) return "image/webp";
  if (/\.gif\b/.test(target)) return "image/gif";
  if (/\.md\b/.test(target)) return "text/markdown";
  if (/\.txt\b/.test(target)) return "text/plain";
  if (type === "text") return "text/plain";
  if (type === "image") return "image/*";
  if (type === "audio") return "audio/*";
  return "application/octet-stream";
}

function extensionForMimeType(mimeType: string): string {
  switch (mimeType) {
    case "text/html": return ".html";
    case "application/pdf": return ".pdf";
    case "image/png": return ".png";
    case "image/jpeg": return ".jpg";
    case "image/svg+xml": return ".svg";
    case "image/webp": return ".webp";
    default: return "";
  }
}

function artifactNameFromURI(uri: string | undefined): string | undefined {
  if (!uri) return undefined;
  try {
    const pathname = new URL(uri, "file:///").pathname;
    return decodeURIComponent(pathname.split("/").filter(Boolean).at(-1) ?? "") || undefined;
  } catch {
    return uri.split("/").filter(Boolean).at(-1);
  }
}

function isHtmlMimeType(mimeType: string): boolean {
  return mimeType === "text/html" || mimeType === "application/xhtml+xml";
}
