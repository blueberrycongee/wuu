import { act } from "react";
import { createRoot } from "react-dom/client";
import { expect, it, vi } from "vitest";
import type { ChannelWork, ChannelWorkArtifact, ChannelWorkCandidateResult, WuuDesktopApi } from "../shared/protocol";
import { WorkCandidateReview } from "./WorkCandidateReview";
import { desktopPluginHost } from "./plugins/DesktopPluginRuntime";

it("pins human application to the reviewed revision and removes disabled extension actions", async () => {
  const container = document.createElement("div"); document.body.append(container);
  const root = createRoot(container);
  const artifact = { id: "artifact", kind: "candidate" } as ChannelWorkArtifact;
  const work = { id: "work", title: "Search" } as ChannelWork;
  const candidate = { session_id: "worker", turn_id: "turn", work_id: "work", goal_revision: 1, root: "/worktree", base_repo: "/project", base_revision: "base", revision: "abc", diff: "+pageSize=50", report: { result: "Updated search", evidence_refs: [], unresolved_items: [], implicit_choices: [] } };
  const api = vi.fn(async (p: { action: string }) => ({ candidate, artifact: { ...artifact, disposition: p.action === "apply" ? "applied" : undefined }, work_revision: 7, stale: false } as ChannelWorkCandidateResult));
  Object.defineProperty(window, "wuu", { configurable: true, value: { channelWorkCandidate: api } as unknown as WuuDesktopApi });
  const publish = vi.fn(async () => ({ url: "https://example.com/review" }));
  try {
    await act(async () => root.render(<WorkCandidateReview work={work} artifact={artifact} />));
    await act(async () => container.querySelector<HTMLButtonElement>(".work-candidate-toggle")!.click());
    const buttons = () => Array.from(container.querySelectorAll("button"));
    expect(buttons().find(button => button.textContent === "开 PR")?.disabled).toBe(true);
    await act(async () => { await desktopPluginHost.activateGeneration({ pluginId: "test-git", generation: "one", register(api) { api.registerCommand({ id: "publish", title: "Git review", contexts: ["work-candidate.publish"], execute: publish }); } }); });
    await act(async () => buttons().find(button => button.textContent === "开 PR")!.click());
    expect(publish).toHaveBeenCalledWith({ candidate, title: "Search" });
    await act(async () => desktopPluginHost.unload("test-git"));
    expect(buttons().find(button => button.textContent === "开 PR")?.disabled).toBe(true);
    await act(async () => buttons().find(button => button.textContent === "应用到项目")!.click());
    expect(api).toHaveBeenLastCalledWith({ work_id: "work", artifact_id: "artifact", action: "apply", expected_revision: 7 });
    expect(buttons().find(button => button.textContent === "丢弃")?.disabled).toBe(true);
  } finally { await act(async () => root.unmount()); desktopPluginHost.unload("test-git"); container.remove(); }
});
