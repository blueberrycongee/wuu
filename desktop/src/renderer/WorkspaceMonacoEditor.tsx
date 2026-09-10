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

export function workspaceMonacoTheme(theme: AppliedTheme): string {
  return theme === "dark" ? "wuu-workspace-dark" : "wuu-workspace";
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

    const scrollbarSize = workspaceScrollbarSize(host);
    const model = monaco.editor.createModel(
      text,
      language,
      workspaceMonacoModelURI(resourceID),
    );
    const editor = monaco.editor.create(host, {
      model,
      automaticLayout: true,
      contextmenu: false,
      detectIndentation: true,
      ...codeEditorTypography(),
      glyphMargin: false,
      lineDecorationsWidth: 6,
      lineNumbersMinChars: 3,
      minimap: { enabled: false },
      readOnly,
      renderLineHighlight: "line",
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
      tabSize: 2,
      theme: workspaceMonacoTheme(currentAppliedTheme()),
      wordWrap: "on",
    });
    const stopObservingTheme = observeAppliedTheme((theme) => {
      monaco.editor.setTheme(workspaceMonacoTheme(theme));
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
    editor.focus();

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
    editorRef.current?.updateOptions({ readOnly });
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
    editor.focus();
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

monaco.editor.defineTheme("wuu-workspace", {
  base: "vs",
  inherit: true,
  rules: [],
  colors: {
    "editor.background": "#ffffff00",
    "editor.foreground": "#24282d",
    "editor.lineHighlightBackground": "#24282d0a",
    "editorLineNumber.foreground": "#8b939b",
    "editorLineNumber.activeForeground": "#24282d",
    "editor.selectionBackground": "#ef5b1838",
    "editorCursor.foreground": "#ef5b18",
  },
});

monaco.editor.defineTheme("wuu-workspace-dark", {
  base: "vs-dark",
  inherit: true,
  rules: [],
  colors: {
    "editor.background": "#1d202400",
    "editor.foreground": "#e4e6e8",
    "editor.lineHighlightBackground": "#dce4eb0d",
    "editorLineNumber.foreground": "#7d8388",
    "editorLineNumber.activeForeground": "#e4e6e8",
    "editor.selectionBackground": "#ff5a2645",
    "editorCursor.foreground": "#ff5a26",
  },
});
