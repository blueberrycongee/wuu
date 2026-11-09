import hljs from "highlight.js/lib/core";
import bash from "highlight.js/lib/languages/bash";
import c from "highlight.js/lib/languages/c";
import cpp from "highlight.js/lib/languages/cpp";
import css from "highlight.js/lib/languages/css";
import diff from "highlight.js/lib/languages/diff";
import dockerfile from "highlight.js/lib/languages/dockerfile";
import go from "highlight.js/lib/languages/go";
import ini from "highlight.js/lib/languages/ini";
import javascript from "highlight.js/lib/languages/javascript";
import json from "highlight.js/lib/languages/json";
import makefile from "highlight.js/lib/languages/makefile";
import markdown from "highlight.js/lib/languages/markdown";
import plaintext from "highlight.js/lib/languages/plaintext";
import python from "highlight.js/lib/languages/python";
import rust from "highlight.js/lib/languages/rust";
import sql from "highlight.js/lib/languages/sql";
import typescript from "highlight.js/lib/languages/typescript";
import xml from "highlight.js/lib/languages/xml";
import yaml from "highlight.js/lib/languages/yaml";

hljs.registerLanguage("bash", bash);
hljs.registerLanguage("c", c);
hljs.registerLanguage("cpp", cpp);
hljs.registerLanguage("css", css);
hljs.registerLanguage("diff", diff);
hljs.registerLanguage("dockerfile", dockerfile);
hljs.registerLanguage("go", go);
hljs.registerLanguage("ini", ini);
hljs.registerLanguage("javascript", javascript);
hljs.registerLanguage("json", json);
hljs.registerLanguage("makefile", makefile);
hljs.registerLanguage("markdown", markdown);
hljs.registerLanguage("plaintext", plaintext);
hljs.registerLanguage("python", python);
hljs.registerLanguage("rust", rust);
hljs.registerLanguage("sql", sql);
hljs.registerLanguage("typescript", typescript);
hljs.registerLanguage("xml", xml);
hljs.registerLanguage("yaml", yaml);

const LANGUAGE_BY_EXTENSION: Record<string, string> = {
  bash: "bash",
  c: "c",
  cc: "cpp",
  cpp: "cpp",
  css: "css",
  diff: "diff",
  go: "go",
  h: "cpp",
  hpp: "cpp",
  html: "xml",
  ini: "ini",
  js: "javascript",
  jsx: "javascript",
  json: "json",
  md: "markdown",
  mdx: "markdown",
  py: "python",
  rs: "rust",
  sh: "bash",
  sql: "sql",
  ts: "typescript",
  tsx: "typescript",
  txt: "plaintext",
  xml: "xml",
  yaml: "yaml",
  yml: "yaml",
  zsh: "bash"
};

const LANGUAGE_BY_BASENAME: Record<string, string> = {
  ".bashrc": "bash",
  ".gitignore": "plaintext",
  ".zshrc": "bash",
  Dockerfile: "dockerfile",
  Makefile: "makefile",
  "go.mod": "go",
  "go.sum": "go",
  "package-lock.json": "json",
  "package.json": "json"
};

export type HighlightedWorkspaceCode = {
  html: string;
  language: string;
};

/** Chat blocks above this stay one text node until the reader asks for color. */
export const CODE_HIGHLIGHT_CHAR_LIMIT = 8_000;

/** Past this, highlighting the whole block builds enough spans to exhaust the renderer. */
export const CODE_HIGHLIGHT_HARD_LIMIT = 100_000;

export function shouldHighlightCode(text: string, revealedText: string | null): boolean {
  if (text.length <= CODE_HIGHLIGHT_CHAR_LIMIT) return true;
  if (text.length > CODE_HIGHLIGHT_HARD_LIMIT) return false;
  return revealedText === text;
}

function escapeHtml(text: string): string {
  return text.replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;");
}

export function highlightCode(language: string, text: string): HighlightedWorkspaceCode {
  if (text.length > CODE_HIGHLIGHT_HARD_LIMIT) {
    return { html: escapeHtml(text), language: "plaintext" };
  }
  const normalizedLanguage = language.trim().toLowerCase();
  const resolvedLanguage = LANGUAGE_BY_EXTENSION[normalizedLanguage] ?? normalizedLanguage;
  const supportedLanguage = hljs.getLanguage(resolvedLanguage) ? resolvedLanguage : "plaintext";
  return {
    html: hljs.highlight(text, {
      language: supportedLanguage,
      ignoreIllegals: true
    }).value,
    language: supportedLanguage
  };
}

export function highlightWorkspaceCode(path: string, text: string): HighlightedWorkspaceCode {
  return highlightCode(workspaceLanguageForPath(path), text);
}

function workspaceLanguageForPath(path: string): string {
  const basename = path.split(/[\\/]/).filter(Boolean).at(-1) ?? path;
  const basenameLanguage = LANGUAGE_BY_BASENAME[basename];
  if (basenameLanguage) {
    return basenameLanguage;
  }

  const extension = basename.includes(".") ? basename.split(".").at(-1)?.toLowerCase() : undefined;
  return extension ? LANGUAGE_BY_EXTENSION[extension] ?? "plaintext" : "plaintext";
}
