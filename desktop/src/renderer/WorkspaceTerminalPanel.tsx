import { isTouchWebShell } from "./ComposerFocus";
import { hostSupports } from "./HostCapabilities";
import { FitAddon } from "@xterm/addon-fit";
import { Terminal as XtermTerminal, type ITerminalOptions, type ITheme } from "@xterm/xterm";
import "@xterm/xterm/css/xterm.css";
import { CheckCircle2, Clock3, Plus, Square, SquareTerminal, Terminal, X, XCircle } from "lucide-react";
import {
  type CSSProperties,
  type KeyboardEvent as ReactKeyboardEvent,
  type PointerEvent as ReactPointerEvent,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import type {
  ManagedProcessSummary,
  RuntimeContext,
  TerminalSessionEvent,
  Thread,
} from "../shared/protocol";
import { currentAppliedTheme, observeAppliedTheme, type AppliedTheme } from "./Theme";
import {
  agentRunGroupsForThread,
  selectAgentRun,
  type AgentRunLocator,
  type AgentRunRecord,
} from "./TerminalRuns";
import { WorkspacePanelEmpty } from "./WorkspaceFiles";
import { desktopApiErrorMessage } from "./WorkspaceReviewHelpers";
import { translateCurrent, useI18n } from "./i18n";
import { TruncatedText } from "./TruncatedText";

const WORKSPACE_TERMINAL_PENDING_EVENT_IDS = 12;
const WORKSPACE_TERMINAL_PENDING_EVENTS_PER_ID = 256;
const WORKSPACE_TERMINAL_PENDING_TEXT_PER_ID = 512 * 1024;
const WORKSPACE_TERMINAL_NAVIGATION_WIDTH_KEY = "wuu.workspaceTerminalNavigationWidth";
const WORKSPACE_TERMINAL_NAVIGATION_DEFAULT_WIDTH = 212;
const WORKSPACE_TERMINAL_NAVIGATION_MIN_WIDTH = 144;
const WORKSPACE_TERMINAL_NAVIGATION_MAX_WIDTH = 360;
const WORKSPACE_TERMINAL_NAVIGATION_WIDTH_STEP = 12;

type WorkspaceTerminalState = "starting" | "ready" | "exited" | "error";

type UserTerminalResource = {
  id: string;
  ordinal: number;
  shell?: string;
  state: WorkspaceTerminalState;
};

function workspaceTerminalStyle(host: HTMLElement | undefined, name: string, fallback: string): string {
  const value = host ? window.getComputedStyle(host).getPropertyValue(name).trim() : "";
  return value || fallback;
}

function workspaceTerminalTheme(theme: AppliedTheme, host?: HTMLElement): ITheme {
  const defaults: ITheme = theme === "dark"
    ? {
      background: "#1d2024",
      black: "#858c93",
      blue: "#58a6ff",
      cursor: "#f2f3f4",
      foreground: "#e4e6e8",
      green: "#4cc38a",
      red: "#f0705f",
      selectionBackground: "#3a4046",
      yellow: "#d9a84e",
    }
    : {
      background: "#ffffff",
      black: "#24292f",
      blue: "#2f98ff",
      cursor: "#202427",
      foreground: "#1f2328",
      green: "#1f9d46",
      red: "#b42318",
      selectionBackground: "#d7e9ff",
      yellow: "#ffc21a",
    };
  return {
    /* The terminal is a host mechanism, not a private theme island. Derive its
       palette from the existing public semantic contract so bundled and
       third-party themes affect it without adding terminal-specific API. */
    background: workspaceTerminalStyle(host, "--wuu-color-canvas", defaults.background ?? "#ffffff"),
    black: workspaceTerminalStyle(host, "--wuu-color-text-muted", defaults.black ?? "#24292f"),
    blue: workspaceTerminalStyle(host, "--wuu-color-info", defaults.blue ?? "#2f98ff"),
    cursor: workspaceTerminalStyle(host, "--wuu-color-text", defaults.cursor ?? "#202427"),
    foreground: workspaceTerminalStyle(host, "--wuu-color-text", defaults.foreground ?? "#1f2328"),
    green: workspaceTerminalStyle(host, "--wuu-color-success", defaults.green ?? "#1f9d46"),
    red: workspaceTerminalStyle(host, "--wuu-color-danger", defaults.red ?? "#b42318"),
    selectionBackground: workspaceTerminalStyle(host, "--wuu-color-live-highlight", defaults.selectionBackground ?? "#d7e9ff"),
    yellow: workspaceTerminalStyle(host, "--wuu-color-warning", defaults.yellow ?? "#ffc21a"),
  };
}

function workspaceTerminalOptions({
  interactive,
  readOnly = false,
  host,
}: {
  interactive: boolean;
  readOnly?: boolean;
  host?: HTMLElement;
}): ITerminalOptions {
  return {
    allowTransparency: false,
    convertEol: !interactive,
    cursorBlink: interactive,
    disableStdin: readOnly,
    fontFamily: workspaceTerminalStyle(host, "--wuu-workspace-terminal-font-family", '"SFMono-Regular", Consolas, "Liberation Mono", monospace'),
    fontSize: 12,
    lineHeight: 1.45,
    scrollback: 10000,
    theme: workspaceTerminalTheme(currentAppliedTheme(), host),
  };
}

export function appendPendingTerminalEvent(
  events: TerminalSessionEvent[],
  event: TerminalSessionEvent,
): TerminalSessionEvent[] {
  const normalized =
    event.type === "data" &&
    event.text.length > WORKSPACE_TERMINAL_PENDING_TEXT_PER_ID
      ? {
          ...event,
          text: event.text.slice(-WORKSPACE_TERMINAL_PENDING_TEXT_PER_ID),
        }
      : event;
  const next = [...events, normalized];
  let textLength = next.reduce(
    (total, pending) =>
      total + (pending.type === "data" ? pending.text.length : 0),
    0,
  );
  while (
    next.length > WORKSPACE_TERMINAL_PENDING_EVENTS_PER_ID ||
    textLength > WORKSPACE_TERMINAL_PENDING_TEXT_PER_ID
  ) {
    const dataIndex = next.findIndex((pending) => pending.type === "data");
    const removeIndex = dataIndex >= 0 ? dataIndex : 0;
    const [removed] = next.splice(removeIndex, 1);
    if (removed?.type === "data") {
      textLength -= removed.text.length;
    }
  }
  return next;
}

function formatTerminalDuration(ms: number): string {
  const totalSeconds = Math.max(0, Math.floor(ms / 1000));
  const hours = Math.floor(totalSeconds / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = totalSeconds % 60;
  if (hours > 0) {
    return translateCurrent("workspace.terminal.durationHours", { hours, minutes, seconds });
  }
  if (minutes > 0) {
    return translateCurrent("workspace.terminal.durationMinutes", { minutes, seconds });
  }
  return translateCurrent("workspace.terminal.durationSeconds", { seconds });
}

function terminalExitText(event: Extract<TerminalSessionEvent, { type: "exit" }>): string {
  const duration = formatTerminalDuration(event.duration_ms);
  if (event.signal) {
    return translateCurrent("workspace.terminal.stoppedBy", { signal: event.signal, duration });
  }
  return translateCurrent("workspace.terminal.exited", {
    code: event.exit_code ?? translateCurrent("workspace.terminal.unknown"),
    duration,
  });
}

export type WorkspaceTerminalRunRequest = AgentRunLocator & { requestID: number };

export function WorkspaceTerminalPanel({
  activeContext,
  thread,
  requestedRun,
}: {
  activeContext?: RuntimeContext;
  thread?: Thread;
  requestedRun?: WorkspaceTerminalRunRequest;
}): JSX.Element {
  const { t } = useI18n();
  const groups = useMemo(
    () => (thread ? agentRunGroupsForThread(thread) : []),
    [thread],
  );
  const requestedRecord = requestedRun ? selectAgentRun(groups, requestedRun) : undefined;
  const [selectedResourceID, setSelectedResourceID] = useState(
    () => requestedRecord?.toolCallID ?? "",
  );
  const [userTerminals, setUserTerminals] = useState<UserTerminalResource[]>([]);
  const nextUserTerminalOrdinalRef = useRef(1);
  const [managedProcesses, setManagedProcesses] = useState<Record<string, ManagedProcessSummary>>({});
  const [navigationWidth, setNavigationWidth] = useState(readStoredTerminalNavigationWidth);
  const [resizingNavigation, setResizingNavigation] = useState(false);
  const navigationResizeRef = useRef<{ startX: number; startWidth: number } | null>(null);
  const runs = useMemo(
    () => Object.values(managedProcesses)
      .filter(isManagedProcessLive)
      .sort((left, right) => Date.parse(right.updated_at) - Date.parse(left.updated_at))
      .map((process) => managedRunFromProcess(thread?.id ?? process.owner_id, process)),
    [managedProcesses, thread?.id],
  );
  const selectedRun = runs.find((run) => run.toolCallID === selectedResourceID)
    ?? (requestedRecord?.toolCallID === selectedResourceID ? requestedRecord : undefined);
  const standaloneAgentRun = Boolean(
    selectedRun && userTerminals.length === 0 && runs.length === 0,
  );
  const handleManagedProcessChange = useCallback((next: ManagedProcessSummary) => {
    setManagedProcesses((current) => {
      if (!isManagedProcessLive(next)) {
        const updated = { ...current };
        delete updated[next.id];
        return updated;
      }
      return {
        ...current,
        [next.id]: preferManagedProcess(current[next.id], next),
      };
    });
  }, []);
  const handleUserTerminalStateChange = useCallback((id: string, state: WorkspaceTerminalState) => {
    setUserTerminals((current) => current.map((terminal) => (
      terminal.id === id && terminal.state !== state ? { ...terminal, state } : terminal
    )));
  }, []);
  const handleUserTerminalShellChange = useCallback((id: string, shell: string) => {
    setUserTerminals((current) => current.map((terminal) => (
      terminal.id === id && terminal.shell !== shell ? { ...terminal, shell } : terminal
    )));
  }, []);

  useEffect(() => {
    const threadID = thread?.id;
    if (!threadID) {
      setManagedProcesses({});
      return undefined;
    }
    let disposed = false;
    let refreshTimer: number | undefined;
    const activeThreadID = threadID;

    async function refresh(): Promise<void> {
      try {
        const result = await window.wuu.listManagedProcesses(activeThreadID);
        if (disposed) {
          return;
        }
        const incoming = result.processes.filter(isManagedProcessLive);
        setManagedProcesses((current) => {
          return Object.fromEntries(incoming.map((process) => [
            process.id,
            preferManagedProcess(current[process.id], process),
          ]));
        });
        refreshTimer = window.setTimeout(() => void refresh(), 1500);
      } catch {
        if (!disposed) {
          refreshTimer = window.setTimeout(() => void refresh(), 3000);
        }
      }
    }

    void refresh();
    return () => {
      disposed = true;
      if (refreshTimer !== undefined) {
        window.clearTimeout(refreshTimer);
      }
    };
  }, [thread?.id]);

  useEffect(() => {
    const root = document.documentElement;
    root.classList.toggle("resizing-workspace-terminal-split", resizingNavigation);
    if (!resizingNavigation) {
      return () => root.classList.remove("resizing-workspace-terminal-split");
    }

    function handlePointerMove(event: PointerEvent): void {
      const session = navigationResizeRef.current;
      if (session) {
        setTerminalNavigationWidth(session.startWidth + event.clientX - session.startX);
      }
    }

    function finishResize(): void {
      navigationResizeRef.current = null;
      setResizingNavigation(false);
    }

    window.addEventListener("pointermove", handlePointerMove);
    window.addEventListener("pointerup", finishResize);
    window.addEventListener("pointercancel", finishResize);
    return () => {
      root.classList.remove("resizing-workspace-terminal-split");
      window.removeEventListener("pointermove", handlePointerMove);
      window.removeEventListener("pointerup", finishResize);
      window.removeEventListener("pointercancel", finishResize);
    };
  }, [resizingNavigation]);

  useEffect(() => {
    if (!requestedRun) {
      return;
    }
    const next = selectAgentRun(groups, requestedRun);
    if (next) {
      setSelectedResourceID(next.toolCallID);
    }
  }, [groups, requestedRun?.requestID, requestedRun?.threadID, requestedRun?.turnID, requestedRun?.toolCallID]);

  useEffect(() => {
    if (userTerminals.some((terminal) => terminal.id === selectedResourceID) || selectedRun) {
      return;
    }
    setSelectedResourceID(userTerminals[0]?.id ?? runs[0]?.toolCallID ?? "");
  }, [runs, selectedResourceID, selectedRun, userTerminals]);

  function createUserTerminal(): void {
    if (!hostSupports("startTerminalSession")) return;
    const ordinal = nextUserTerminalOrdinalRef.current;
    nextUserTerminalOrdinalRef.current += 1;
    const id = `user-terminal:${ordinal}`;
    setUserTerminals((current) => [...current, { id, ordinal, state: "starting" }]);
    setSelectedResourceID(id);
  }

  function closeUserTerminal(id: string): void {
    const index = userTerminals.findIndex((terminal) => terminal.id === id);
    if (index < 0) {
      return;
    }
    const remaining = userTerminals.filter((terminal) => terminal.id !== id);
    setUserTerminals(remaining);
    if (selectedResourceID === id) {
      setSelectedResourceID(
        remaining[index]?.id
          ?? remaining[index - 1]?.id
          ?? runs[0]?.toolCallID
          ?? "",
      );
    }
  }

  function setTerminalNavigationWidth(width: number): void {
    const next = clampTerminalNavigationWidth(width);
    window.localStorage.setItem(WORKSPACE_TERMINAL_NAVIGATION_WIDTH_KEY, String(next));
    setNavigationWidth(next);
  }

  function startNavigationResize(event: ReactPointerEvent<HTMLDivElement>): void {
    if (event.button !== 0) {
      return;
    }
    event.preventDefault();
    navigationResizeRef.current = { startX: event.clientX, startWidth: navigationWidth };
    setResizingNavigation(true);
  }

  function handleNavigationResizeKeyDown(event: ReactKeyboardEvent<HTMLDivElement>): void {
    if (event.key === "ArrowLeft") {
      event.preventDefault();
      setTerminalNavigationWidth(navigationWidth - WORKSPACE_TERMINAL_NAVIGATION_WIDTH_STEP);
    } else if (event.key === "ArrowRight") {
      event.preventDefault();
      setTerminalNavigationWidth(navigationWidth + WORKSPACE_TERMINAL_NAVIGATION_WIDTH_STEP);
    } else if (event.key === "Home") {
      event.preventDefault();
      setTerminalNavigationWidth(WORKSPACE_TERMINAL_NAVIGATION_MIN_WIDTH);
    } else if (event.key === "End") {
      event.preventDefault();
      setTerminalNavigationWidth(WORKSPACE_TERMINAL_NAVIGATION_MAX_WIDTH);
    }
  }

  if (!activeContext?.cwd) {
    return <WorkspacePanelEmpty title={t("workspace.files.noProject")} hint={t("workspace.terminal.noProjectDescription")} icon={<Terminal size={24} />} />;
  }

  return (
    <div
      className={`workspace-terminal-workspace${resizingNavigation ? " resizing" : ""}${standaloneAgentRun ? " standalone-agent-run" : ""}`}
      data-wuu-component="workspace-terminal-layout"
      data-wuu-state={standaloneAgentRun ? "standalone" : "split"}
      style={{ "--workspace-terminal-navigation-width": `${navigationWidth}px` } as CSSProperties}
    >
      <nav
        className="workspace-terminal-navigation"
        data-wuu-component="workspace-terminal-navigation"
        aria-label={t("workspace.terminal.resources")}
      >
        <button
          className="workspace-terminal-new"
          type="button"
          aria-label={t("workspace.terminal.newTerminal")}
          title={t("workspace.terminal.newTerminal")}
          disabled={!hostSupports("startTerminalSession")}
          onClick={createUserTerminal}
        >
          <Plus size={16} />
        </button>
        <div className="workspace-terminal-run-list">
          {userTerminals.map((terminal, index) => {
            const name = userTerminalName(terminal, t("workspace.terminal.interactiveTerminal"));
            return (
              <div
                className={`workspace-terminal-resource-item${index === 0 ? " first" : ""}${selectedResourceID === terminal.id ? " active" : ""}`}
                data-wuu-component="workspace-terminal-item"
                data-wuu-active={selectedResourceID === terminal.id}
                key={terminal.id}
              >
                <button
                  className={`workspace-terminal-resource${selectedResourceID === terminal.id ? " active" : ""}`}
                  type="button"
                  onClick={() => setSelectedResourceID(terminal.id)}
                >
                  <UserTerminalStatusIcon state={terminal.state} />
                  <span className="workspace-terminal-resource-copy">
                    <span className="workspace-terminal-resource-name">{name}</span>
                    {terminal.state !== "ready" ? (
                      <span className="workspace-terminal-resource-meta">{userTerminalStatusLabel(terminal.state)}</span>
                    ) : null}
                  </span>
                </button>
                <button
                  className="workspace-terminal-resource-close"
                  type="button"
                  aria-label={t("workspace.terminal.closeTerminal", { name })}
                  title={t("workspace.terminal.closeTerminal", { name })}
                  onClick={() => closeUserTerminal(terminal.id)}
                >
                  <X size={13} />
                </button>
              </div>
            );
          })}
          {runs.map((run) => {
            const process = run.processID ? managedProcesses[run.processID] : undefined;
            const selected = selectedResourceID === run.toolCallID
              || (run.processID !== undefined && run.processID === selectedRun?.processID);
            return (
              <button
                className={`workspace-terminal-resource workspace-terminal-run${userTerminals.length === 0 ? " first" : ""}${selected ? " active" : ""}`}
                data-wuu-component="workspace-terminal-item"
                data-wuu-active={selected}
                type="button"
                key={run.toolCallID}
                onClick={() => setSelectedResourceID(run.toolCallID)}
              >
                <RunStatusIcon run={run} process={process} />
                <span className="workspace-terminal-resource-copy">
                  <TruncatedText className="workspace-terminal-resource-name" text={run.command} />
                </span>
              </button>
            );
          })}
          {userTerminals.length === 0 && runs.length === 0 ? (
            <div className="workspace-terminal-no-runs">{t("workspace.terminal.noRuns")}</div>
          ) : null}
        </div>
      </nav>
      <div
        className="workspace-terminal-resizer"
        role="separator"
        aria-label={t("workspace.terminal.resizeNavigation")}
        aria-orientation="vertical"
        aria-valuemin={WORKSPACE_TERMINAL_NAVIGATION_MIN_WIDTH}
        aria-valuemax={WORKSPACE_TERMINAL_NAVIGATION_MAX_WIDTH}
        aria-valuenow={Math.round(navigationWidth)}
        tabIndex={0}
        onDoubleClick={() => setTerminalNavigationWidth(WORKSPACE_TERMINAL_NAVIGATION_DEFAULT_WIDTH)}
        onKeyDown={handleNavigationResizeKeyDown}
        onPointerDown={startNavigationResize}
      />
      <div className="workspace-terminal-content" data-wuu-component="workspace-terminal-content">
        {userTerminals.map((terminal) => (
          <UserTerminalPane
            active={selectedResourceID === terminal.id}
            activeContext={activeContext}
            key={terminal.id}
            resourceID={terminal.id}
            onShellChange={handleUserTerminalShellChange}
            onStateChange={handleUserTerminalStateChange}
          />
        ))}
        {selectedRun ? (
          <AgentTerminalPane
            key={selectedRun.toolCallID}
            run={selectedRun}
            process={selectedRun.processID ? managedProcesses[selectedRun.processID] : undefined}
            onProcessChange={handleManagedProcessChange}
            onCreateTerminal={createUserTerminal}
          />
        ) : null}
        {userTerminals.length === 0 && !selectedRun ? (
          <WorkspacePanelEmpty
            title={t("workspace.terminal.noRuns")}
            hint={t("workspace.terminal.noRunsDescription")}
            icon={<Terminal size={24} />}
          />
        ) : null}
      </div>
    </div>
  );
}

function clampTerminalNavigationWidth(width: number): number {
  return Math.min(
    WORKSPACE_TERMINAL_NAVIGATION_MAX_WIDTH,
    Math.max(WORKSPACE_TERMINAL_NAVIGATION_MIN_WIDTH, width),
  );
}

function readStoredTerminalNavigationWidth(): number {
  const stored = Number(window.localStorage.getItem(WORKSPACE_TERMINAL_NAVIGATION_WIDTH_KEY));
  return Number.isFinite(stored) && stored > 0
    ? clampTerminalNavigationWidth(stored)
    : WORKSPACE_TERMINAL_NAVIGATION_DEFAULT_WIDTH;
}

function managedRunFromProcess(threadID: string, process: ManagedProcessSummary): AgentRunRecord {
  return {
    kind: "agent_run",
    execution: "managed",
    threadID,
    turnID: "",
    toolCallID: `managed:${process.id}`,
    command: process.command,
    status: "incomplete",
    timedOut: false,
    truncated: false,
    processID: process.id,
    tty: process.tty ?? false,
  };
}

function userTerminalName(terminal: UserTerminalResource, fallback: string): string {
  const base = terminal.shell?.split("/").filter(Boolean).at(-1) ?? fallback;
  return terminal.ordinal === 1 ? base : `${base} ${terminal.ordinal}`;
}

function UserTerminalStatusIcon({ state }: { state: WorkspaceTerminalState }): JSX.Element {
  switch (state) {
    case "ready":
      return <SquareTerminal className="icon live" />;
    case "exited":
      return <CheckCircle2 className="icon completed" />;
    case "error":
      return <XCircle className="icon failed" />;
    case "starting":
      return <Clock3 className="icon" />;
  }
}

function userTerminalStatusLabel(state: WorkspaceTerminalState): string {
  switch (state) {
    case "ready":
      return translateCurrent("workspace.terminal.status.interactive");
    case "exited":
      return translateCurrent("workspace.terminal.status.stopped");
    case "error":
      return translateCurrent("workspace.terminal.status.failed");
    case "starting":
      return translateCurrent("workspace.terminal.status.starting");
  }
}

function RunStatusIcon({
  run,
  process,
}: {
  run: AgentRunRecord;
  process?: ManagedProcessSummary;
}): JSX.Element {
  if (process ? process.status === "failed" : run.status === "failed") {
    return <XCircle className="icon failed" />;
  }
  if (run.execution === "managed" && (!process || isManagedProcessLive(process))) {
    return <Clock3 className="icon live" />;
  }
  if (process?.status === "stopped" || run.status === "completed") {
    return <CheckCircle2 className="icon completed" />;
  }
  return <Clock3 className="icon" />;
}

function isManagedProcessLive(process: ManagedProcessSummary): boolean {
  return process.status === "starting" || process.status === "running" || process.status === "stopping";
}

function preferManagedProcess(
  current: ManagedProcessSummary | undefined,
  next: ManagedProcessSummary,
): ManagedProcessSummary {
  if (!current) {
    return next;
  }
  if (!isManagedProcessLive(current) && isManagedProcessLive(next)) {
    return current;
  }
  return Date.parse(next.updated_at) < Date.parse(current.updated_at) ? current : next;
}

function AgentTerminalPane({
  run,
  process,
  onProcessChange,
  onCreateTerminal,
}: {
  run: AgentRunRecord;
  process?: ManagedProcessSummary;
  onProcessChange: (process: ManagedProcessSummary) => void;
  onCreateTerminal: () => void;
}): JSX.Element {
  const { locale, t } = useI18n();
  const containerRef = useRef<HTMLDivElement | null>(null);
  const terminalRef = useRef<XtermTerminal | null>(null);
  const processRef = useRef<ManagedProcessSummary | undefined>(process);
  const onProcessChangeRef = useRef(onProcessChange);
  const [currentProcess, setCurrentProcess] = useState(process);
  const [stopping, setStopping] = useState(false);
  const [terminalError, setTerminalError] = useState<string | undefined>();
  const processID = run.processID;
  const live = run.execution === "managed" && (!currentProcess || isManagedProcessLive(currentProcess));

  useEffect(() => {
    onProcessChangeRef.current = onProcessChange;
  }, [onProcessChange]);

  useEffect(() => {
    if (!process) {
      return;
    }
    const next = preferManagedProcess(processRef.current, process);
    processRef.current = next;
    setCurrentProcess(next);
    const terminal = terminalRef.current;
    if (terminal) {
      terminal.options.disableStdin = !(next.tty && next.input_available && isManagedProcessLive(next));
    }
  }, [process]);

  useEffect(() => {
    const container = containerRef.current;
    if (!container) {
      return undefined;
    }

    let disposed = false;
    let resizeFrame: number | undefined;
    let didInitialResize = false;
    let offset = 0;
    setTerminalError(undefined);

    const terminal = new XtermTerminal(workspaceTerminalOptions({
      interactive: run.execution === "managed" && run.tty,
      readOnly: true,
      host: container,
    }));
    const fitAddon = new FitAddon();
    terminal.loadAddon(fitAddon);
    terminal.open(container);
    terminalRef.current = terminal;
    const stopObservingTheme = observeAppliedTheme((theme) => {
      terminal.options.theme = workspaceTerminalTheme(theme, container);
    });

    function updateProcess(next: ManagedProcessSummary): void {
      const preferred = preferManagedProcess(processRef.current, next);
      processRef.current = preferred;
      terminal.options.disableStdin = !(
        preferred.tty && preferred.input_available && isManagedProcessLive(preferred)
      );
      setCurrentProcess(preferred);
      onProcessChangeRef.current(preferred);
    }

    function fitAndResize(): void {
      if (disposed) {
        return;
      }
      try {
        fitAddon.fit();
      } catch {
        return;
      }
      const current = processRef.current;
      if (processID && current?.tty && isManagedProcessLive(current)) {
        void window.wuu.resizeManagedProcess(run.threadID, processID, terminal.cols, terminal.rows).catch((error) => {
          if (!disposed && isManagedProcessLive(processRef.current ?? current)) {
            setTerminalError(desktopApiErrorMessage(error, translateCurrent("workspace.terminal.resizeFailed")));
          }
        });
      }
    }

    const dataDisposable = terminal.onData((data) => {
      const current = processRef.current;
      if (!processID || !current?.tty || !current.input_available || !isManagedProcessLive(current)) {
        return;
      }
      void window.wuu.writeManagedProcess(run.threadID, processID, data).catch((error) => {
        if (!disposed) {
          setTerminalError(desktopApiErrorMessage(error, translateCurrent("workspace.terminal.writeFailed")));
        }
      });
    });
    const resizeObserver = new ResizeObserver(() => {
      if (resizeFrame !== undefined) {
        window.cancelAnimationFrame(resizeFrame);
      }
      resizeFrame = window.requestAnimationFrame(fitAndResize);
    });
    resizeObserver.observe(container);
    resizeFrame = window.requestAnimationFrame(fitAndResize);

    if (run.execution === "snapshot" || !processID) {
      if (run.stdout) {
        terminal.write(run.stdout);
      }
      if (run.stderr) {
        terminal.write(run.stderr);
      }
      if (!run.stdout && !run.stderr) {
        terminal.writeln(t("workspace.terminal.noOutput"));
      }
      if (run.truncated) {
        terminal.writeln("");
        terminal.writeln(`[${t("workspace.terminal.retainedOutputTruncated")}]`);
      }
    } else {
      const managedProcessID = processID;
      async function readManagedOutput(): Promise<void> {
        while (!disposed) {
          try {
            const result = await window.wuu.readManagedProcess({
              thread_id: run.threadID,
              process_id: managedProcessID,
              offset_bytes: offset,
              max_bytes: 512 * 1024,
              wait_ms: 10000,
            });
            if (disposed) {
              return;
            }
            setTerminalError(undefined);
            if (result.truncated && result.start_offset > offset) {
              terminal.writeln(`[${translateCurrent("workspace.terminal.earlierOutputTruncated")}]`);
            }
            if (result.output) {
              terminal.write(result.output);
            }
            offset = result.end_offset;
            updateProcess(result.process);
            if (!didInitialResize) {
              didInitialResize = true;
              fitAndResize();
            }
            if (!isManagedProcessLive(result.process)) {
              return;
            }
          } catch (error) {
            if (!disposed) {
              setTerminalError(desktopApiErrorMessage(error, translateCurrent("workspace.terminal.readFailed")));
            }
            await new Promise((resolve) => window.setTimeout(resolve, 1500));
            const current = processRef.current;
            if (disposed || (current && !isManagedProcessLive(current))) {
              return;
            }
          }
        }
      }
      void readManagedOutput();
    }

    return () => {
      disposed = true;
      if (resizeFrame !== undefined) {
        window.cancelAnimationFrame(resizeFrame);
      }
      dataDisposable.dispose();
      resizeObserver.disconnect();
      stopObservingTheme();
      terminal.dispose();
      terminalRef.current = null;
    };
  }, [locale, processID, run.execution, run.stderr, run.stdout, run.threadID, run.toolCallID, run.truncated, run.tty]);

  async function stopProcess(): Promise<void> {
    if (!processID || stopping) {
      return;
    }
    setStopping(true);
    setTerminalError(undefined);
    try {
      const result = await window.wuu.stopManagedProcess(run.threadID, processID);
      processRef.current = result.process;
      setCurrentProcess(result.process);
      onProcessChange(result.process);
    } catch (error) {
      setTerminalError(desktopApiErrorMessage(error, t("workspace.terminal.stopFailed")));
    } finally {
      setStopping(false);
    }
  }

  return (
    <article className="workspace-agent-terminal" data-tool-call-id={run.toolCallID}>
      <header className="workspace-agent-terminal-toolbar">
        <TruncatedText as="div" className="workspace-agent-terminal-command" text={run.command} />
        <div className="workspace-agent-terminal-actions">
          <button
            className="workspace-agent-terminal-new"
            type="button"
            aria-label={t("workspace.terminal.newTerminal")}
            title={t("workspace.terminal.newTerminal")}
            disabled={!hostSupports("startTerminalSession")}
            onClick={onCreateTerminal}
          >
            <Plus size={14} />
          </button>
          <span className={`workspace-agent-run-status ${currentProcess?.status ?? run.status}`}>
            {managedRunStatusLabel(run, currentProcess, stopping)}
          </span>
          {live && processID ? (
            <button type="button" className="workspace-agent-terminal-stop" disabled={stopping} onClick={() => void stopProcess()}>
              <Square size={12} fill="currentColor" />
              {stopping ? t("workspace.terminal.stopping") : t("workspace.terminal.stop")}
            </button>
          ) : null}
        </div>
      </header>
      {terminalError ? <div className="workspace-agent-terminal-error">{terminalError}</div> : null}
      <div
        className="workspace-terminal-screen"
        data-wuu-component="workspace-terminal-screen"
        onMouseDown={() => terminalRef.current?.focus()}
      >
        <div className="workspace-terminal-host" ref={containerRef} />
      </div>
    </article>
  );
}

function runStatusLabel(run: AgentRunRecord): string {
  switch (run.status) {
    case "completed":
      return translateCurrent("workspace.terminal.status.completed");
    case "failed":
      return translateCurrent("workspace.terminal.status.failed");
    case "interrupted":
      return translateCurrent("workspace.terminal.status.interrupted");
    case "incomplete":
      return translateCurrent("workspace.terminal.status.incomplete");
  }
}

function managedRunStatusLabel(
  run: AgentRunRecord,
  process: ManagedProcessSummary | undefined,
  stopping: boolean,
): string {
  if (stopping || process?.status === "stopping") {
    return translateCurrent("workspace.terminal.status.stopping");
  }
  switch (process?.status) {
    case "starting":
      return translateCurrent("workspace.terminal.status.starting");
    case "running":
      return process.input_available
        ? translateCurrent("workspace.terminal.status.interactive")
        : translateCurrent("workspace.terminal.status.running");
    case "stopped":
      return translateCurrent("workspace.terminal.status.stopped");
    case "failed":
      return translateCurrent("workspace.terminal.status.failed");
    default:
      return run.execution === "managed"
        ? translateCurrent("workspace.terminal.status.running")
        : runStatusLabel(run);
  }
}

function UserTerminalPane({
  active,
  activeContext,
  resourceID,
  onShellChange,
  onStateChange,
}: {
  active: boolean;
  activeContext?: RuntimeContext;
  resourceID: string;
  onShellChange: (id: string, shell: string) => void;
  onStateChange: (id: string, state: WorkspaceTerminalState) => void;
}): JSX.Element {
  const { t } = useI18n();
  const containerRef = useRef<HTMLDivElement | null>(null);
  const terminalRef = useRef<XtermTerminal | null>(null);
  const sessionIDRef = useRef<string | undefined>(undefined);
  const pendingTerminalEventsRef = useRef(new Map<string, TerminalSessionEvent[]>());
  const [terminalState, setTerminalState] = useState<WorkspaceTerminalState>("starting");
  const [restartKey, setRestartKey] = useState(0);
  const workspaceRoot = activeContext?.cwd;

  useEffect(() => {
    onStateChange(resourceID, terminalState);
  }, [onStateChange, resourceID, terminalState]);

  useEffect(() => {
    if (active) {
      terminalRef.current?.focus();
    }
  }, [active]);

  useEffect(() => {
    const container = containerRef.current;
    if (!workspaceRoot || !container) {
      return undefined;
    }

    let disposed = false;
    let resizeFrame: number | undefined;
    setTerminalState("starting");
    pendingTerminalEventsRef.current.clear();

    const terminal = new XtermTerminal(workspaceTerminalOptions({ interactive: true, host: container }));
    const fitAddon = new FitAddon();
    terminal.loadAddon(fitAddon);
    terminal.open(container);
    terminal.focus();
    terminalRef.current = terminal;
    const stopObservingTheme = observeAppliedTheme((theme) => {
      terminal.options.theme = workspaceTerminalTheme(theme, container);
    });

    function fitAndResize(): void {
      if (disposed) {
        return;
      }
      try {
        fitAddon.fit();
      } catch {
        return;
      }
      const id = sessionIDRef.current;
      if (id) {
        void window.wuu.resizeTerminalSession(id, terminal.cols, terminal.rows);
      }
    }

    const dataDisposable = terminal.onData((data) => {
      const id = sessionIDRef.current;
      if (id) {
        void window.wuu.writeTerminalSession(id, data);
      }
    });
    const resizeObserver = new ResizeObserver(() => {
      if (resizeFrame !== undefined) {
        window.cancelAnimationFrame(resizeFrame);
      }
      resizeFrame = window.requestAnimationFrame(fitAndResize);
    });
    resizeObserver.observe(container);
    resizeFrame = window.requestAnimationFrame(fitAndResize);

    function bufferTerminalEvent(event: TerminalSessionEvent): void {
      const events = pendingTerminalEventsRef.current.get(event.id) ?? [];
      pendingTerminalEventsRef.current.set(
        event.id,
        appendPendingTerminalEvent(events, event),
      );
      while (pendingTerminalEventsRef.current.size > WORKSPACE_TERMINAL_PENDING_EVENT_IDS) {
        const firstID = pendingTerminalEventsRef.current.keys().next().value;
        if (!firstID) {
          break;
        }
        pendingTerminalEventsRef.current.delete(firstID);
      }
    }

    function handleTerminalEvent(event: TerminalSessionEvent): void {
      if (event.type === "data") {
        terminal.write(event.text);
        return;
      }
      if (event.type === "exit") {
        terminal.writeln("");
        terminal.writeln(`[${terminalExitText(event)}]`);
        setTerminalState("exited");
        sessionIDRef.current = undefined;
        return;
      }
      terminal.writeln("");
      terminal.writeln(`[${translateCurrent("workspace.terminal.error", { message: event.message })}]`);
      setTerminalState("error");
      sessionIDRef.current = undefined;
    }

    function flushPendingTerminalEvents(id: string): void {
      const events = pendingTerminalEventsRef.current.get(id);
      pendingTerminalEventsRef.current.clear();
      if (!events) {
        return;
      }
      for (const event of events) {
        handleTerminalEvent(event);
      }
    }

    const unsubscribeTerminal = window.wuu.onTerminalEvent((event) => {
      const sessionID = sessionIDRef.current;
      if (!sessionID) {
        bufferTerminalEvent(event);
        return;
      }
      if (event.id === sessionID) {
        handleTerminalEvent(event);
      }
    });

    async function startSession(): Promise<void> {
      try {
        fitAndResize();
        const started = await window.wuu.startTerminalSession({
          cols: terminal.cols,
          rows: terminal.rows,
          cwd: workspaceRoot
        });
        if (disposed) {
          void window.wuu.stopTerminalSession(started.id);
          return;
        }
        sessionIDRef.current = started.id;
        onShellChange(resourceID, started.shell);
        setTerminalState("ready");
        flushPendingTerminalEvents(started.id);
        fitAndResize();
        terminal.focus();
      } catch (error) {
        terminal.writeln(desktopApiErrorMessage(error, translateCurrent("workspace.terminal.startFailed")));
        setTerminalState("error");
      }
    }

    void startSession();

    return () => {
      disposed = true;
      if (resizeFrame !== undefined) {
        window.cancelAnimationFrame(resizeFrame);
      }
      const sessionID = sessionIDRef.current;
      sessionIDRef.current = undefined;
      if (sessionID) {
        void window.wuu.stopTerminalSession(sessionID);
      }
      unsubscribeTerminal();
      stopObservingTheme();
      dataDisposable.dispose();
      resizeObserver.disconnect();
      pendingTerminalEventsRef.current.clear();
      terminal.dispose();
      terminalRef.current = null;
    };
  }, [onShellChange, resourceID, restartKey, workspaceRoot]);

  if (!workspaceRoot) {
    return <WorkspacePanelEmpty title={t("workspace.files.noProject")} hint={t("workspace.terminal.noProjectDescription")} icon={<Terminal size={24} />} />;
  }

  return (
    <div className="workspace-terminal-panel" hidden={!active} data-wuu-component="workspace-terminal">
      <div
        className="workspace-terminal-screen"
        data-wuu-component="workspace-terminal-screen"
        onMouseDown={() => terminalRef.current?.focus()}
      >
        <div className="workspace-terminal-host" ref={containerRef} />
      </div>
      {isTouchWebShell() && terminalState === 'ready' && <div className="workspace-terminal-touch-keys" aria-label="Terminal keys">
        {([['Esc','\x1b'],['Tab','\t'],['Ctrl-C','\x03'],['↑','\x1b[A'],['↓','\x1b[B'],['←','\x1b[D'],['→','\x1b[C']] as const).map(([label,data])=><button key={label} type="button" onPointerDown={event=>event.preventDefault()} onClick={()=>{const id=sessionIDRef.current;if(id)void window.wuu.writeTerminalSession(id,data).catch(()=>setTerminalState('error'));}}>{label}</button>)}
      </div>}
      {terminalState === "exited" || terminalState === "error" ? (
        <button className="workspace-terminal-restart" type="button" onClick={() => setRestartKey((current) => current + 1)}>
          {t("workspace.terminal.restart")}
        </button>
      ) : null}
    </div>
  );
}
