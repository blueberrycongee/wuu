import { describe, expect, it } from "vitest";
import type { InitializeResult, Thread, Turn } from "../shared/protocol";
import {
  runtimeViewForConversation,
  runtimeViewForSession,
  submittedThreadRuntime,
} from "./SessionRuntimeState";

function workspaceDefaults(): InitializeResult {
  return {
    protocol_version: "wuu-app-server/v0.1",
    provider: "workspace-provider",
    model: "workspace-model",
    variant: "medium",
    workspace_root: "/tmp/project",
    permissions: { mode: "standard" },
  };
}

function session(): Thread {
  return {
    id: "session-a",
    preview: "session A",
    model_provider: "session-provider",
    model: "session-model",
    model_variant: "high",
    model_effort: "",
    permission_mode: "read_only",
    approve_for_me: true,
    cwd: "/tmp/project",
    status: "idle",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    turns: [],
  };
}

function turn(status: Turn["status"]): Turn {
  return {
    id: "turn-a",
    status,
    model_provider: "turn-provider",
    model: "turn-model",
    items_view: "full",
    items: [],
  };
}

describe("session runtime state", () => {
  it("uses the session pin for the next turn instead of workspace defaults", () => {
    expect(runtimeViewForSession(workspaceDefaults(), session())).toMatchObject({
      provider: "session-provider",
      model: "session-model",
      variant: "high",
      effort: "",
      permissions: { mode: "read_only", approve_for_me: true },
    });
  });

  it("shows the admitted turn model while it is running", () => {
    expect(
      runtimeViewForConversation(workspaceDefaults(), session(), turn("in_progress")),
    ).toMatchObject({
      provider: "turn-provider",
      model: "turn-model",
      variant: "high",
    });
  });

  it("returns to the session pin as soon as the turn settles", () => {
    expect(
      runtimeViewForConversation(workspaceDefaults(), session(), turn("completed")),
    ).toMatchObject({
      provider: "session-provider",
      model: "session-model",
      variant: "high",
      permissions: { mode: "read_only", approve_for_me: true },
    });
  });
});

describe("submitted thread runtime", () => {
  it("pins the engine id for protocol engines", () => {
    expect(
      submittedThreadRuntime(
        { engine: "devin", provider: "workspace-provider", model: "swe-2" },
        [{ id: "devin" }],
        { provider: "workspace-provider", model: "workspace-model" },
      ),
    ).toEqual({ provider: "devin", model: "swe-2" });
  });

  it("pins the selected provider for host-model engines", () => {
    expect(
      submittedThreadRuntime(
        { engine: "wuu", provider: "other-provider", model: "other-model" },
        [{ id: "wuu", requires_host_model: true }],
        { provider: "workspace-provider", model: "workspace-model" },
      ),
    ).toEqual({ provider: "other-provider", model: "other-model" });
  });

  it("keeps the workspace default when the draft selection is empty", () => {
    expect(
      submittedThreadRuntime(
        { engine: "wuu", provider: "", model: "" },
        [{ id: "wuu", requires_host_model: true }],
        { provider: "workspace-provider", model: "workspace-model" },
      ),
    ).toEqual({ provider: "workspace-provider", model: "workspace-model" });
  });

  it("falls back to workspace defaults without a draft selection", () => {
    expect(
      submittedThreadRuntime(undefined, undefined, {
        provider: "workspace-provider",
        model: "workspace-model",
      }),
    ).toEqual({ provider: "workspace-provider", model: "workspace-model" });
  });
});
