import { isTouchWebShell } from "./ComposerFocus";
import * as monaco from "monaco-editor";
import { codeEditorTypography, observeAppearance } from "./AppearancePreferences";
import { useEffect, useMemo, useRef } from "react";
import { currentAppliedTheme, observeAppliedTheme } from "./Theme";
import {
  installMonacoWorkers,
  monacoLanguageForPath,
  workspaceMonacoModelURI,
  workspaceMonacoTheme,
  workspaceScrollbarSize,
} from "./WorkspaceMonacoEditor";
import { useI18n } from "./i18n";

export function WorkspaceMonacoDiffEditor({
  path,
  originalText,
  modifiedText,
}: {
  path: string;
  originalText: string;
  modifiedText: string;
}): JSX.Element {
  const { t } = useI18n();
  const hostRef = useRef<HTMLDivElement>(null);
  const language = useMemo(() => monacoLanguageForPath(path), [path]);

  useEffect(() => {
    installMonacoWorkers();
  }, []);

  useEffect(() => {
    const host = hostRef.current;
    if (!host) return undefined;

    const scrollbarSize = workspaceScrollbarSize(host);
    const resourceID = encodeURIComponent(path);
    const originalModel = monaco.editor.createModel(
      originalText,
      language,
      workspaceMonacoModelURI(`diff/original/${resourceID}`),
    );
    const modifiedModel = monaco.editor.createModel(
      modifiedText,
      language,
      workspaceMonacoModelURI(`diff/modified/${resourceID}`),
    );
    const typography = codeEditorTypography();
    const editor = monaco.editor.createDiffEditor(host, {
      automaticLayout: true,
      contextmenu: false,
      diffCodeLens: false,
      // Word wrap + hideUnchangedRegions mis-positions view-lines so wrapped
      // or deleted content paints over neighboring sparse rows. Keep horizontal
      // scroll instead; sparse gutters still come from hideUnchangedRegions.
      diffWordWrap: "off",
      enableSplitViewResizing: true,
      ...typography,
      glyphMargin: false,
      hideUnchangedRegions: {
        enabled: true,
        contextLineCount: 3,
        minimumLineCount: 8,
        revealLineCount: 12,
      },
      lineNumbersMinChars: 3,
      minimap: { enabled: false },
      originalEditable: false,
      readOnly: true,
      renderIndicators: true,
      renderMarginRevertIcon: false,
      renderOverviewRuler: false,
      renderSideBySide: true,
      scrollBeyondLastLine: false,
      scrollbar: {
        vertical: isTouchWebShell() ? "hidden" : "auto",
        horizontal: isTouchWebShell() ? "hidden" : "auto",
        alwaysConsumeMouseWheel: false,
        horizontalScrollbarSize: scrollbarSize,
        horizontalSliderSize: Math.max(4, scrollbarSize - 2),
        useShadows: false,
        verticalScrollbarSize: scrollbarSize,
        verticalSliderSize: Math.max(4, scrollbarSize - 2),
      },
      theme: workspaceMonacoTheme(currentAppliedTheme()),
      // Narrow panels used to flip into inline mode. Inline + hideUnchangedRegions
      // leaves ghost view-lines at top:0 that paint over the first visible rows.
      useInlineViewWhenSpaceIsLimited: false,
    });
    editor.setModel({ original: originalModel, modified: modifiedModel });
    const stopObservingTheme = observeAppliedTheme((theme) => {
      monaco.editor.setTheme(workspaceMonacoTheme(theme));
    });
    const applyTypography = () => {
      const next = codeEditorTypography();
      editor.updateOptions(next);
      editor.getOriginalEditor().updateOptions(next);
      editor.getModifiedEditor().updateOptions(next);
    };
    const stopObservingAppearance = observeAppearance(applyTypography);

    return () => {
      stopObservingTheme();
      stopObservingAppearance();
      editor.dispose();
      originalModel.dispose();
      modifiedModel.dispose();
    };
  }, [language, modifiedText, originalText, path]);

  return (
    <div
      aria-label={t("workspaceReview.codeDiffFor", { path })}
      className="workspace-monaco-diff-editor"
      data-language={language}
      data-path={path}
      ref={hostRef}
    />
  );
}
