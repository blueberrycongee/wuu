import { isTouchWebShell } from "./ComposerFocus";
import * as monaco from "monaco-editor";
import { codeEditorTypography, observeAppearance } from "./AppearancePreferences";
import CssWorker from "monaco-editor/esm/vs/language/css/css.worker?worker";
import EditorWorker from "monaco-editor/esm/vs/editor/editor.worker?worker";
import HtmlWorker from "monaco-editor/esm/vs/language/html/html.worker?worker";
import JsonWorker from "monaco-editor/esm/vs/language/json/json.worker?worker";
import TsWorker from "monaco-editor/esm/vs/language/typescript/ts.worker?worker";
import { useEffect, useMemo, useRef } from "react";
import type { WorkspaceFileSelection } from "./LinkTargets";
import { useI18n } from "./i18n";
import { currentAppliedTheme, observeAppliedTheme, type AppliedTheme } from "./Theme";
import { resolveCssColors } from "./CssColors";

declare global {
  interface Window {
    MonacoEnvironment?: {
      getWorker: (_workerId: string, label: string) => Worker;
    };
  }
}

type MonacoLanguage =
  | "css"
  | "go"
  | "html"
  | "javascript"
  | "json"
  | "markdown"
  | "plaintext"
  | "python"
  | "rust"
  | "shell"
  | "sql"
  | "typescript"
  | "xml"
  | "yaml";

// A viewer answers to the reader's selection, not to the word under an idle
// caret; an editor keeps the usual occurrence marks.
function workspaceMonacoOccurrences(readOnly: boolean): "off" | "singleFile" {
  return readOnly ? "off" : "singleFile";
}

export type WorkspaceMonacoViewState = monaco.editor.ICodeEditorViewState;

export function WorkspaceMonacoEditor({
  path,
  resourceID,
  text,
  initialViewState,
  selection,
  readOnly = false,
  onChange,
  onSave,
  onViewStateChange,
}: {
  path: string;
  resourceID: string;
  text: string;
  initialViewState?: WorkspaceMonacoViewState | null;
  selection?: WorkspaceFileSelection;
  readOnly?: boolean;
  onChange?: (value: string) => void;
  onSave?: () => void;
  onViewStateChange?: (state: WorkspaceMonacoViewState | null) => void;
}): JSX.Element {
  const { t } = useI18n();
  const hostRef = useRef<HTMLDivElement>(null);
  const editorRef = useRef<monaco.editor.IStandaloneCodeEditor | null>(null);
  const modelRef = useRef<monaco.editor.ITextModel | null>(null);
  const onChangeRef = useRef(onChange);
  const onSaveRef = useRef(onSave);
  const onViewStateChangeRef = useRef(onViewStateChange);
  const language = useMemo(() => monacoLanguageForPath(path), [path]);

  useEffect(() => {
    onChangeRef.current = onChange;
    onSaveRef.current = onSave;
    onViewStateChangeRef.current = onViewStateChange;
  }, [onChange, onSave, onViewStateChange]);

  useEffect(() => {
    installMonacoWorkers();
  }, []);

  useEffect(() => {
    const host = hostRef.current;
    if (!host) {
      return undefined;
    }

    const model = createWorkspaceMonacoModel(text, language, workspaceMonacoModelURI(resourceID));
    const editor = monaco.editor.create(host, {
      ...workspaceMonacoReadingOptions(host),
      model,
      detectIndentation: true,
      occurrencesHighlight: workspaceMonacoOccurrences(readOnly),
      readOnly,
      tabSize: 2,
      theme: defineWorkspaceMonacoTheme(currentAppliedTheme()),
      wordWrap: "on",
    });
    const stopObservingTheme = observeAppliedTheme((theme) => {
      monaco.editor.setTheme(defineWorkspaceMonacoTheme(theme));
    });
    const stopObservingAppearance = observeAppearance(() => editor.updateOptions(codeEditorTypography()));

    const changeDisposable = editor.onDidChangeModelContent(() => {
      onChangeRef.current?.(model.getValue());
    });
    editor.addCommand(
      monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS,
      () => onSaveRef.current?.(),
    );

    modelRef.current = model;
    editorRef.current = editor;
    if (initialViewState) {
      editor.restoreViewState(initialViewState);
    }
    // A viewer leaves focus where the reader was (the file tree keeps its
    // keyboard path); only an editor takes the caret.
    if (!readOnly) {
      editor.focus();
    }

    return () => {
      onViewStateChangeRef.current?.(editor.saveViewState());
      stopObservingTheme();
      stopObservingAppearance();
      changeDisposable.dispose();
      editor.dispose();
      model.dispose();
      if (editorRef.current === editor) {
        editorRef.current = null;
      }
      if (modelRef.current === model) {
        modelRef.current = null;
      }
    };
  }, [language, resourceID]);

  useEffect(() => {
    const model = modelRef.current;
    if (!model || model.getValue() === text) {
      return;
    }
    model.pushEditOperations(
      [],
      [
        {
          range: model.getFullModelRange(),
          text,
        },
      ],
      () => null,
    );
  }, [text]);

  useEffect(() => {
    editorRef.current?.updateOptions({ readOnly, occurrencesHighlight: workspaceMonacoOccurrences(readOnly) });
  }, [readOnly]);

  useEffect(() => {
    const editor = editorRef.current;
    const model = modelRef.current;
    if (!editor || !model || !selection) {
      return;
    }
    const start = model.validatePosition({
      lineNumber: selection.startLineNumber,
      column: selection.startColumn,
    });
    const end = model.validatePosition({
      lineNumber: selection.endLineNumber ?? selection.startLineNumber,
      column: selection.endColumn ?? selection.startColumn,
    });
    const editorSelection = new monaco.Selection(
      start.lineNumber,
      start.column,
      end.lineNumber,
      end.column,
    );
    editor.setSelection(editorSelection);
    editor.revealRangeInCenter(editorSelection, monaco.editor.ScrollType.Immediate);
    if (!readOnly) {
      editor.focus();
    }
  }, [
    selection?.endColumn,
    selection?.endLineNumber,
    selection?.startColumn,
    selection?.startLineNumber,
  ]);

  return (
    <div
      aria-label={t(readOnly ? "workspace.monaco.viewer" : "workspace.monaco.editor", { path })}
      className="workspace-monaco-editor"
      data-language={language}
      data-path={path}
      data-resource-id={resourceID}
      ref={hostRef}
    />
  );
}

export function workspaceScrollbarSize(host: HTMLElement): number {
  const configuredSize = Number.parseFloat(
    window.getComputedStyle(host).getPropertyValue("--scrollbar-width"),
  );
  return Number.isFinite(configuredSize) && configuredSize > 0
    ? configuredSize
    : 10;
}

/**
 * A model for the workspace editors. Brackets keep the syntax colour of their
 * language: rainbow pairs are a model option in the standalone editor, which
 * the editor-level switch does not reach.
 */
export function createWorkspaceMonacoModel(text: string, language: string, uri: monaco.Uri): monaco.editor.ITextModel {
  const model = monaco.editor.createModel(text, language, uri);
  model.updateOptions({ bracketColorizationOptions: { enabled: false, independentColorPoolPerBracketType: false } });
  return model;
}

export function workspaceMonacoModelURI(resourceID: string): monaco.Uri {
  return monaco.Uri.parse(`wuu-workspace:///${encodeURIComponent(resourceID)}`);
}

export function installMonacoWorkers(): void {
  if (typeof window === "undefined" || window.MonacoEnvironment) {
    return;
  }

  window.MonacoEnvironment = {
    getWorker: (_workerId: string, label: string) => {
      if (label === "json") {
        return new JsonWorker();
      }
      if (label === "css" || label === "scss" || label === "less") {
        return new CssWorker();
      }
      if (label === "html" || label === "handlebars" || label === "razor") {
        return new HtmlWorker();
      }
      if (label === "typescript" || label === "javascript") {
        return new TsWorker();
      }
      return new EditorWorker();
    },
  };
}

export function monacoLanguageForPath(path: string): MonacoLanguage {
  const basename = path.split(/[\\/]/).filter(Boolean).at(-1) ?? path;
  const extension = basename.includes(".") ? basename.split(".").at(-1)?.toLowerCase() : undefined;

  if (extension === "ts" || extension === "tsx") {
    return "typescript";
  }
  if (extension === "js" || extension === "jsx" || extension === "mjs" || extension === "cjs") {
    return "javascript";
  }
  if (extension === "json" || extension === "jsonc") {
    return "json";
  }
  if (extension === "md" || extension === "mdx") {
    return "markdown";
  }
  if (extension === "yaml" || extension === "yml") {
    return "yaml";
  }
  if (extension === "css" || extension === "scss" || extension === "less") {
    return "css";
  }
  if (extension === "html" || extension === "htm") {
    return "html";
  }
  if (extension === "xml" || extension === "svg") {
    return "xml";
  }
  if (basename === "go.mod" || basename === "go.sum" || extension === "go") {
    return "go";
  }
  if (extension === "py") {
    return "python";
  }
  if (extension === "rs") {
    return "rust";
  }
  if (extension === "sql") {
    return "sql";
  }
  if (extension === "sh" || extension === "bash" || extension === "zsh") {
    return "shell";
  }
  return "plaintext";
}

// Diff washes are quiet tints of the status colors; the characters that
// changed read one step stronger. Dark paper needs a little more of the hue
// for the same weight. Both steps keep the syntax colours readable on them.
const DIFF_TINTS: Record<AppliedTheme, { line: number; text: number }> = {
  light: { line: 8, text: 14 },
  dark: { line: 12, text: 18 },
};

/**
 * Defines the workspace editor theme from the current CSS tokens and returns
 * its name. Editors paint what the rest of the product paints: ink on paper,
 * the product text selection, the syntax palette of rendered code blocks, and
 * status tints for diffs. Callers re-define on every theme change, so
 * extension themes reach the editor as well.
 */
export function defineWorkspaceMonacoTheme(theme: AppliedTheme): string {
  const name = theme === "dark" ? "wuu-workspace-dark" : "wuu-workspace";
  const tint = DIFF_TINTS[theme];
  const color = resolveCssColors({
    ink: "var(--ink)",
    inkSoft: "var(--ink-soft)",
    inkTertiary: "var(--ink-tertiary)",
    inkFaint: "var(--ink-faint)",
    danger: "var(--danger)",
    selection: "var(--selection-bg)",
    selectionSoft: "color-mix(in srgb, var(--selection-bg) 50%, transparent)",
    lineHighlight: "var(--ink-overlay-4)",
    hairlineSoft: "var(--hairline-soft)",
    hairline: "var(--hairline)",
    hairlineStrong: "var(--hairline-strong)",
    surface: "var(--surface-2)",
    menu: "var(--menu-bg)",
    menuBorder: "var(--menu-border)",
    field: "var(--field-bg)",
    fieldBorder: "var(--field-border)",
    focus: "var(--focus-ring)",
    thumb: "var(--scrollbar-thumb)",
    thumbHover: "var(--scrollbar-thumb-hover)",
    thumbActive: "var(--scrollbar-thumb-active)",
    insertedLine: `color-mix(in srgb, var(--success) ${tint.line}%, transparent)`,
    insertedText: `color-mix(in srgb, var(--success) ${tint.text}%, transparent)`,
    removedLine: `color-mix(in srgb, var(--danger) ${tint.line}%, transparent)`,
    removedText: `color-mix(in srgb, var(--danger) ${tint.text}%, transparent)`,
    keyword: "var(--hljs-keyword)",
    function: "var(--hljs-function)",
    string: "var(--hljs-string)",
    number: "var(--hljs-number)",
    comment: "var(--hljs-comment)",
    tag: "var(--hljs-tag)",
    literal: "var(--hljs-literal)",
    meta: "var(--hljs-meta)",
  });
  // Token rules take opaque RRGGBB without the hash.
  const token = (value: string): string => value.slice(1, 7);
  const transparent = "#00000000";
  monaco.editor.defineTheme(name, {
    base: theme === "dark" ? "vs-dark" : "vs",
    // Every token maps onto the product's syntax roles; nothing is left to the
    // stock palette, whose saturated hues would be the only colour on screen.
    inherit: false,
    rules: [
      { token: "", foreground: token(color.ink) },
      { token: "invalid", foreground: token(color.danger) },
      { token: "emphasis", fontStyle: "italic" },
      { token: "strong", fontStyle: "bold" },
      { token: "comment", foreground: token(color.comment), fontStyle: "italic" },
      { token: "keyword", foreground: token(color.keyword) },
      { token: "keyword.json", foreground: token(color.literal) },
      { token: "string", foreground: token(color.string) },
      { token: "string.key.json", foreground: token(color.tag) },
      { token: "regexp", foreground: token(color.string) },
      { token: "number", foreground: token(color.number) },
      { token: "constant", foreground: token(color.literal) },
      { token: "type", foreground: token(color.function) },
      { token: "predefined", foreground: token(color.function) },
      { token: "annotation", foreground: token(color.meta) },
      { token: "meta", foreground: token(color.meta) },
      { token: "metatag", foreground: token(color.meta) },
      { token: "tag", foreground: token(color.tag) },
      { token: "attribute.name", foreground: token(color.tag) },
      { token: "attribute.value", foreground: token(color.string) },
      { token: "attribute.value.number", foreground: token(color.number) },
      { token: "attribute.value.unit", foreground: token(color.number) },
      { token: "key", foreground: token(color.tag) },
      { token: "variable", foreground: token(color.meta) },
      { token: "variable.predefined", foreground: token(color.literal) },
    ],
    colors: {
      "editor.background": transparent,
      "editor.foreground": color.ink,
      "editorGutter.background": transparent,
      "editorLineNumber.foreground": color.inkTertiary,
      "editorLineNumber.activeForeground": color.ink,
      "editorLineNumber.dimmedForeground": color.inkTertiary,
      "editorCursor.foreground": color.ink,
      "editor.selectionBackground": color.selection,
      // A linked range stays marked while focus remains in the conversation.
      "editor.inactiveSelectionBackground": color.selection,
      "editor.selectionHighlightBackground": color.selectionSoft,
      "editor.wordHighlightBackground": color.selectionSoft,
      "editor.wordHighlightStrongBackground": color.selectionSoft,
      "editor.findMatchBackground": color.selection,
      "editor.findMatchHighlightBackground": color.selectionSoft,
      "editor.lineHighlightBackground": color.lineHighlight,
      "editor.lineHighlightBorder": transparent,
      "editorBracketMatch.background": color.selectionSoft,
      "editorBracketMatch.border": transparent,
      "editorIndentGuide.background1": color.hairlineSoft,
      "editorIndentGuide.activeBackground1": color.hairlineStrong,
      "editorWhitespace.foreground": color.inkFaint,
      "editorWidget.background": color.menu,
      "editorWidget.foreground": color.ink,
      "editorWidget.border": color.menuBorder,
      "editorHoverWidget.background": color.menu,
      "editorHoverWidget.border": color.menuBorder,
      "input.background": color.field,
      "input.border": color.fieldBorder,
      "input.foreground": color.ink,
      "input.placeholderForeground": color.inkTertiary,
      "inputOption.activeBorder": color.focus,
      focusBorder: color.focus,
      "scrollbar.shadow": transparent,
      "scrollbarSlider.background": color.thumb,
      "scrollbarSlider.hoverBackground": color.thumbHover,
      "scrollbarSlider.activeBackground": color.thumbActive,
      "diffEditor.insertedLineBackground": color.insertedLine,
      "diffEditor.insertedTextBackground": color.insertedText,
      "diffEditor.removedLineBackground": color.removedLine,
      "diffEditor.removedTextBackground": color.removedText,
      "diffEditorGutter.insertedLineBackground": color.insertedLine,
      "diffEditorGutter.removedLineBackground": color.removedLine,
      "diffEditor.border": color.hairlineSoft,
      "diffEditor.diagonalFill": color.hairline,
      "diffEditor.unchangedRegionBackground": color.surface,
      "diffEditor.unchangedRegionForeground": color.inkSoft,
      "diffEditor.unchangedRegionShadow": transparent,
      "diffEditor.unchangedCodeBackground": transparent,
      "diffEditor.move.border": color.hairlineStrong,
      "diffEditor.moveActive.border": color.inkSoft,
    },
  });
  return name;
}

/** Options shared by the file viewer and the review diff: calm reading chrome. */
export function workspaceMonacoReadingOptions(host: HTMLElement): monaco.editor.IEditorOptions {
  const scrollbarSize = workspaceScrollbarSize(host);
  return {
    automaticLayout: true,
    contextmenu: false,
    ...codeEditorTypography(),
    glyphMargin: false,
    guides: { bracketPairs: false, indentation: true, highlightActiveIndentation: false },
    hideCursorInOverviewRuler: true,
    lineDecorationsWidth: 6,
    lineNumbersMinChars: 3,
    minimap: { enabled: false },
    overviewRulerBorder: false,
    overviewRulerLanes: 0,
    padding: { top: 12, bottom: 12 },
    renderLineHighlight: "line",
    renderLineHighlightOnlyWhenFocus: true,
    scrollBeyondLastLine: false,
    showFoldingControls: "never",
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
  };
}
