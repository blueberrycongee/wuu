import { describe, expect, it } from "vitest";
import {
  formatWorkspaceFileTarget,
  parseLinkTarget,
  resolveWorkspaceFileTarget,
} from "./LinkTargets";

describe("parseLinkTarget", () => {
  it("classifies allowed external URLs", () => {
    expect(parseLinkTarget("https://example.com/docs?q=1")).toEqual({
      kind: "external",
      url: "https://example.com/docs?q=1",
    });
    expect(parseLinkTarget("mailto:team@example.com")).toEqual({
      kind: "external",
      url: "mailto:team@example.com",
    });
  });

  it("keeps same-document anchors out of the workspace file path", () => {
    expect(parseLinkTarget("#install-notes")).toEqual({
      kind: "anchor",
      id: "install-notes",
    });
  });

  it("parses VS Code line and range fragments", () => {
    expect(parseLinkTarget("src/App.tsx#L12")).toEqual({
      kind: "workspace-file",
      path: "src/App.tsx",
      selection: { startLineNumber: 12, startColumn: 1 },
    });
    expect(parseLinkTarget("src/App.tsx#L12,4-L18,9")).toEqual({
      kind: "workspace-file",
      path: "src/App.tsx",
      selection: {
        startLineNumber: 12,
        startColumn: 4,
        endLineNumber: 18,
        endColumn: 9,
      },
    });
  });

  it("accepts legacy colon line suffixes without treating them as URI schemes", () => {
    expect(parseLinkTarget("README.md:12")).toEqual({
      kind: "workspace-file",
      path: "README.md",
      selection: { startLineNumber: 12, startColumn: 1 },
    });
    expect(parseLinkTarget("C:\\repo\\src\\App.tsx:12:4-18:9")).toEqual({
      kind: "workspace-file",
      path: "C:\\repo\\src\\App.tsx",
      selection: {
        startLineNumber: 12,
        startColumn: 4,
        endLineNumber: 18,
        endColumn: 9,
      },
    });
  });

  it("parses file URLs as workspace files", () => {
    expect(parseLinkTarget("file:///repo/src/App.tsx#L20")).toEqual({
      kind: "workspace-file",
      path: "/repo/src/App.tsx",
      selection: { startLineNumber: 20, startColumn: 1 },
    });
  });

  it("rejects executable and unknown protocols", () => {
    expect(parseLinkTarget("javascript:alert(1)")).toEqual({ kind: "invalid" });
    expect(parseLinkTarget("command:workbench.action.openSettings")).toEqual({ kind: "invalid" });
  });
});

describe("formatWorkspaceFileTarget", () => {
  it("writes selections in canonical fragment form", () => {
    const target = parseLinkTarget("src/App.tsx:12:4-18:9");
    expect(target.kind).toBe("workspace-file");
    if (target.kind === "workspace-file") {
      expect(formatWorkspaceFileTarget(target)).toBe("src/App.tsx#L12,4-L18,9");
    }
  });

  it("resolves Markdown file links relative to the source document", () => {
    const target = parseLinkTarget("../src/App.tsx#L12");
    expect(target.kind).toBe("workspace-file");
    if (target.kind === "workspace-file") {
      expect(
        formatWorkspaceFileTarget(resolveWorkspaceFileTarget("docs/guide/README.md", target)),
      ).toBe("docs/src/App.tsx#L12");
    }
  });
});
