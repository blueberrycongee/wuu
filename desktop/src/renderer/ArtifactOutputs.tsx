import { lazy, Suspense, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Download, ExternalLink, X } from "lucide-react";

import type { ThreadItem, ToolResultContentPart, Turn } from "../shared/protocol";
import { useImagePreview } from "./ImagePreview";
import { useI18n } from "./i18n";
import { desktopWorkbenchController } from "./plugins/DesktopPluginRuntime";
import { WorkbenchContentRenderer } from "./plugins/Workbench";
import { RichContent } from "./RichContent";
import { AttachmentImage } from "./AttachmentImage";
import { ProcessSurfaceFold } from "./ProcessSurfaceFold";

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
  uri?: string;
  resource?: unknown;
  placement: "inline" | "turn_end";
  ref?: string;
  sha256?: string;
  sizeBytes?: number;
}>;

export function collectTurnArtifacts(turn: Turn): readonly TurnArtifact[] {
  const artifacts: TurnArtifact[] = [];
  for (const item of turn.items) {
    if (item.type !== "tool_call" || !item.result_detail?.content) continue;
    const content = item.result_detail.content;
    // Text-only results remain in process presentation. Once a result contains
    // media or a resource, retain every part so [image, text, image] cannot be
    // silently projected as [image, image].
    if (!content.some((part) => part.type !== "text")) continue;
    content.forEach((part, index) => {
      const artifact = artifactFromContentPart(item, part, index, contentPartPlacement(content, index));
      if (artifact) artifacts.push(artifact);
    });
  }
  return artifacts;
}

export function TurnInlineArtifactOutputs({
  artifacts,
  cwd,
  onOpenFile,
}: {
  artifacts: readonly TurnArtifact[];
  cwd?: string;
  onOpenFile?: (path: string) => void;
}): JSX.Element | null {
  const [preview, setPreview] = useState<TurnArtifact>();
  const inline = artifacts.filter((artifact) => artifact.placement === "inline");
  if (inline.length === 0) return null;
  return (
    <>
      <div className="turn-artifact-inline-list" data-wuu-component="turn-artifacts-inline">
        {inline.map((artifact) => (
          <ArtifactRenderer
            artifact={artifact}
            cwd={cwd}
            key={artifact.id}
            onOpenFile={onOpenFile}
            onPreview={setPreview}
            variant="inline"
          />
        ))}
      </div>
      {preview ? (
        <ArtifactPreviewOverlay artifact={preview} cwd={cwd} onClose={() => setPreview(undefined)} />
      ) : null}
    </>
  );
}

export function TurnEndArtifactOutputs({
  artifacts,
  cwd,
  onOpenFile,
}: {
  artifacts: readonly TurnArtifact[];
  cwd?: string;
  onOpenFile?: (path: string) => void;
}): JSX.Element | null {
  const { t } = useI18n();
  const [preview, setPreview] = useState<TurnArtifact>();
  const turnEnd = artifacts.filter((artifact) => artifact.placement === "turn_end");
  const artifactCount = turnEnd.filter((artifact) => artifact.type !== "text").length;

  useEffect(() => {
    if (preview && !turnEnd.some((artifact) => artifact.id === preview.id)) {
      setPreview(undefined);
    }
  }, [preview, turnEnd]);

  if (turnEnd.length === 0) return null;
  return (
    <>
      <section className="turn-output-summary-card turn-artifact-summary-card" data-wuu-component="turn-artifacts">
        <header className="turn-output-summary-header turn-artifact-summary-header">
          <strong className="turn-output-summary-title">{t("artifacts.title")}</strong>
          <span>{t("artifacts.count", { count: artifactCount })}</span>
        </header>
        <div className="turn-output-summary-list turn-artifact-summary-list">
          {turnEnd.map((artifact) => (
            <ArtifactRenderer
              artifact={artifact}
              cwd={cwd}
              key={artifact.id}
              onOpenFile={onOpenFile}
              onPreview={setPreview}
              variant="card"
            />
          ))}
        </div>
      </section>
      {preview ? (
        <ArtifactPreviewOverlay
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
}: {
  artifact: TurnArtifact;
  cwd?: string;
  onOpenFile?: (path: string) => void;
  onPreview?: (artifact: TurnArtifact) => void;
  variant: "inline" | "card";
}): JSX.Element {
  const fallback = artifact.type === "text" ? (
    <div className="turn-artifact-text-part">
      <ToolResultText text={artifact.text ?? ""} cwd={cwd} onOpenFile={onOpenFile} />
    </div>
  ) : variant === "inline" && artifact.mimeType.startsWith("image/") ? (
    <InlineArtifact artifact={artifact} cwd={cwd} />
  ) : variant === "inline" ? (
    <div className="turn-artifact-inline-card">
      <ArtifactCard
        artifact={artifact}
        cwd={cwd}
        onOpenFile={onOpenFile}
        onPreview={onPreview}
      />
    </div>
  ) : (
    <ArtifactCard
      artifact={artifact}
      cwd={cwd}
      onOpenFile={onOpenFile}
      onPreview={onPreview}
    />
  );
  return (
    <WorkbenchContentRenderer
      controller={desktopWorkbenchController}
      category="tool-result"
      contentType={artifact.mimeType}
      content={artifact}
      metadata={{
        artifactId: artifact.ref ?? artifact.id,
        partId: artifact.id,
        itemId: artifact.itemId,
        placement: artifact.placement,
        variant,
      }}
      fallback={fallback}
    />
  );
}

function ToolResultText({ text, cwd, onOpenFile }: { text: string; cwd?: string; onOpenFile?: (path: string) => void }): JSX.Element {
  const { t } = useI18n();
  const [expanded, setExpanded] = useState(false);
  const structured = useMemo(() => {
    try {
      const value: unknown = JSON.parse(text);
      return value !== null && typeof value === "object" ? JSON.stringify(value, null, 2) : undefined;
    } catch { return undefined; }
  }, [text]);
  // Mixed image/data results retain every part, but machine-readable metadata
  // must not turn into a page of answer prose merely because an image follows.
  if (structured === undefined) return <RichContent text={text} cwd={cwd} onOpenFile={onOpenFile} />;
  return <div className="process-surface tool-result-data">
    <ProcessSurfaceFold open={expanded} onToggle={(event) => setExpanded(event.currentTarget.open)}
      summary={<span className="process-surface-summary-line">{t("artifacts.structuredData")}</span>}>
      {expanded ? <pre className="tool-result-data-json">{structured}</pre> : null}
    </ProcessSurfaceFold>
  </div>;
}

function InlineArtifact({ artifact, cwd }: { artifact: TurnArtifact; cwd?: string }): JSX.Element {
  const { t } = useI18n();
  const { openPreview } = useImagePreview();
  const source = artifactSource(artifact, cwd);
  if (artifact.remoteRef && artifact.mimeType.startsWith("image/")) {
    return <figure className="turn-artifact-inline-image">
      <AttachmentImage image={{media_type:artifact.mimeType,data:artifact.data ?? "",remote_ref:artifact.remoteRef}} label={artifact.name}
        onOpen={src => openPreview({src,alt:artifact.name,title:artifact.name})} />
      <figcaption>{artifact.name}</figcaption>
    </figure>;
  }
  if (!source || !artifact.mimeType.startsWith("image/")) {
    return <div className="turn-artifact-unavailable">{artifact.name}</div>;
  }
  const open = (): void => openPreview({ src: source, alt: artifact.name, title: artifact.name });
  return (
    <figure className="turn-artifact-inline-image">
      <button
        type="button"
        onClick={open}
        aria-label={t("artifacts.previewNamed", { name: artifact.name })}
      >
        <img src={source} alt={artifact.name} />
      </button>
      <figcaption>{artifact.name}</figcaption>
    </figure>
  );
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
  onPreview?: (artifact: TurnArtifact) => void;
}): JSX.Element {
  const { t } = useI18n();
  if (artifact.remoteRef && artifact.mimeType.startsWith("image/")) return <InlineArtifact artifact={artifact} cwd={cwd} />;
  const open = (): void => {
    if (canPreviewArtifact(artifact)) {
      onPreview?.(artifact);
      return;
    }
    const workspacePath = workspaceArtifactPath(artifact.uri, cwd);
    if (workspacePath && onOpenFile) {
      onOpenFile(workspacePath);
      return;
    }
    if (artifact.uri && /^https?:/i.test(artifact.uri)) {
      void window.wuu?.openExternal?.(artifact.uri);
      return;
    }
    onPreview?.(artifact);
  };
  return (
    <button
      type="button"
      className="turn-output-summary-row turn-artifact-summary-row"
      onClick={open}
      aria-label={t("artifacts.openNamed", { name: artifact.name })}
    >
      <span className="turn-output-summary-file">
        <strong className="turn-output-summary-name">{artifact.name}</strong>
      </span>
      <span className="turn-artifact-summary-meta">
        <span>{artifact.mimeType}</span>
        <ExternalLink aria-hidden="true" className="icon" />
      </span>
    </button>
  );
}

function ArtifactPreviewOverlay({
  artifact,
  cwd,
  onClose,
}: {
  artifact: TurnArtifact;
  cwd?: string;
  onClose: () => void;
}): JSX.Element {
  const { t } = useI18n();
  const source = useArtifactPreviewSource(artifact, cwd);
  const [downloadError,setDownloadError] = useState("");
  const dialogRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const previouslyFocused = document.activeElement instanceof HTMLElement
      ? document.activeElement
      : undefined;
    const focusable = (): HTMLElement[] => Array.from(
      dialogRef.current?.querySelectorAll<HTMLElement>(
        "button:not([disabled]), a[href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), iframe, audio[controls], [tabindex]:not([tabindex='-1'])",
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
  }, [onClose]);

  const download = (): void => {
    if (!source) return;
    if (window.wuu.saveArtifactFile) {
      void window.wuu.saveArtifactFile(artifact.name,source).catch(error=>setDownloadError(String(error)));
      return;
    }
    const anchor = document.createElement("a");
    anchor.href = source;
    anchor.download = artifact.name;
    anchor.rel = "noopener";
    anchor.click();
  };

  let body: ReactNode;
  if (!source) {
    body = <div className="artifact-preview-empty">{t("artifacts.previewUnavailable")}</div>;
  } else if (artifact.mimeType === "application/pdf") {
    body = (
      <Suspense fallback={<div className="artifact-preview-empty">{t("imagePreview.loading")}</div>}>
        <WorkspacePdfPreview url={source} title={artifact.name} />
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
  } else if (artifact.mimeType.startsWith("image/")) {
    body = <img className="artifact-preview-image" src={source} alt={artifact.name} />;
  } else if (artifact.mimeType.startsWith("audio/")) {
    body = <audio className="artifact-preview-audio" src={source} controls />;
  } else if (artifact.mimeType.startsWith("text/") || artifact.text !== undefined) {
    body = <pre className="artifact-preview-text">{artifact.text ?? decodedArtifactText(artifact)}</pre>;
  } else {
    body = <div className="artifact-preview-empty">{t("artifacts.previewUnavailable")}</div>;
  }

  return (
    <div
      className="artifact-preview-overlay"
      role="dialog"
      aria-modal="true"
      aria-label={t("artifacts.previewNamed", { name: artifact.name })}
      onClick={(event) => {
        if (event.target === event.currentTarget) onClose();
      }}
    >
      <div className="artifact-preview-shell" ref={dialogRef} tabIndex={-1}>
        <header className="artifact-preview-toolbar">
          <div>
            <strong>{artifact.name}</strong>
            <span>{artifact.mimeType}</span>
          </div>
          <div className="artifact-preview-actions">
            {source ? (
              <button type="button" onClick={download} aria-label={t("artifacts.downloadNamed", { name: artifact.name })}>
                <Download className="icon" aria-hidden="true" />
              </button>
            ) : null}
            <button type="button" onClick={onClose} aria-label={t("common.close")}>
              <X className="icon" aria-hidden="true" />
            </button>
          </div>
        </header>
        {downloadError && <p role="alert">{downloadError}</p>}
        <div className="artifact-preview-body">{body}</div>
      </div>
    </div>
  );
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
    uri,
    resource: part.resource,
    placement,
    ref: part.artifact?.ref,
    sha256: part.artifact?.sha256,
    sizeBytes: part.artifact?.size_bytes,
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
  const direct = artifactSource(artifact, cwd);
  const [source, setSource] = useState<string | undefined>(direct);
  useEffect(() => {
    const nextDirect = artifactSource(artifact, cwd);
    if (nextDirect) {
      setSource(nextDirect);
      return undefined;
    }
    const bytes = decodedArtifactBytes(artifact);
    if (!bytes && artifact.text === undefined) {
      setSource(undefined);
      return undefined;
    }
    let payload: BlobPart = artifact.text ?? "";
    if (bytes) {
      const copied = new Uint8Array(bytes.byteLength);
      copied.set(bytes);
      payload = copied.buffer;
    }
    const blob = new Blob([payload], { type: artifact.mimeType });
    const objectURL = URL.createObjectURL(blob);
    setSource(objectURL);
    return () => URL.revokeObjectURL(objectURL);
  }, [artifact, cwd]);
  return source;
}

function artifactSource(artifact: TurnArtifact, cwd?: string): string | undefined {
  if (artifact.data && (artifact.mimeType.startsWith("image/") || artifact.mimeType.startsWith("audio/"))) {
    return `data:${artifact.mimeType};base64,${artifact.data}`;
  }
  if (artifact.text !== undefined && artifact.mimeType.startsWith("image/")) {
    return `data:${artifact.mimeType};charset=utf-8,${encodeURIComponent(artifact.text)}`;
  }
  const uri = artifact.uri?.trim();
  if (!uri) return undefined;
  if (/^(https?:|data:|blob:|wuu-file:|wuu-artifact:)/i.test(uri)) return uri;
  if (isHtmlMimeType(artifact.mimeType)) return undefined;
  const path = absoluteArtifactPath(uri, cwd);
  return path && isHostRenderableMimeType(artifact.mimeType) ? renderableFileURL(path) : undefined;
}

function canPreviewArtifact(artifact: TurnArtifact): boolean {
  const previewableMime = isHostRenderableMimeType(artifact.mimeType)
    || artifact.mimeType.startsWith("audio/")
    || artifact.mimeType.startsWith("text/");
  return Boolean(
    artifact.data
    || artifact.text !== undefined
    || (artifact.uri && previewableMime),
  );
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

function renderableFileURL(path: string): string {
  const encoded = base64URL(path);
  return window.wuuRenderableFileURL?.(encoded) ?? `wuu-file://local/${encoded}`;
}

function base64URL(value: string): string {
  const bytes = new TextEncoder().encode(value);
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/g, "");
}

function decodedArtifactBytes(artifact: TurnArtifact): Uint8Array | undefined {
  if (!artifact.data) return undefined;
  try {
    const binary = atob(artifact.data);
    return Uint8Array.from(binary, (character) => character.charCodeAt(0));
  } catch {
    return undefined;
  }
}

function decodedArtifactText(artifact: TurnArtifact): string {
  const bytes = decodedArtifactBytes(artifact);
  if (!bytes) return "";
  try {
    return new TextDecoder().decode(bytes);
  } catch {
    return "";
  }
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

function isHostRenderableMimeType(mimeType: string): boolean {
  return mimeType.startsWith("image/") || mimeType === "application/pdf" || isHtmlMimeType(mimeType);
}
