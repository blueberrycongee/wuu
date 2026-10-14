import { WorkspaceFileSource, readWorkspaceFileInContext, workspaceFileContextKey } from "./WorkspacePaths";
import { PENDING_STREAM_LINK } from "./StreamingMarkdownMend";
import { hostSupports } from "./HostCapabilities";
import { Children, createContext, useContext, cloneElement, isValidElement, memo, useEffect, useId, useMemo, useRef, useState, useSyncExternalStore, type KeyboardEvent as ReactKeyboardEvent, type MouseEvent as ReactMouseEvent, type ReactNode } from "react";
import { Github, Globe2, Mail } from "./WuuIcons";
import ReactMarkdown, { defaultUrlTransform, type Components, type UrlTransform } from "react-markdown";
import rehypeRaw from "rehype-raw";
import remarkGfm from "remark-gfm";
import remarkParse from "remark-parse";
import { unified } from "unified";
import { toMarkdown } from "mdast-util-to-markdown";
import type { Definition, Root as MarkdownRoot } from "mdast";
import remarkCjkAutolinkBoundary from "./remarkCjkAutolinkBoundary";
import remarkCjkStrongBoundary from "./remarkCjkStrongBoundary";
import { useImagePreview } from "./ImagePreview";
import { useImagePreviewRegistration } from "./ImagePreviewGallery";
import {
  formatWorkspaceFileTarget,
  parseLinkTarget,
  type WorkspaceFileLinkTarget,
} from "./LinkTargets";
import { MessageCopyButton } from "./MessageActions";
import { copyToClipboard, ThreadContextMenu } from "./ThreadContextMenu";
import { useI18n } from "./i18n";
import { Tooltip } from "./Tooltip";
import {
  openExternalURL,
  prefersSystemBrowser,
  useWorkspaceBrowserOpen,
  workspaceBrowserClickModifiers,
} from "./WorkspaceBrowserOpen";
import { currentAppliedTheme, observeAppliedTheme, type AppliedTheme } from "./Theme";
import { desktopWorkbenchController } from "./plugins/DesktopPluginRuntime";
import { WorkbenchContentRenderer } from "./plugins/Workbench";
import { rehypeFileSelectionMapping, type SelectionASTNode } from "./FileSelectionMapping";
import {
  CODE_HIGHLIGHT_CHAR_LIMIT,
  CODE_HIGHLIGHT_HARD_LIMIT,
  highlightCode,
  shouldHighlightCode,
} from "./WorkspaceCodeHighlight";

const markdownRemarkPlugins = [remarkGfm, remarkCjkAutolinkBoundary, remarkCjkStrongBoundary];

type RichContentProps = {
  text?: string;
  cwd?: string;
  onOpenFile?: (path: string) => void;
  onOpenURL?: (url: string, event?: ReactMouseEvent<HTMLElement>) => void;
  /**
   * When true, inline HTML inside the markdown source is rendered as HTML
   * instead of being escaped. Use only for trusted sources the user opened
   * themselves (e.g. workspace file previews). Chat messages stay off by
   * default so LLM output cannot inject arbitrary HTML.
   */
  allowRawHtml?: boolean;
  sourceMapping?: boolean;
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

const MermaidStreamingContext = createContext(false);
// The code block whose source reaches `end` hosts the streaming cursor. The
// cursor travels through context so the Markdown components keep a stable
// identity while the source grows.
const CodeCursorContext = createContext<{ end: number; cursor: ReactNode } | undefined>(undefined);

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
  onOpenURL,
  allowRawHtml = false,
  sourceMapping = false,
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
        onOpenURL={onOpenURL}
        allowRawHtml={allowRawHtml}
        sourceMapping={sourceMapping}
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
  onOpenURL,
  renderMermaid = true,
  mermaidStreaming = false,
  allowRawHtml = false,
  sourceMapping = false,
  documentContext,
  codeCursor,
}: {
  text: string;
  cwd?: string;
  renderText?: RichTextRenderer;
  onOpenFile?: (path: string) => void;
  onOpenURL?: (url: string, event?: ReactMouseEvent<HTMLElement>) => void;
  renderMermaid?: boolean;
  /** The diagram source is still streaming: render offscreen and commit only
   * successful results (see MermaidDiagram). */
  mermaidStreaming?: boolean;
  allowRawHtml?: boolean;
  sourceMapping?: boolean;
  documentContext?: MarkdownFragmentContext;
  /** Rendered after the last code line when a code block ends the source. */
  codeCursor?: ReactNode;
}): JSX.Element {
  const components = useMemo(
    () => markdownComponents(cwd, renderText, renderMermaid, onOpenFile, onOpenURL, sourceMapping),
    [cwd, renderText, renderMermaid, onOpenFile, onOpenURL, sourceMapping]
  );
  const source = documentContext?.referenceDefinitions ? `${documentContext.referenceDefinitions}\n${text}` : text;
  const sourceEnd = source.trimEnd().length;
  const codeCursorTarget = useMemo(
    () => codeCursor ? { end: sourceEnd, cursor: codeCursor } : undefined,
    [codeCursor, sourceEnd]
  );
  return (
    <MermaidStreamingContext.Provider value={mermaidStreaming}>
      <CodeCursorContext.Provider value={codeCursorTarget}>
        <ReactMarkdown
          components={components}
          // CommonMark links and GFM autolinks are the only text-to-link rules.
          // File mentions stay literal; file navigation uses explicit link targets.
          remarkPlugins={markdownRemarkPlugins}
          rehypePlugins={[...(allowRawHtml ? [rehypeRaw] : []), [rehypeHeadingIDs, documentContext?.headingIDs], ...(sourceMapping ? [rehypeFileSelectionMapping] : [])]}
          urlTransform={richMarkdownUrlTransform}
        >
          {source}
        </ReactMarkdown>
      </CodeCursorContext.Provider>
    </MermaidStreamingContext.Provider>
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
  onOpenFile: ((path: string) => void) | undefined,
  onOpenURL: ((url: string, event?: ReactMouseEvent<HTMLElement>) => void) | undefined,
  sourceMapping: boolean,
): Components {
  const richTextOptions: RichTextRenderOptions = {
    renderText,
  };
  const components: Components = {
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
      if (href === PENDING_STREAM_LINK) return <span>{inner}</span>;
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
        return <RichWebLink href={target.url} title={title} onOpenURL={onOpenURL}>{inner}</RichWebLink>;
      }
      if (target.kind === "anchor") {
        return <RichAnchorLink id={target.id} title={title}>{inner}</RichAnchorLink>;
      }
      return <span>{inner}</span>;
    },
    img({ src, alt }) {
      if (src === PENDING_STREAM_LINK) return <span>{alt}</span>;
      if (!src) {
        return null;
      }
      return <RichImage source={src} alt={alt ?? ""} cwd={cwd} inline />;
    },
    pre({ children, node }) {
      const codeCursorTarget = useContext(CodeCursorContext);
      const child = Children.toArray(children)[0];
      if (isValidElement<CodeElementProps>(child)) {
        const language = languageFromClassName(child.props.className);
        if (language === "mermaid" && renderMermaid) {
          const diagramCode = reactNodeText(child.props.children).replace(
            STREAM_CURSOR_SENTINEL_PATTERN,
            "",
          );
          return <MermaidDiagram code={diagramCode} />;
        }
        const displayedCode = reactNodeText(child.props.children);
        const code = displayedCode.replace(/\n$/, "");
        // An unclosed fence ends at the source end; a closed one ends at its
        // closing marker, which is the end of the whitespace-trimmed source.
        const endsSource = codeCursorTarget !== undefined &&
          (node?.position?.end.offset ?? -1) >= codeCursorTarget.end;
        return (
          <RichCodeBlock
            code={code}
            displayedCode={displayedCode}
            language={language}
            cursor={endsSource ? codeCursorTarget.cursor : undefined}
          />
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
  if (!sourceMapping) return components;
  // Custom renderers replace the AST element, so carry its source boundaries
  // onto their actual root (including highlighted code and rendered diagrams).
  return Object.fromEntries(Object.entries(components).map(([tag, component]) => [tag,
    (props: { node?: SelectionASTNode }) => {
      const rendered = (component as (props: { node?: SelectionASTNode }) => JSX.Element | null)(props);
      if (!rendered) return rendered;
      const attributes = props.node?.properties;
      const sourceProps = {
        "data-file-source-start": attributes?.["data-file-source-start"],
        "data-file-source-end": attributes?.["data-file-source-end"],
      };
      // Composite renderers need a DOM boundary because they do not forward
      // arbitrary attributes. Inline links keep their mapped text children.
      if (tag === "pre") return <div {...sourceProps}>{rendered}</div>;
      return typeof rendered.type === "string" ? cloneElement(rendered, sourceProps) : rendered;
    },
  ])) as Components;
}

export function RichCodeBlock({
  code,
  displayedCode,
  language,
  cursor,
}: {
  code: string;
  displayedCode: string;
  language: string;
  /** Painted after the last code character, outside the copied <code>. */
  cursor?: ReactNode;
}): JSX.Element {
  const { t } = useI18n();
  // Reveal is pinned to one snapshot. A growing stream must not re-run
  // highlight.js on every token after the reader opts in.
  const [revealedText, setRevealedText] = useState<string | null>(null);
  const highlight = shouldHighlightCode(displayedCode, revealedText);
  // The parser ends code with a newline that <pre> does not paint. An inline
  // cursor after it would start an extra line, so move that newline after the
  // cursor: the bar follows the last character and the block keeps its height.
  const codeBody = cursor ? displayedCode.replace(/\n$/, "") : displayedCode;
  const highlighted = useMemo(
    () => highlight ? highlightCode(language, codeBody) : null,
    [codeBody, highlight, language]
  );
  const highlightedCode = (
    <>
      {highlighted ? (
        <code
          className={`hljs language-${highlighted.language}`}
          dangerouslySetInnerHTML={{ __html: highlighted.html }}
        />
      ) : (
        <code className="hljs">{codeBody}</code>
      )}
      {cursor}
      {cursor && codeBody !== displayedCode ? "\n" : null}
    </>
  );
  const oversized = displayedCode.length > CODE_HIGHLIGHT_CHAR_LIMIT;
  const canReveal = oversized && displayedCode.length <= CODE_HIGHLIGHT_HARD_LIMIT && !highlight;
  const highlightControl = canReveal ? (
    <button
      type="button"
      className="rich-code-highlight"
      onClick={() => setRevealedText(displayedCode)}
    >
      {t("rich.highlightCode")}
    </button>
  ) : oversized && !highlight ? (
    <span className="rich-code-plain-note">{t("rich.codePlainOnly")}</span>
  ) : null;
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
          {highlightControl}
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
    <div className={`rich-code-block${highlightControl ? "" : " rich-code-block--no-header"}`}>
      {highlightControl ? (
        <div className="rich-code-header">
          {highlightControl}
          <MessageCopyButton
            getText={() => code}
            className="rich-code-copy"
            iconSize={13}
            idleLabel={t("rich.copyCode")}
            copiedLabel={t("rich.codeCopied")}
            failedLabel={t("rich.copyFailed")}
          />
        </div>
      ) : null}
      <pre className="rich-code">
        {highlightedCode}
      </pre>
      {highlightControl ? null : (
        <MessageCopyButton
          getText={() => code}
          className="rich-code-copy"
          iconSize={13}
          idleLabel={t("rich.copyCode")}
          copiedLabel={t("rich.codeCopied")}
          failedLabel={t("rich.copyFailed")}
        />
      )}
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
  const fileSource = useContext(WorkspaceFileSource);
  const localFiles = !fileSource?.context.file_target || fileSource.context.file_target.environment_id === "local";
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
            ...(localFiles && hostSupports("revealWorkspaceItem")
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
  children,
  onOpenURL,
}: {
  href: string;
  title?: string;
  children: ReactNode;
  onOpenURL?: (url: string, event?: ReactMouseEvent<HTMLElement>) => void;
}): JSX.Element {
  const { t } = useI18n();
  const [contextMenu, setContextMenu] = useState<{ x: number; y: number } | null>(null);
  const openWorkspaceURL = useWorkspaceBrowserOpen((url, modifiers) => {
    if (prefersSystemBrowser(modifiers) || !onOpenURL) {
      openExternalURL(url);
      return;
    }
    onOpenURL(url);
  });
  const openLink = (event: ReactMouseEvent<HTMLAnchorElement>): void => {
    if (event.button > 1) {
      return;
    }
    event.preventDefault();
    const modifiers = workspaceBrowserClickModifiers(event);
    if (onOpenURL && !prefersSystemBrowser(modifiers)) {
      onOpenURL(href);
      return;
    }
    openWorkspaceURL(href, modifiers);
  };
  const closeContextMenu = (): void => setContextMenu(null);
  return (
    <>
      <Tooltip content={title ?? t("rich.openLink", { url: href })}>
        <a
          className="rich-link rich-web-link"
          href={href}
          rel="noopener noreferrer"
          onAuxClick={openLink}
          onClick={openLink}
          onContextMenu={(event) => {
            event.preventDefault();
            event.stopPropagation();
            setContextMenu({ x: event.clientX, y: event.clientY });
          }}
        >
          <span className="rich-link-content">
            <RichWebLinkIcon href={href} />
            <span className="rich-link-label">{children}</span>
          </span>
        </a>
      </Tooltip>
      {contextMenu ? (
        <ThreadContextMenu
          x={contextMenu.x}
          y={contextMenu.y}
          onClose={closeContextMenu}
          items={[
            { label: t("rich.openLinkInWorkspace"), onSelect: () => openWorkspaceURL(href) },
            { label: t("rich.openLinkExternal"), onSelect: () => openExternalURL(href) },
          ]}
        />
      ) : null}
    </>
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
  if (url === PENDING_STREAM_LINK) return url;
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

function rehypeHeadingIDs(headingIDs?: readonly string[]) {
  return (tree: MarkdownASTNode): void => {
    const usedIDs = new Set<string>();
    let headingIndex = 0;
    visitMarkdownAST(tree, (node) => {
      if (!/^h[1-6]$/.test(node.tagName ?? "")) {
        return;
      }
      const baseSlug = markdownHeadingSlug(markdownASTText(node));
      if (!baseSlug) {
        return;
      }
      const id = headingIDs?.[headingIndex++] ?? uniqueHeadingID(baseSlug, usedIDs);
      node.properties = {
        ...node.properties,
        id,
      };
    });
  };
}

function uniqueHeadingID(base: string, used: Set<string>): string {
  let id = base;
  let suffix = 0;
  while (used.has(id)) id = `${base}-${++suffix}`;
  used.add(id);
  return id;
}

// Use the same CommonMark/GFM parser and text repairs as the render path.
// Metadata is cached per frozen fragment; ordinary deltas only parse the tail.
const markdownMetadataParser = unified()
  .use(remarkParse)
  .use(markdownRemarkPlugins);

type MarkdownFragmentMetadata = {
  definitionSource: string;
  headingSlugs: string[];
  hasMermaid: boolean;
  hasContent: boolean;
};

export type MarkdownFragmentContext = {
  referenceDefinitions: string;
  headingIDs: readonly string[];
  hasMermaid: boolean;
  hasContent: boolean;
};

type MarkdownDocumentFragment = {
  text: string;
  parsed: MarkdownFragmentMetadata;
  resolved: MarkdownFragmentMetadata;
  context: MarkdownFragmentContext;
};

export type MarkdownDocument = {
  fragments: MarkdownDocumentFragment[];
};

export function inspectMarkdownFragment(text: string): MarkdownFragmentMetadata {
  const tree = markdownMetadataParser.runSync(markdownMetadataParser.parse(text), text) as MarkdownRoot;
  const definitions: Definition[] = [];
  const headingSlugs: string[] = [];
  let hasMermaid = false;
  visitMarkdownAST(tree, (node) => {
    if (node.type === "definition") definitions.push(node as Definition);
    if (node.type === "heading") {
      const slug = markdownHeadingSlug(markdownSourceText(node));
      if (slug) headingSlugs.push(slug);
    }
    if (node.type === "code" && "lang" in node && typeof node.lang === "string") {
      hasMermaid ||= node.lang.toLowerCase() === "mermaid";
    }
  });
  return {
    definitionSource: definitions.length ? toMarkdown({ type: "root", children: definitions }) : "",
    headingSlugs,
    hasMermaid,
    hasContent: tree.children.some(node => node.type !== "definition"),
  };
}

function markdownSourceText(node: MarkdownASTNode): string {
  if (node.type === "text" || node.type === "inlineCode") return node.value ?? "";
  return node.children?.map(markdownSourceText).join("") ?? "";
}

/** Share reference definitions and heading IDs without replacing stable DOM. */
export function createMarkdownDocument(texts: readonly string[], previous?: MarkdownDocument): MarkdownDocument {
  const parsed = texts.map((text, index) => {
    const old = previous?.fragments[index];
    return old?.text === text ? old.parsed : inspectMarkdownFragment(text);
  });
  // Prefix definitions so an earlier definition wins even when a later block
  // repeats its identifier. Serialization preserves escaped labels and titles,
  // including definitions nested in lists or block quotes.
  const referenceDefinitions = parsed.map(fragment => fragment.definitionSource).filter(Boolean).join("\n");
  const usedIDs = new Set<string>();
  const fragments = texts.map((text, index): MarkdownDocumentFragment => {
    const old = previous?.fragments[index];
    const sameDefinitions = old?.context.referenceDefinitions === referenceDefinitions;
    const resolved = old?.parsed === parsed[index] && sameDefinitions
      ? old.resolved
      : referenceDefinitions ? inspectMarkdownFragment(`${referenceDefinitions}\n${text}`) : parsed[index];
    const headingIDs = resolved.headingSlugs.map(slug => uniqueHeadingID(slug, usedIDs));
    const context = sameDefinitions
      && old.context.hasMermaid === resolved.hasMermaid
      && old.context.hasContent === resolved.hasContent
      && old.context.headingIDs.length === headingIDs.length
      && old.context.headingIDs.every((id, position) => id === headingIDs[position])
      ? old.context
      : { referenceDefinitions, headingIDs, hasMermaid: resolved.hasMermaid, hasContent: resolved.hasContent };
    return { text, parsed: parsed[index], resolved, context };
  });
  return { fragments };
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
  const fileSource = useContext(WorkspaceFileSource);
  const scoped = Boolean(fileSource?.context.file_target);
  const external = /^(?:https?:|data:image\/)/i.test(source);
  const sourceKey = JSON.stringify([workspaceFileContextKey(fileSource?.context), fileSource?.path, source]);
  const [workspaceImage, setWorkspaceImage] = useState<{ key: string; url?: string }>();
  useEffect(() => {
    if (!scoped || external || !fileSource) return;
    let cancelled = false;
    setWorkspaceImage(undefined);
    let path = source;
    if (path.startsWith("file://")) {
      try { path = decodeURIComponent(new URL(path).pathname); } catch { path = ""; }
    } else if (!path.startsWith("/") && !/^[a-z][a-z0-9+.-]*:/i.test(path)) {
      const directory = fileSource.path.slice(0, fileSource.path.lastIndexOf("/") + 1);
      const segments: string[] = [];
      let outside = false;
      for (const segment of `${directory}${path}`.split("/")) {
        if (!segment || segment === ".") continue;
        if (segment === "..") { if (segments.length === 0) { outside = true; break; } segments.pop(); }
        else segments.push(segment);
      }
      path = outside ? "" : `${directory.startsWith("/") ? "/" : ""}${segments.join("/")}`;
    }
    void readWorkspaceFileInContext(path, fileSource.context).then(result => {
      if (!cancelled) setWorkspaceImage({ key: sourceKey, url: result.renderable_kind === "image" ? result.renderable_url : undefined });
    }, () => { if (!cancelled) setWorkspaceImage({ key: sourceKey }); });
    return () => { cancelled = true; };
  }, [sourceKey, scoped, external]);
  const resolvedSource = scoped && !external
    ? workspaceImage?.key === sourceKey ? workspaceImage.url ?? "" : ""
    : resolveImageSource(source, cwd);
  const [failedSource, setFailedSource] = useState<string | null>(null);
  const { openPreview } = useImagePreview();
  const titleText = imageTarget(source);
  const register = useImagePreviewRegistration(!resolvedSource || failedSource === resolvedSource ? null : { src: resolvedSource, alt, title: titleText });
  const handleActivate = (origin: HTMLElement): void => {
    openPreview({ src: resolvedSource, alt, title: titleText }, origin);
  };
  const handleKeyDown = (event: ReactKeyboardEvent<HTMLImageElement>): void => {
    if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      handleActivate(event.currentTarget);
    }
  };
  const image = !resolvedSource || failedSource === resolvedSource ? (
    <span className="rich-image rich-image-unavailable" role="img" aria-label={t("imagePreview.loadFailed")}>
      {t("imagePreview.loadFailed")}
    </span>
  ) : (
    <Tooltip content={titleText}>
      <img
        className="rich-image"
        ref={register}
        src={resolvedSource}
        alt={alt}
        loading="lazy"
        role="button"
        tabIndex={0}
        aria-label={
          alt ? t("rich.enlargeNamed", { alt }) : t("rich.enlargeImage")
        }
        onClick={event => handleActivate(event.currentTarget)}
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

function MermaidDiagram({ code: source }: { code: string }): JSX.Element {
  // Context keeps the Markdown component types stable as the live tail settles.
  // Recreating the custom `pre` component would remount an already drawn diagram.
  const streaming = useContext(MermaidStreamingContext);
  const code = streaming ? source : source.replace(/\n$/, "");
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

  return (
    <div className="rich-mermaid" data-mermaid-state={state.status} aria-busy={state.status === "rendering"}>
      {state.status === "rendered" ? (
        <button
          type="button"
          className="rich-mermaid-diagram"
          aria-label={t("rich.enlargeDiagram")}
          title={t("rich.enlargeDiagram")}
          onClick={() => openPreview({
            svg: state.svg,
            alt: t("rich.mermaidDiagram"),
            title: t("rich.mermaidDiagram"),
          })}
          dangerouslySetInnerHTML={{ __html: state.svg }}
        />
      ) : state.status === "error" ? (
        <div className="rich-mermaid-error">
          <span>{state.message}</span>
          <pre><code>{code}</code></pre>
        </div>
      ) : (
        <div className="rich-mermaid-loading" role="status">{t("rich.renderingDiagram")}</div>
      )}
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
