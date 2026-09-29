import * as monaco from "monaco-editor";
import { observeAppearance, codeEditorTypography } from "./AppearancePreferences";
import { useEffect, useMemo, useRef } from "react";
import { currentAppliedTheme, observeAppliedTheme } from "./Theme";
import {
  createWorkspaceMonacoModel,
  defineWorkspaceMonacoTheme,
  installMonacoWorkers,
  monacoLanguageForPath,
  workspaceMonacoModelURI,
  workspaceMonacoReadingOptions,
} from "./WorkspaceMonacoEditor";
import { useI18n } from "./i18n";

// Monaco's own diff decorations, so a one-sided file reads exactly like the
// changed lines of a two-sided diff.
const WHOLE_FILE_CHANGE: Record<"inserted" | "removed", monaco.editor.IModelDecorationOptions> = {
  inserted: {
    isWholeLine: true,
    className: "line-insert",
    marginClassName: "gutter-insert",
    linesDecorationsClassName: "insert-sign codicon codicon-diff-insert",
  },
  removed: {
    isWholeLine: true,
    className: "line-delete",
    marginClassName: "gutter-delete",
    linesDecorationsClassName: "delete-sign codicon codicon-diff-remove",
  },
};

// Room for the +/− indicator between the line number and the code.
const DIFF_INDICATOR_WIDTH = 16;

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

    const resourceID = encodeURIComponent(path);
    const options = {
      ...workspaceMonacoReadingOptions(host),
      lineDecorationsWidth: DIFF_INDICATOR_WIDTH,
      occurrencesHighlight: "off" as const,
      readOnly: true,
      theme: defineWorkspaceMonacoTheme(currentAppliedTheme()),
    };
    const stopObservingTheme = observeAppliedTheme((theme) => {
      monaco.editor.setTheme(defineWorkspaceMonacoTheme(theme));
    });

    // With one side empty every line of the other side changed. The diff
    // algorithm would still pair the empty side's lone blank line with the
    // first changed line and paint a removed row that holds nothing, so an
    // added or deleted file shows its one real side, every line marked.
    const wholeFile = originalText === "" ? "inserted" : modifiedText === "" ? "removed" : undefined;
    if (wholeFile) {
      const model = createWorkspaceMonacoModel(
        wholeFile === "inserted" ? modifiedText : originalText,
        language,
        workspaceMonacoModelURI(`diff/${wholeFile}/${resourceID}`),
      );
      const editor = monaco.editor.create(host, { ...options, model, wordWrap: "on" });
      editor.createDecorationsCollection([
        { range: model.getFullModelRange(), options: WHOLE_FILE_CHANGE[wholeFile] },
      ]);
      const stopObservingAppearance = observeAppearance(() => editor.updateOptions(codeEditorTypography()));
      return () => {
        stopObservingTheme();
        stopObservingAppearance();
        editor.dispose();
        model.dispose();
      };
    }

    const originalModel = createWorkspaceMonacoModel(
      originalText,
      language,
      workspaceMonacoModelURI(`diff/original/${resourceID}`),
    );
    const modifiedModel = createWorkspaceMonacoModel(
      modifiedText,
      language,
      workspaceMonacoModelURI(`diff/modified/${resourceID}`),
    );
    const editor = monaco.editor.createDiffEditor(host, {
      ...options,
      diffCodeLens: false,
      diffWordWrap: "on",
      enableSplitViewResizing: true,
      hideUnchangedRegions: {
        enabled: true,
        contextLineCount: 3,
        minimumLineCount: 8,
        revealLineCount: 12,
      },
      originalEditable: false,
      renderIndicators: true,
      renderMarginRevertIcon: false,
      renderOverviewRuler: false,
      renderSideBySide: true,
      useInlineViewWhenSpaceIsLimited: true,
    });
    editor.setModel({ original: originalModel, modified: modifiedModel });
    const stopObservingAppearance = observeAppearance(() => editor.updateOptions(codeEditorTypography()));

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
