import { hostSupports } from "./HostCapabilities";
import { Children, cloneElement, isValidElement, memo, useEffect, useId, useMemo, useRef, useState, useSyncExternalStore, type KeyboardEvent as ReactKeyboardEvent, type MouseEvent as ReactMouseEvent, type ReactNode } from "react";
import { Github, Globe2, Mail } from "lucide-react";
import ReactMarkdown, { defaultUrlTransform, type Components, type UrlTransform } from "react-markdown";
import rehypeRaw from "rehype-raw";
import remarkGfm from "remark-gfm";
import remarkCjkAutolinkBoundary from "./remarkCjkAutolinkBoundary";
import remarkCjkStrongBoundary from "./remarkCjkStrongBoundary";
import { useImagePreview } from "./ImagePreview";
import {
  formatWorkspaceFileTarget,
  parseLinkTarget,
  type WorkspaceFileLinkTarget,
} from "./LinkTargets";
import { MessageCopyButton } from "./MessageActions";
import { copyToClipboard, ThreadContextMenu } from "./ThreadContextMenu";
import { useI18n } from "./i18n";
import { Tooltip } from "./Tooltip";
import { currentAppliedTheme, observeAppliedTheme, type AppliedTheme } from "./Theme";
import { desktopWorkbenchController } from "./plugins/DesktopPluginRuntime";
import { WorkbenchContentRenderer } from "./plugins/Workbench";
import { highlightCode } from "./WorkspaceCodeHighlight";

type RichContentProps = {
  text?: string;
  cwd?: string;
  onOpenFile?: (path: string) => void;
  /**
   * When true, inline HTML inside the markdown source is rendered as HTML
   * instead of being escaped. Use only for trusted sources the user opened
   * themselves (e.g. workspace file previews). Chat messages stay off by
   * default so LLM output cannot inject arbitrary HTML.
   */
  allowRawHtml?: boolean;
};

export type RichTextRenderContext = {
  startOffset: number;
  endOffset: number;
  totalLength: number;
  hasCursor: boolean;
};

export type RichTextRenderer = (
  text: string,
  keyPrefix: string,
  context?: RichTextRenderContext,
) => Array<JSX.Element | string>;

type RichTextRenderOptions = {
  renderText?: RichTextRenderer;
};

type MermaidState =
  | { status: "rendering" }
  | { status: "rendered"; svg: string }
  | { status: "error"; message: string };

// The streaming cursor sentinel (CURSOR_SENTINEL in StreamingMarkdown) is a
// private-use character appended to live text. It must never reach the
// Mermaid parser, so any occurrence is stripped from extracted diagram code.
const STREAM_CURSOR_SENTINEL_PATTERN = /\uE000/g;
// While the diagram source is still streaming, render attempts are paced: a
// debounce rides out chunk bursts, and a minimum interval between renders
// keeps a fast stream from replacing the whole SVG on every chunk (each
// replacement re-runs the layout, which reads as flicker).
const STREAM_MERMAID_RENDER_DEBOUNCE_MS = 300;
const STREAM_MERMAID_RENDER_INTERVAL_MS = 500;
// Rendered SVGs are cached by (theme, code) so a diagram that moves from the
// streaming tail into a stable block (fence closed + blank line) remounts
// with its picture already committed instead of flashing the placeholder.
// Only streaming renders populate the cache; settled messages always render.
const mermaidSvgCache = new Map<string, string>();
const MERMAID_SVG_CACHE_LIMIT = 64;

function cacheMermaidSvg(key: string, svg: string): void {
  mermaidSvgCache.delete(key);
  mermaidSvgCache.set(key, svg);
  if (mermaidSvgCache.size > MERMAID_SVG_CACHE_LIMIT) {
    const oldest = mermaidSvgCache.keys().next().value;
    if (oldest !== undefined) {
      mermaidSvgCache.delete(oldest);
    }
  }
}

/**
 * The code actually fed to the renderer. While the fence is still open the
 * source may end with a partial line that keeps growing; feeding it to the
 * parser would fail on every chunk. When the last line is not
 * newline-terminated it is unfinished, so only the completed lines are
 * rendered and the diagram grows line by line. A trailing newline (kept by
 * the caller while streaming) means the last line is complete and must be
 * included.
 */
function mermaidCodeToRender(code: string, streaming: boolean): string {
  let codeToRender = code;
  if (streaming && !code.endsWith("\n")) {
    const lastLineBreak = code.lastIndexOf("\n");
    codeToRender = lastLineBreak < 0 ? "" : code.slice(0, lastLineBreak + 1);
  }
  if (codeToRender.endsWith("\n")) {
    codeToRender = codeToRender.slice(0, -1);
  }
  return codeToRender;
}

function mermaidRenderKey(theme: AppliedTheme, codeToRender: string): string {
  return `${theme}\u0000${codeToRender}`;
}

const IMAGE_FILE_PATTERN = /\.(apng|avif|gif|jpe?g|png|svg|webp)(?:[?#].*)?$/i;

export const RichContent = memo(function RichContent({
  text = "",
  cwd,
  onOpenFile,
  allowRawHtml = false
}: RichContentProps): JSX.Element {
  useSyncExternalStore(
    desktopWorkbenchController.subscribe,
    desktopWorkbenchController.getSnapshot,
  );
  const fallback = (
    <div className="rich-content">
      <MarkdownContent
        text={text}
        cwd={cwd}
        onOpenFile={onOpenFile}
        allowRawHtml={allowRawHtml}
      />
    </div>
  );
  return (
    <WorkbenchContentRenderer
      controller={desktopWorkbenchController}
      category="message"
      contentType="text/markdown"
      content={text}
      metadata={{ cwd, allowRawHtml }}
      fallback={fallback}
    />
  );
});

function MarkdownContentView({
  text,
  cwd,
  renderText,
  onOpenFile,
  renderMermaid = true,
  mermaidStreaming = false,
  allowRawHtml = false
}: {
  text: string;
  cwd?: string;
  renderText?: RichTextRenderer;
  onOpenFile?: (path: string) => void;
  renderMermaid?: boolean;
  /** The diagram source is still streaming: render offscreen and commit only
   * successful results (see MermaidDiagram). */
  mermaidStreaming?: boolean;
  allowRawHtml?: boolean;
}): JSX.Element {
  const components = useMemo(
    () => markdownComponents(cwd, renderText, renderMermaid, mermaidStreaming, onOpenFile),
    [cwd, renderText, renderMermaid, mermaidStreaming, onOpenFile]
  );
  return (
    <ReactMarkdown
      components={components}
      // CommonMark links and GFM autolinks are the only text-to-link rules.
      // File mentions stay literal; file navigation uses explicit link targets.
      remarkPlugins={[remarkGfm, remarkCjkAutolinkBoundary, remarkCjkStrongBoundary]}
      rehypePlugins={allowRawHtml ? [rehypeRaw, rehypeHeadingIDs] : [rehypeHeadingIDs]}
      urlTransform={richMarkdownUrlTransform}
    >
      {text}
    </ReactMarkdown>
  );
}

export const MarkdownContent = memo(MarkdownContentView);

type CodeElementProps = {
  className?: string;
  children?: ReactNode;
};

function markdownComponents(
  cwd: string | undefined,
  renderText: RichTextRenderer | undefined,
  renderMermaid: boolean,
  mermaidStreaming: boolean,
  onOpenFile: ((path: string) => void) | undefined
): Components {
  const richTextOptions: RichTextRenderOptions = {
    renderText,
  };
  return {
    p({ children }) {
      return (
        <p className="rich-paragraph">{renderMarkdownText(children, richTextOptions, "p")}</p>
      );
    },
    // Heading levels share the same flat `rich-heading` shape so streamed
    // chat output stays scannable, but they also expose a level modifier so
    // the workspace file preview can render a proper document outline
    // (h1 → big title with underline; h2 → section heading; h3 → sub-head).
    h1({ children, id }) {
      return (
        <p className="rich-heading rich-heading--h1" id={id}>
          {renderMarkdownText(children, richTextOptions, "h1")}
        </p>
      );
    },
    h2({ children, id }) {
      return (
        <p className="rich-heading rich-heading--h2" id={id}>
          {renderMarkdownText(children, richTextOptions, "h2")}
        </p>
      );
    },
    h3({ children, id }) {
      return (
        <p className="rich-heading rich-heading--h3" id={id}>
          {renderMarkdownText(children, richTextOptions, "h3")}
        </p>
      );
    },
    h4({ children, id }) {
      return (
        <p className="rich-heading rich-heading--h4" id={id}>
          {renderMarkdownText(children, richTextOptions, "h4")}
        </p>
      );
    },
    h5({ children, id }) {
      return (
        <p className="rich-heading rich-heading--h5" id={id}>
          {renderMarkdownText(children, richTextOptions, "h5")}
        </p>
      );
    },
    h6({ children, id }) {
      return (
        <p className="rich-heading rich-heading--h6" id={id}>
          {renderMarkdownText(children, richTextOptions, "h6")}
        </p>
      );
    },
    a({ href, title, children }) {
      const inner = renderMarkdownText(children, richTextOptions, "a");
      const target = parseLinkTarget(href);
      if (target.kind === "workspace-file") {
        if (!onOpenFile) {
          return <span>{inner}</span>;
        }
        return (
          <RichFileLink
            key={`a-${formatWorkspaceFileTarget(target)}`}
            display={inner}
            cwd={cwd}
            target={target}
            onOpenFile={onOpenFile}
          />
        );
      }
      if (target.kind === "external") {
        return <RichWebLink href={target.url} title={title}>{inner}</RichWebLink>;
      }
      if (target.kind === "anchor") {
        return <RichAnchorLink id={target.id} title={title}>{inner}</RichAnchorLink>;
      }
      return <span>{inner}</span>;
    },
    img({ src, alt }) {
      if (!src) {
        return null;
      }
      return <RichImage source={src} alt={alt ?? ""} cwd={cwd} inline />;
    },
    pre({ children }) {
      const child = Children.toArray(children)[0];
      if (isValidElement<CodeElementProps>(child)) {
        const language = languageFromClassName(child.props.className);
        if (language === "mermaid" && renderMermaid) {
          const diagramCode = reactNodeText(child.props.children).replace(
            STREAM_CURSOR_SENTINEL_PATTERN,
            "",
          );
          return (
            <MermaidDiagram
              // Keep the trailing newline while streaming: it is the only
              // signal that the last line is complete, which lets the
              // partial-line render skip just the truly unfinished line.
              code={mermaidStreaming ? diagramCode : diagramCode.replace(/\n$/, "")}
              streaming={mermaidStreaming}
            />
          );
        }
        const displayedCode = reactNodeText(child.props.children);
        const code = displayedCode.replace(/\n$/, "");
        return (
          <RichCodeBlock code={code} displayedCode={displayedCode} language={language} />
        );
      }
      return <pre className="rich-code">{children}</pre>;
    },
    code({ className, children }) {
      return <code className={className}>{renderMarkdownText(children, richTextOptions, "code")}</code>;
    },
    li({ children }) {
      return (
        <li>
          {renderMarkdownText(children, richTextOptions, "li")}
        </li>
      );
    },
    table({ children }) {
      return (
        <div className="rich-table-wrap">
          <table>{children}</table>
        </div>
      );
    },
    th({ children }) {
      return <th>{renderMarkdownText(children, richTextOptions, "th")}</th>;
    },
    td({ children }) {
      return <td>{renderMarkdownText(children, richTextOptions, "td")}</td>;
    },
    blockquote({ children }) {
      return <blockquote className="rich-blockquote">{renderMarkdownText(children, richTextOptions, "blockquote")}</blockquote>;
    },
    hr() {
      return <hr className="rich-rule" />;
    }
  };
}

function RichCodeBlock({
  code,
  displayedCode,
  language
}: {
  code: string;
  displayedCode: string;
  language: string;
}): JSX.Element {
  const { t } = useI18n();
  const highlighted = useMemo(
    () => highlightCode(language, displayedCode),
    [displayedCode, language]
  );
  const highlightedCode = (
    <code
      className={`hljs language-${highlighted.language}`}
      dangerouslySetInnerHTML={{ __html: highlighted.html }}
    />
  );
  // Two layouts:
  //   - With a language tag, the header row carries the language label
  //     and the copy button on the same baseline. Keeps the chrome
  //     out of the code surface so long blocks don't paint the header
  //     into a scrollable overflow box.
  //   - Without a language tag (anonymous fenced block like ```` ``` ````),
  //     there is no header row at all. The copy button floats over the
  //     top-right of the <pre> instead — same `RichCodeBlock` styling,
  //     same always-visible opacity behaviour, just anchored to the code
  //     surface instead of a sibling row. The <pre> gets extra right
  //     padding (see CSS) so short lines don't slide under the button.
  if (language) {
    return (
      <div className="rich-code-block">
        <div className="rich-code-header">
          <span className="rich-code-language">{language}</span>
          <MessageCopyButton
            getText={() => code}
            className="rich-code-copy"
            iconSize={13}
            idleLabel={t("rich.copyCode")}
            copiedLabel={t("rich.codeCopied")}
            failedLabel={t("rich.copyFailed")}
          />
        </div>
        <pre className="rich-code">
          {highlightedCode}
        </pre>
      </div>
    );
  }
  return (
    <div className="rich-code-block rich-code-block--no-header">
      <pre className="rich-code">
        {highlightedCode}
      </pre>
      <MessageCopyButton
        getText={() => code}
        className="rich-code-copy"
        iconSize={13}
        idleLabel={t("rich.copyCode")}
        copiedLabel={t("rich.codeCopied")}
        failedLabel={t("rich.copyFailed")}
      />
    </div>
  );
}

function renderMarkdownText(
  children: ReactNode,
  options: RichTextRenderOptions,
  keyPrefix: string
): ReactNode {
  const flattenedText = markdownNodeText(children);
  return renderMarkdownTextWithContext(children, options, keyPrefix, {
    offset: 0,
    totalLength: flattenedText.length,
    hasCursor: flattenedText.includes("\uE000"),
  });
}

type MarkdownTextTraversal = {
  offset: number;
  totalLength: number;
  hasCursor: boolean;
};

function renderMarkdownTextWithContext(
  children: ReactNode,
  options: RichTextRenderOptions,
  keyPrefix: string,
  traversal: MarkdownTextTraversal,
): ReactNode {
  const renderText = options.renderText;
  if (!renderText) {
    return children;
  }
  return Children.toArray(children).flatMap((child, index): ReactNode[] => {
    const childKey = `${keyPrefix}-${index}`;
    if (typeof child === "string" || typeof child === "number") {
      const text = String(child);
      const startOffset = traversal.offset;
      traversal.offset += text.length;
      return renderText(text, childKey, {
        startOffset,
        endOffset: traversal.offset,
        totalLength: traversal.totalLength,
        hasCursor: traversal.hasCursor,
      });
    }
    if (!isValidElement<{ children?: ReactNode }>(child) || child.props.children === undefined) {
      return [child];
    }
    return [
      cloneElement(child, {
        children: renderMarkdownTextWithContext(
          child.props.children,
          options,
          childKey,
          traversal,
        )
      })
    ];
  });
}

function markdownNodeText(node: ReactNode): string {
  return Children.toArray(node).map((child) => {
    if (typeof child === "string" || typeof child === "number") {
      return String(child);
    }
    if (isValidElement<{ children?: ReactNode }>(child)) {
      return markdownNodeText(child.props.children);
    }
    return "";
  }).join("");
}

function RichFileLink({
  display,
  cwd,
  target,
  onOpenFile
}: {
  display: ReactNode;
  cwd?: string;
  target: WorkspaceFileLinkTarget;
  onOpenFile: (path: string) => void;
}): JSX.Element {
  const { t } = useI18n();
  const reference = formatWorkspaceFileTarget(target);
  const [contextMenu, setContextMenu] = useState<{ x: number; y: number } | null>(null);
  const path = target.path;
  const normalizedPath = path.replace(/\\/g, "/");
  const absolutePath = isAbsolutePath(normalizedPath)
    ? path
    : cwd
      ? `${cwd.replace(/[\\/]+$/, "")}/${path.replace(/^[/\\]+/, "")}`
      : path;
  const fileName = normalizedPath.split("/").filter(Boolean).at(-1) ?? path;
  const closeContextMenu = (): void => setContextMenu(null);
  return (
    <>
      <Tooltip content={t("rich.openFile", { reference })}>
        <button
          type="button"
          className="rich-link rich-file-link"
          onClick={() => onOpenFile(reference)}
          onContextMenu={(event) => {
            event.preventDefault();
            event.stopPropagation();
            setContextMenu({ x: event.clientX, y: event.clientY });
          }}
        >
          <span className="rich-link-label">{display}</span>
        </button>
      </Tooltip>
      {contextMenu ? (
        <ThreadContextMenu
          x={contextMenu.x}
          y={contextMenu.y}
          onClose={closeContextMenu}
          items={[
            { label: t("workspace.files.openFile"), onSelect: () => onOpenFile(reference) },
            { separator: true },
            { label: t("workspace.files.copyPath"), onSelect: () => copyToClipboard(absolutePath) },
            { label: t("workspace.files.copyRelativePath"), onSelect: () => copyToClipboard(path) },
            { label: t("workspace.files.copyFileName"), onSelect: () => copyToClipboard(fileName) },
            ...(hostSupports("revealWorkspaceItem")
              ? [{ separator: true } as const, {
                  label: t("workspace.files.revealInFileManager"),
                  onSelect: () => window.wuu.revealWorkspaceItem(absolutePath),
                }]
              : []),
          ]}
        />
      ) : null}
    </>
  );
}

function isAbsolutePath(path: string): boolean {
  return path.startsWith("/") || path.startsWith("//") || /^[a-z]:\//i.test(path);
}

function RichAnchorLink({
  id,
  title,
  children,
}: {
  id: string;
  title?: string;
  children: ReactNode;
}): JSX.Element {
  return (
    <Tooltip content={title}>
      <a
        className="rich-link rich-anchor-link"
        href={`#${encodeURIComponent(id)}`}
        onClick={(event) => {
          event.preventDefault();
          const root = event.currentTarget.closest(".rich-content");
          const target = Array.from(root?.querySelectorAll<HTMLElement>("[id]") ?? [])
            .find((element) => element.id === id);
          target?.scrollIntoView({ block: "start" });
        }}
      >
        <span className="rich-link-label">{children}</span>
      </a>
    </Tooltip>
  );
}

function RichWebLink({
  href,
  title,
  children
}: {
  href: string;
  title?: string;
  children: ReactNode;
}): JSX.Element {
  const openLink = (event: ReactMouseEvent<HTMLAnchorElement>): void => {
    if (event.button > 1) {
      return;
    }
    event.preventDefault();
    void window.wuu?.openExternal?.(href).catch((error) => {
      console.error(`[rich-link] failed to open ${href}`, error);
    });
  };
  return (
    <Tooltip content={title}>
      <a
        className="rich-link rich-web-link"
        href={href}
        rel="noopener noreferrer"
        onAuxClick={openLink}
        onClick={openLink}
      >
        <span className="rich-link-content">
          <RichWebLinkIcon href={href} />
          <span className="rich-link-label">{children}</span>
        </span>
      </a>
    </Tooltip>
  );
}

function RichWebLinkIcon({ href }: { href: string }): JSX.Element {
  if (/^mailto:/i.test(href)) {
    return (
      <span className="rich-link-icon" aria-hidden="true">
        <Mail className="icon-xs" />
      </span>
    );
  }

  const host = linkHost(href);
  if (host && /(^|\.)github\.com$/i.test(host)) {
    return (
      <span className="rich-link-icon" aria-hidden="true">
        <Github className="icon-xs" />
      </span>
    );
  }

  const favicon = faviconSource(href);
  return (
    <span className="rich-link-icon rich-link-favicon-frame" aria-hidden="true">
      <Globe2 className="icon-xs rich-link-fallback-icon" />
      {favicon ? (
        <img
          className="rich-link-favicon"
          src={favicon}
          alt=""
          loading="lazy"
          onError={(event) => {
            event.currentTarget.style.display = "none";
          }}
        />
      ) : null}
    </span>
  );
}

function faviconSource(href: string): string | undefined {
  try {
    const url = new URL(href);
    if (url.protocol !== "http:" && url.protocol !== "https:") {
      return undefined;
    }
    return `${url.origin}/favicon.ico`;
  } catch {
    return undefined;
  }
}

function linkHost(href: string): string | undefined {
  try {
    return new URL(href).hostname;
  } catch {
    return undefined;
  }
}

function languageFromClassName(className: string | undefined): string {
  const match = className?.match(/(?:^|\s)language-([^\s]+)/);
  return match?.[1]?.toLowerCase() ?? "";
}

function reactNodeText(node: ReactNode): string {
  if (typeof node === "string" || typeof node === "number") {
    return String(node);
  }
  if (Array.isArray(node)) {
    return node.map(reactNodeText).join("");
  }
  return "";
}

const richMarkdownUrlTransform: UrlTransform = (url, key) => {
  if (key === "href") {
    return parseLinkTarget(url).kind === "invalid" ? "" : url;
  }
  return defaultUrlTransform(url);
};

type MarkdownASTNode = {
  type?: string;
  tagName?: string;
  value?: string;
  properties?: Record<string, unknown>;
  children?: MarkdownASTNode[];
};

function rehypeHeadingIDs() {
  return (tree: MarkdownASTNode): void => {
    const slugCounts = new Map<string, number>();
    visitMarkdownAST(tree, (node) => {
      if (!/^h[1-6]$/.test(node.tagName ?? "")) {
        return;
      }
      const baseSlug = markdownHeadingSlug(markdownASTText(node));
      if (!baseSlug) {
        return;
      }
      const count = slugCounts.get(baseSlug) ?? 0;
      slugCounts.set(baseSlug, count + 1);
      node.properties = {
        ...node.properties,
        id: count === 0 ? baseSlug : `${baseSlug}-${count}`,
      };
    });
  };
}

function visitMarkdownAST(node: MarkdownASTNode, visitor: (node: MarkdownASTNode) => void): void {
  visitor(node);
  node.children?.forEach((child) => visitMarkdownAST(child, visitor));
}

function markdownASTText(node: MarkdownASTNode): string {
  if (node.type === "text") {
    return node.value ?? "";
  }
  return node.children?.map(markdownASTText).join("") ?? "";
}

function markdownHeadingSlug(value: string): string {
  return value
    .trim()
    .toLowerCase()
    .replace(/[^\p{L}\p{M}\p{N}\s_-]/gu, "")
    .replace(/\s+/g, "-");
}

function RichImage({
  source,
  alt,
  cwd,
  inline = false,
}: {
  source: string;
  alt: string;
  cwd: string | undefined;
  inline?: boolean;
}): JSX.Element {
  const { t } = useI18n();
  const resolvedSource = resolveImageSource(source, cwd);
  const [failedSource, setFailedSource] = useState<string | null>(null);
  const { openPreview } = useImagePreview();
  if (failedSource === resolvedSource) {
    return <></>;
  }
  const titleText = imageTarget(source);
  const handleActivate = (): void => {
    openPreview({ src: resolvedSource, alt, title: titleText });
  };
  const handleKeyDown = (event: ReactKeyboardEvent<HTMLImageElement>): void => {
    if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      handleActivate();
    }
  };
  const image = (
    <Tooltip content={titleText}>
      <img
        className="rich-image"
        src={resolvedSource}
        alt={alt}
        loading="lazy"
        role="button"
        tabIndex={0}
        aria-label={
          alt ? t("rich.enlargeNamed", { alt }) : t("rich.enlargeImage")
        }
        onClick={handleActivate}
        onKeyDown={handleKeyDown}
        onError={() => setFailedSource(resolvedSource)}
      />
    </Tooltip>
  );
  if (inline) {
    return <span className="rich-image-block inline">{image}</span>;
  }
  return (
    <figure className="rich-image-block">
      {image}
    </figure>
  );
}

function stripWrappers(value: string): string {
  const pairs: Array<[string, string]> = [
    ["`", "`"],
    ['"', '"'],
    ["'", "'"],
    ["<", ">"]
  ];
  for (const [open, close] of pairs) {
    if (value.startsWith(open) && value.endsWith(close)) {
      return value.slice(open.length, -close.length).trim();
    }
  }
  return value;
}

export function resolveImageSource(rawSource: string, cwd: string | undefined): string {
  const source = imageTarget(rawSource);
  const renderableWuuFileURL = renderableBrowserFileURLFromWuuFile(source);
  if (renderableWuuFileURL) {
    return renderableWuuFileURL;
  }
  if (isWebImageSource(source)) {
    return source;
  }
  if (source.startsWith("file://")) {
    return renderableFileURL(fileURLPath(source));
  }
  if (source.startsWith("~/")) {
    return renderableFileURL(resolveHomePath(cwd, source));
  }
  if (source.startsWith("/") || source.startsWith("./") || source.startsWith("../")) {
    return renderableFileURL(source.startsWith("/") ? source : resolveRelativePath(cwd, source));
  }
  if (cwd && IMAGE_FILE_PATTERN.test(source)) {
    return renderableFileURL(resolveRelativePath(cwd, source));
  }
  return source;
}

export function imageTarget(rawSource: string): string {
  let source = stripWrappers(rawSource.trim());
  if (source.startsWith("<") && source.endsWith(">")) {
    return source.slice(1, -1).trim();
  }
  const titleMatch = source.match(/^(.*?)(?:\s+["'][^"']*["'])$/);
  if (titleMatch) {
    source = titleMatch[1].trim();
  }
  return source;
}

function isWebImageSource(source: string): boolean {
  return /^(https?:|data:image\/|blob:|wuu-file:)/i.test(source);
}

function fileURLPath(source: string): string {
  try {
    return decodeURIComponent(new URL(source).pathname);
  } catch {
    return source.replace(/^file:\/\//, "");
  }
}

function resolveRelativePath(cwd: string | undefined, relativePath: string): string {
  const base = cwd ?? "/";
  const parts = `${base}/${relativePath}`.split("/");
  const stack: string[] = [];
  for (const part of parts) {
    if (!part || part === ".") {
      continue;
    }
    if (part === "..") {
      stack.pop();
      continue;
    }
    stack.push(part);
  }
  return `/${stack.join("/")}`;
}

function resolveHomePath(cwd: string | undefined, path: string): string {
  const homePath = cwd?.match(/^\/Users\/[^/]+/)?.[0] ?? "";
  return `${homePath}/${path.slice(2)}`;
}

function renderableFileURL(filePath: string): string {
  const encodedPath = base64URL(filePath);
  return window.wuuRenderableFileURL?.(encodedPath) ?? `wuu-file://local/${encodedPath}`;
}

function renderableBrowserFileURLFromWuuFile(source: string): string | undefined {
  if (!/^wuu-file:/i.test(source)) {
    return undefined;
  }
  try {
    const url = new URL(source);
    if (url.hostname !== "local") {
      return undefined;
    }
    const encodedPath = url.pathname.replace(/^\/+/, "");
    return encodedPath ? window.wuuRenderableFileURL?.(encodedPath) : undefined;
  } catch {
    return undefined;
  }
}

function base64URL(value: string): string {
  const bytes = new TextEncoder().encode(value);
  let binary = "";
  for (const byte of bytes) {
    binary += String.fromCharCode(byte);
  }
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/g, "");
}

function MermaidDiagram({
  code,
  streaming = false,
}: {
  code: string;
  /**
   * While the diagram source is still streaming (an open fence in the live
   * tail), render attempts happen offscreen on a debounced cadence and only
   * successful results are committed to the visible tree. Failed partial
   * renders are dropped silently so the last coherent diagram stays visible
   * and no error flashes into the stream; once this flips off, the final
   * render runs immediately and errors are surfaced as before.
   */
  streaming?: boolean;
}): JSX.Element {
  const { locale, t } = useI18n();
  const { openPreview } = useImagePreview();
  const reactID = useId();
  const diagramID = useMemo(() => `wuu-mermaid-${reactID.replace(/[^a-zA-Z0-9_-]/g, "")}-${hashString(code)}`, [code, reactID]);
  const [theme, setTheme] = useState<AppliedTheme>(currentAppliedTheme);
  // Mount-time snapshot only: if the same (theme, code) diagram was already
  // rendered while streaming, start with that picture committed instead of
  // the placeholder. Refs below mirror the snapshot so the first effect run
  // skips the identical render instead of flashing.
  const mountKey = mermaidRenderKey(
    currentAppliedTheme(),
    mermaidCodeToRender(code, streaming),
  );
  const [state, setState] = useState<MermaidState>(() => {
    const svg = mermaidSvgCache.get(mountKey);
    return svg ? { status: "rendered", svg } : { status: "rendering" };
  });
  const renderAttemptRef = useRef(0);
  const hasRenderedRef = useRef(mermaidSvgCache.has(mountKey));
  const lastRenderedKeyRef = useRef(
    mermaidSvgCache.has(mountKey) ? mountKey : "",
  );
  const lastRenderStartedAtRef = useRef(0);

  useEffect(() => observeAppliedTheme(setTheme), []);

  useEffect(() => {
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | undefined;

    const codeToRender = mermaidCodeToRender(code, streaming);
    const renderKey = mermaidRenderKey(theme, codeToRender);
    if (renderKey === lastRenderedKeyRef.current) {
      // An identical diagram is already committed (e.g. chunks that only
      // changed the partial line, or a settle with the same source).
      return;
    }
    if (!codeToRender) {
      // Nothing renderable yet; keep the placeholder until lines complete.
      return;
    }

    const renderDiagram = async (): Promise<void> => {
      lastRenderStartedAtRef.current = performance.now();
      const attempt = renderAttemptRef.current + 1;
      renderAttemptRef.current = attempt;
      try {
        const mermaid = (await import("./MermaidRuntime")).default;
        mermaid.initialize({
          startOnLoad: false,
          securityLevel: "strict",
          theme: "base",
          themeVariables: mermaidThemeVariables(theme),
        });
        // A fresh id per attempt keeps a failed render's internal DOM from
        // colliding with the next attempt's.
        const result = await mermaid.render(`${diagramID}-${theme}-${attempt}`, codeToRender);
        if (cancelled || attempt !== renderAttemptRef.current) {
          return;
        }
        hasRenderedRef.current = true;
        lastRenderedKeyRef.current = renderKey;
        if (streaming) {
          // Only streaming renders populate the cache: a later remount of
          // the same diagram (tail → stable block promotion) can reuse the
          // picture. Settled messages always render fresh.
          cacheMermaidSvg(renderKey, result.svg);
        }
        setState({ status: "rendered", svg: result.svg });
      } catch (error) {
        if (cancelled || attempt !== renderAttemptRef.current) {
          return;
        }
        if (streaming) {
          // Partial/invalid source: keep the last committed diagram (or the
          // placeholder if nothing has rendered yet). Never record the key,
          // so the settle-time render still re-attempts this source.
          return;
        }
        setState({
          status: "error",
          message:
            error instanceof Error ? error.message : t("rich.mermaidFailed"),
        });
      }
    };

    if (streaming) {
      // Offscreen partial render while the source keeps growing. The debounce
      // rides out chunk bursts; the minimum interval guarantees at most one
      // whole-SVG replacement per interval so fast streams stay calm.
      const elapsed = performance.now() - lastRenderStartedAtRef.current;
      const delay = Math.max(
        STREAM_MERMAID_RENDER_DEBOUNCE_MS,
        STREAM_MERMAID_RENDER_INTERVAL_MS - elapsed,
      );
      timer = setTimeout(() => {
        void renderDiagram();
      }, delay);
    } else {
      // Keep the last committed diagram visible while the final render is in
      // flight so settling never flashes the placeholder.
      if (!hasRenderedRef.current) {
        setState({ status: "rendering" });
      }
      void renderDiagram();
    }

    return () => {
      cancelled = true;
      if (timer !== undefined) {
        clearTimeout(timer);
      }
    };
  }, [code, diagramID, locale, streaming, theme]);

  if (state.status === "rendered") {
    return (
      <button
        type="button"
        className="rich-mermaid rich-mermaid-diagram"
        aria-label={t("rich.enlargeDiagram")}
        title={t("rich.enlargeDiagram")}
        onClick={() => openPreview({
          svg: state.svg,
          alt: t("rich.mermaidDiagram"),
          title: t("rich.mermaidDiagram"),
        })}
        dangerouslySetInnerHTML={{ __html: state.svg }}
      />
    );
  }
  if (state.status === "error") {
    return (
      <div className="rich-mermaid rich-mermaid-error">
        <span>{state.message}</span>
        <pre>
          <code>{code}</code>
        </pre>
      </div>
    );
  }
  return (
    <div className="rich-mermaid rich-mermaid-loading">
      {t("rich.renderingDiagram")}
    </div>
  );
}

function mermaidThemeVariables(theme: AppliedTheme): Record<string, string> {
  return {
    background: theme === "dark" ? "#1d2024" : "#ffffff",
    primaryColor: theme === "dark" ? "#24282c" : "#eef2f0",
    primaryTextColor: theme === "dark" ? "#e4e6e8" : "#202427",
    primaryBorderColor: theme === "dark" ? "#3a4046" : "#ccd6d0",
    lineColor: theme === "dark" ? "#a9aeb3" : "#6f7478",
    fontFamily: "Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, Segoe UI, sans-serif",
  };
}

function hashString(value: string): string {
  let hash = 0;
  for (let index = 0; index < value.length; index += 1) {
    hash = (hash * 31 + value.charCodeAt(index)) >>> 0;
  }
  return hash.toString(36);
}
