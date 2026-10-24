import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ProjectSessionResult, ProjectWork, ServerEvent, WuuDesktopApi } from "../../shared/protocol";
import { ApplicationProjects } from "./ApplicationProjects";

const work: ProjectWork = { id: "work", project_id: "project", version: 1, revision: 7, title: "Task", brief: "Brief",
  acceptance: "Evidence", authority: "Read", phase: "blocked", lead_id: "lead", workspace: "/source" };
let source: string;
let receive: (event: ServerEvent) => void;
let service: ApplicationProjects;
const original = window.wuu;
const projectWork = vi.fn(); const stop = vi.fn(); const updateThread = vi.fn();
beforeEach(() => {
  source = "source"; projectWork.mockReset(); stop.mockReset(); updateThread.mockReset();
  projectWork.mockResolvedValue({ project_id: "project", works: [work], usage: [] });
  window.wuu = { projectWork, projectSession: vi.fn(), onServerEvent: (listener: (event: ServerEvent) => void) => { receive = listener; return stop; } } as unknown as WuuDesktopApi;
  service = new ApplicationProjects({ getSource: () => ({ contextKey: source, enabled: true }), updateThread });
});
afterEach(() => { service.dispose(); window.wuu = original; });

describe("public project work owner", () => {
  it("shares one immutable observation and refreshes it from ledger events", async () => {
    const first = service.subscribe("project", vi.fn());
    const second = service.subscribe("project", vi.fn());
    await vi.waitFor(() => expect(service.getSnapshot("project").snapshot?.works).toHaveLength(1));
    expect(projectWork).toHaveBeenCalledTimes(1);
    expect(Object.isFrozen(service.getSnapshot("project").snapshot?.works[0])).toBe(true);
    receive({ kind: "notification", message: { method: "project/work/updated", params: { project_id: "project" } } } as ServerEvent);
    await vi.waitFor(() => expect(projectWork).toHaveBeenCalledTimes(2));
    first(); expect(stop).not.toHaveBeenCalled(); second(); expect(stop).toHaveBeenCalledOnce();
  });
  it("rejects stale work revisions and sends one captured mutation", async () => {
    await service.refresh("project"); projectWork.mockClear();
    await expect(service.control({ projectId: "project", workId: "work", revision: 6, operation: "resume" })).rejects.toThrow("revision");
    expect(projectWork).not.toHaveBeenCalled();
    await service.control({ projectId: "project", workId: "work", revision: 7, operation: "resume" });
    expect(projectWork).toHaveBeenCalledWith(expect.objectContaining({ project_id: "project", work_id: "work", revision: 7,
      operation: "resume", brief: "Brief", acceptance: "Evidence", authority: "Read", request_id: expect.any(String) }));
    expect(projectWork.mock.calls.filter(([input]) => input.operation === "resume")).toHaveLength(1);
  });
  it("discards an old source read and never publishes a released session into its successor", async () => {
    let finish!: (value: unknown) => void;
    projectWork.mockImplementation(() => new Promise(resolve => { finish = resolve; }));
    const pending = service.refresh("project"); source = "successor"; service.notifyChanged();
    finish({ project_id: "project", works: [work], usage: [] });
    await expect(pending).rejects.toThrow("source changed");
    expect(service.getSnapshot("project").snapshot).toBeUndefined();
    let finishRelease!: (value: ProjectSessionResult) => void;
    vi.mocked(window.wuu.projectSession!).mockImplementation(() => new Promise(resolve => { finishRelease = resolve; }));
    const releasing = service.releaseSession({ projectId: "project", threadId: "member" });
    source = "third"; service.notifyChanged(); finishRelease({ thread: { id: "member" } } as ProjectSessionResult);
    await expect(releasing).rejects.toThrow("source changed"); expect(updateThread).not.toHaveBeenCalled();
  });
  it("ends observers and refuses new work on disposal", async () => {
    const changed = vi.fn(); service.subscribe("project", changed);
    await vi.waitFor(() => expect(service.getSnapshot("project").loading).toBe(false));
    service.dispose(); changed.mockClear();
    await expect(service.refresh("project")).rejects.toThrow("disposed");
    expect(stop).toHaveBeenCalledOnce(); expect(changed).not.toHaveBeenCalled();
  });
});
