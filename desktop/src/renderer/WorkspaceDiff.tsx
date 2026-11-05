import { parseDiffFromFile, parsePatchFiles, registerCustomCSSVariableTheme, type CodeViewItem } from "@pierre/diffs";
import { CodeView, type CodeViewReactOptions } from "@pierre/diffs/react";
import { type CSSProperties, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import type { EnvironmentFileDiffV1 } from "../../../packages/plugin-sdk/src/ui-environment";
import { codeEditorTypography, observeAppearance } from "./AppearancePreferences";
import { currentAppliedTheme, observeAppliedTheme } from "./Theme";
import { useI18n } from "./i18n";

// CSS variables keep syntax in sync with built-in and extension palettes,
// including palette changes that do not switch between light and dark mode.
registerCustomCSSVariableTheme("wuu-review", {
  foreground: "var(--ink)",
  background: "var(--workspace-canvas-bg)",
  "token-comment": "var(--hljs-comment)",
  "token-keyword": "var(--hljs-keyword)",
  "token-string": "var(--hljs-string)",
  "token-string-expression": "var(--hljs-string)",
  "token-constant": "var(--hljs-literal)",
  "token-function": "var(--hljs-function)",
  "token-parameter": "var(--hljs-meta)",
  "token-punctuation": "var(--ink-soft)",
  "token-link": "var(--hljs-tag)",
  "token-inserted": "var(--success)",
  "token-deleted": "var(--danger)",
});

export function WorkspaceDiff({ diff }: { diff: EnvironmentFileDiffV1 }): JSX.Element {
  const { t } = useI18n();
  const hostRef = useRef<HTMLDivElement>(null);
  const [theme, setTheme] = useState(currentAppliedTheme);
  const [typography, setTypography] = useState(codeEditorTypography);
  const [split, setSplit] = useState(false);
  const items = useMemo<CodeViewItem<undefined>[]>(() => {
    const { path, old_path, status, original_text, modified_text, patch } = diff;
    const files = typeof original_text === "string" && typeof modified_text === "string"
      ? [parseDiffFromFile(
        status === "added" || status === "untracked" ? null : { name: old_path ?? path, contents: original_text },
        status === "deleted" ? null : { name: path, contents: modified_text },
      )]
      : parsePatchFiles(patch).flatMap((parsed) => parsed.files);
    return files.filter((fileDiff) => fileDiff.hunks.length > 0)
      .map((fileDiff, index) => ({ id: `${path}:${index}`, type: "diff", fileDiff }));
  }, [diff]);

  useEffect(() => observeAppliedTheme(setTheme), []);
  useEffect(() => observeAppearance(() => setTypography(codeEditorTypography())), []);
  useLayoutEffect(() => {
    const host = hostRef.current;
    if (!host) return;
    const update = () => {
      // Leave enough room for both number gutters and readable code columns.
      if (host.clientWidth > 0) setSplit(host.clientWidth >= 720);
    };
    update();
    const observer = new ResizeObserver(update);
    observer.observe(host);
    return () => observer.disconnect();
  }, []);

  const options = useMemo<CodeViewReactOptions<undefined, undefined>>(() => ({
    theme: "wuu-review",
    themeType: theme,
    preferredHighlighter: "shiki-js",
    diffStyle: split ? "split" : "unified",
    diffIndicators: "classic",
    disableFileHeader: true,
    overflow: "wrap",
    enableLineSelection: true,
    itemMetrics: { lineHeight: typography.lineHeight },
  }), [split, theme, typography.lineHeight]);
  const style = useMemo<CSSProperties>(() => ({
    "--diffs-font-family": typography.fontFamily,
    "--diffs-font-size": `${typography.fontSize}px`,
    "--diffs-line-height": `${typography.lineHeight}px`,
  } as CSSProperties), [typography]);

  return (
    <div
      aria-label={t("workspaceReview.codeDiffFor", { path: diff.path })}
      className="workspace-diff"
      data-path={diff.path}
      ref={hostRef}
    >
      {items.length > 0 ? (
        <CodeView key={diff.path} className="workspace-diff-view" items={items} options={options} style={style} />
      ) : (
        <div className="workspace-diff-empty">{t("workspaceReview.noTextDiff")}</div>
      )}
    </div>
  );
}
