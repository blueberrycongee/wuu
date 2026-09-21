import { isTouchWebShell } from "./ComposerFocus";
import { hostSupports } from "./HostCapabilities";
import type { FitAddon as XtermFitAddon } from "@xterm/addon-fit";
import type { Terminal as XtermTerminal, ITerminalOptions, ITheme } from "@xterm/xterm";
import { Square, Terminal } from "lucide-react";
import {
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
import { codeEditorTypography, observeAppearance } from "./AppearancePreferences";
import {
  agentRunGroupsForThread,
  selectAgentRun,
  type AgentRunLocator,
  type AgentRunRecord,
} from "./TerminalRuns";
import { WorkspacePanelLoading } from "./LoadingViews";
import { WorkspacePanelEmpty } from "./WorkspaceFiles";
import { desktopApiErrorMessage } from "./WorkspaceReviewHelpers";
import { translateCurrent, useI18n } from "./i18n";
import { TruncatedText } from "./TruncatedText";
import {
  createWindowResizeSettleScheduler,
  isWindowResizing,
} from "./WindowResizeState";

const WORKSPACE_TERMINAL_PENDING_EVENT_IDS = 12;
const WORKSPACE_TERMINAL_PENDING_EVENTS_PER_ID = 256;
const WORKSPACE_TERMINAL_PENDING_TEXT_PER_ID = 512 * 1024;
const WORKSPACE_TERMINAL_STARTING_RAIL_DELAY_MS = 120;

type WorkspaceTerminalRuntime = {
  Terminal: typeof import("@xterm/xterm").Terminal;
  FitAddon: typeof import("@xterm/addon-fit").FitAddon;
};

let workspaceTerminalRuntime: Promise<WorkspaceTerminalRuntime> | undefined;

function loadWorkspaceTerminalRuntime(): Promise<WorkspaceTerminalRuntime> {
  workspaceTerminalRuntime ??= Promise.all([
    import("@xterm/xterm"),
    import("@xterm/addon-fit"),
    import("@xterm/xterm/css/xterm.css"),
  ]).then(
    ([xterm, fit]) => ({
      Terminal: xterm.Terminal,
      FitAddon: fit.FitAddon,
    }),
    (error: unknown) => {
      workspaceTerminalRuntime = undefined;
      throw error;
    },
  );
  return workspaceTerminalRuntime;
}

export function preloadWorkspaceTerminalRuntime(): void {
  void loadWorkspaceTerminalRuntime().catch(() => undefined);
}

async function openWorkspaceTerminal(
  host: HTMLElement,
  options: ITerminalOptions,
): Promise<{ terminal: XtermTerminal; fitAddon: XtermFitAddon }> {
  const runtime = await loadWorkspaceTerminalRuntime();
  const terminal = new runtime.Terminal(options);
  const fitAddon = new runtime.FitAddon();
  terminal.loadAddon(fitAddon);
  try {
    terminal.open(host);
  } catch (error) {
    terminal.dispose();
    throw error;
  }
  return { terminal, fitAddon };
}

// A hidden panel reports a 0×0 host. Fitting then resizes the live pty down
// to a single column and throws away the layout the user had.
function fitOpenTerminal(host: HTMLElement | null, fitAddon: XtermFitAddon): boolean {
  if (!host || host.clientWidth < 2 || host.clientHeight < 2) {
    return false;
  }
  try {
    fitAddon.fit();
    return true;
  } catch {
    return false;
  }
}

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
  const typography = codeEditorTypography();
  return {
    allowTransparency: false,
    convertEol: !interactive,
    cursorBlink: interactive,
    disableStdin: readOnly,
    fontFamily: workspaceTerminalStyle(host, "--wuu-workspace-terminal-font-family", typography.fontFamily),
    fontSize: typography.fontSize,
    lineHeight: 1.6,
    scrollback: 10000,
    theme: workspaceTerminalTheme(currentAppliedTheme(), host),
  };
}

function observeTerminalAppearance(terminal: XtermTerminal, host: HTMLElement, resize: () => void): () => void {
  return observeAppearance(() => {
    const typography = codeEditorTypography();
    const family = workspaceTerminalStyle(host, "--wuu-workspace-terminal-font-family", typography.fontFamily);
    if (terminal.options.fontSize === typography.fontSize && terminal.options.fontFamily === family) return;
    terminal.options.fontSize = typography.fontSize;
    terminal.options.fontFamily = family;
    resize();
  });
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
  active = true,
  activeContext,
  thread,
  requestedRun,
}: {
  active?: boolean;
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
  const [userTerminal, setUserTerminal] = useState<UserTerminalResource | undefined>();
  const autoStartedUserTerminalRef = useRef(false);
  const [managedProcesses, setManagedProcesses] = useState<Record<string, ManagedProcessSummary>>({});
  const [managedInventoryReady, setManagedInventoryReady] = useState(() => !thread?.id);
  const runs = useMemo(
    () => Object.values(managedProcesses)
      .filter(isManagedProcessLive)
      .sort((left, right) => Date.parse(right.updated_at) - Date.parse(left.updated_at))
      .map((process) => managedRunFromProcess(thread?.id ?? process.owner_id, process)),
    [managedProcesses, thread?.id],
  );
  const selectedRun = requestedRecord
    ?? (userTerminal ? undefined : runs.find((run) => run.toolCallID === selectedResourceID) ?? runs[0]);
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
    setUserTerminal((current) => (
      current?.id === id && current.state !== state ? { ...current, state } : current
    ));
  }, []);
  const handleUserTerminalShellChange = useCallback((id: string, shell: string) => {
    setUserTerminal((current) => (
      current?.id === id && current.shell !== shell ? { ...current, shell } : current
    ));
  }, []);

  useEffect(() => {
    const threadID = thread?.id;
    if (!threadID) {
      setManagedProcesses({});
      setManagedInventoryReady(true);
      return undefined;
    }
    let disposed = false;
    let refreshTimer: number | undefined;
    const activeThreadID = threadID;
    setManagedInventoryReady(false);

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
        setManagedInventoryReady(true);
        refreshTimer = window.setTimeout(() => void refresh(), 1500);
      } catch {
        if (!disposed) {
          setManagedInventoryReady(true);
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
    if (!requestedRun) {
      return;
    }
    const next = selectAgentRun(groups, requestedRun);
    if (next) {
      setSelectedResourceID(next.toolCallID);
    }
  }, [groups, requestedRun?.requestID, requestedRun?.threadID, requestedRun?.turnID, requestedRun?.toolCallID]);

  useEffect(() => {
    if (userTerminal || selectedRun) {
      return;
    }
    setSelectedResourceID(runs[0]?.toolCallID ?? "");
  }, [runs, selectedRun, userTerminal]);

  useEffect(() => {
    if (autoStartedUserTerminalRef.current || requestedRun || userTerminal || runs.length > 0) {
      return;
    }
    if (!managedInventoryReady || !activeContext?.cwd || !hostSupports("startTerminalSession")) {
      return;
    }
    autoStartedUserTerminalRef.current = true;
    const id = "user-terminal:1";
    setUserTerminal({ id, ordinal: 1, state: "starting" });
    setSelectedResourceID(id);
  }, [activeContext?.cwd, managedInventoryReady, requestedRun, runs.length, userTerminal]);

  if (!activeContext?.cwd) {
    return <WorkspacePanelEmpty title={t("workspace.files.noProject")} hint={t("workspace.terminal.noProjectDescription")} icon={<Terminal size={24} />} />;
  }

  return (
    <div
      className="workspace-terminal-workspace"
      data-wuu-component="workspace-terminal-layout"
      data-wuu-state="standalone"
    >
      <div className="workspace-terminal-content" data-wuu-component="workspace-terminal-content">
        {userTerminal ? (
          <UserTerminalPane
            active={active}
            activeContext={activeContext}
            key={userTerminal.id}
            resourceID={userTerminal.id}
            onShellChange={handleUserTerminalShellChange}
            onStateChange={handleUserTerminalStateChange}
          />
        ) : selectedRun ? (
          <AgentTerminalPane
            active={active}
            key={selectedRun.toolCallID}
            run={selectedRun}
            process={selectedRun.processID ? managedProcesses[selectedRun.processID] : undefined}
            onProcessChange={handleManagedProcessChange}
          />
        ) : (
          <WorkspacePanelEmpty
            title={t("workspace.terminal.noRuns")}
            hint={t("workspace.terminal.noRunsDescription")}
            icon={<Terminal size={24} />}
          />
        )}
      </div>
    </div>
  );
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
  active,
  run,
  process,
  onProcessChange,
}: {
  active: boolean;
  run: AgentRunRecord;
  process?: ManagedProcessSummary;
  onProcessChange: (process: ManagedProcessSummary) => void;
}): JSX.Element {
  const { locale, t } = useI18n();
  const containerRef = useRef<HTMLDivElement | null>(null);
  const terminalRef = useRef<XtermTerminal | null>(null);
  const fitRef = useRef<() => void>(() => {});
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
    if (active) {
      fitRef.current();
    }
  }, [active]);

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
    let terminal: XtermTerminal | undefined;
    let fitAddon: XtermFitAddon | undefined;
    let dataDisposable: { dispose: () => void } | undefined;
    let resizeObserver: ResizeObserver | undefined;
    let settleResize: ReturnType<typeof createWindowResizeSettleScheduler> | undefined;
    let stopObservingTheme: (() => void) | undefined;
    let stopObservingAppearance: (() => void) | undefined;
    setTerminalError(undefined);

    function fitAndResize(): void {
      if (disposed || !terminal || !fitAddon) {
        return;
      }
      if (!fitOpenTerminal(container, fitAddon)) {
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

    fitRef.current = fitAndResize;

    void (async () => {
      let opened: { terminal: XtermTerminal; fitAddon: XtermFitAddon };
      try {
        opened = await openWorkspaceTerminal(container, workspaceTerminalOptions({
          interactive: run.execution === "managed" && run.tty,
          readOnly: true,
          host: container,
        }));
      } catch {
        if (!disposed) {
          setTerminalError(translateCurrent("workspace.terminal.startFailed"));
        }
        return;
      }
      if (disposed) {
        opened.terminal.dispose();
        return;
      }
      terminal = opened.terminal;
      fitAddon = opened.fitAddon;
      terminalRef.current = terminal;
      const openedTerminal = terminal;
      stopObservingTheme = observeAppliedTheme((theme) => {
        openedTerminal.options.theme = workspaceTerminalTheme(theme, container);
      });
      stopObservingAppearance = observeTerminalAppearance(openedTerminal, container, fitAndResize);

      function updateProcess(next: ManagedProcessSummary): void {
        const preferred = preferManagedProcess(processRef.current, next);
        processRef.current = preferred;
        openedTerminal.options.disableStdin = !(
          preferred.tty && preferred.input_available && isManagedProcessLive(preferred)
        );
        setCurrentProcess(preferred);
        onProcessChangeRef.current(preferred);
      }

      dataDisposable = openedTerminal.onData((data) => {
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
      settleResize = createWindowResizeSettleScheduler(() => {
        if (resizeFrame !== undefined) {
          window.cancelAnimationFrame(resizeFrame);
        }
        resizeFrame = window.requestAnimationFrame(fitAndResize);
      });
      resizeObserver = new ResizeObserver(() => {
        // Fitting the terminal measures the host and rewrites the pty size.
        // Do that once, when the window drag ends, not on every width change.
        if (isWindowResizing()) {
          settleResize.schedule();
          return;
        }
        if (resizeFrame !== undefined) {
          window.cancelAnimationFrame(resizeFrame);
        }
        resizeFrame = window.requestAnimationFrame(fitAndResize);
      });
      resizeObserver.observe(container);
      resizeFrame = window.requestAnimationFrame(fitAndResize);

      if (run.execution === "snapshot" || !processID) {
        if (run.stdout) {
          openedTerminal.write(run.stdout);
        }
        if (run.stderr) {
          openedTerminal.write(run.stderr);
        }
        if (!run.stdout && !run.stderr) {
          openedTerminal.writeln(t("workspace.terminal.noOutput"));
        }
        if (run.truncated) {
          openedTerminal.writeln("");
          openedTerminal.writeln(`[${t("workspace.terminal.retainedOutputTruncated")}]`);
        }
        return;
      }
      const managedProcessID = processID;
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
            openedTerminal.writeln(`[${translateCurrent("workspace.terminal.earlierOutputTruncated")}]`);
          }
          if (result.output) {
            openedTerminal.write(result.output);
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
    })();

    return () => {
      disposed = true;
      fitRef.current = () => {};
      if (resizeFrame !== undefined) {
        window.cancelAnimationFrame(resizeFrame);
      }
      dataDisposable?.dispose();
      settleResize?.cancel();
      resizeObserver?.disconnect();
      stopObservingTheme?.();
      stopObservingAppearance?.();
      terminal?.dispose();
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
  const fitRef = useRef<() => void>(() => {});
  const activeRef = useRef(active);
  const [terminalState, setTerminalState] = useState<WorkspaceTerminalState>("starting");
  const [showStartingRail, setShowStartingRail] = useState(false);
  const [restartKey, setRestartKey] = useState(0);
  const workspaceRoot = activeContext?.cwd;
  activeRef.current = active;

  useEffect(() => {
    onStateChange(resourceID, terminalState);
  }, [onStateChange, resourceID, terminalState]);

  useEffect(() => {
    if (terminalState !== "starting") {
      setShowStartingRail(false);
      return undefined;
    }
    // A warm terminal is ready on the next beat. Waiting avoids flashing the rail.
    const timeoutID = window.setTimeout(
      () => setShowStartingRail(true),
      WORKSPACE_TERMINAL_STARTING_RAIL_DELAY_MS,
    );
    return () => window.clearTimeout(timeoutID);
  }, [terminalState]);

  useEffect(() => {
    const terminal = terminalRef.current;
    if (!terminal) {
      return;
    }
    if (active) {
      fitRef.current();
      terminal.focus();
      return;
    }
    terminal.blur();
  }, [active]);

  useEffect(() => {
    const container = containerRef.current;
    if (!workspaceRoot || !container) {
      return undefined;
    }

    let disposed = false;
    let resizeFrame: number | undefined;
    let terminal: XtermTerminal | undefined;
    let fitAddon: XtermFitAddon | undefined;
    let dataDisposable: { dispose: () => void } | undefined;
    let resizeObserver: ResizeObserver | undefined;
    let settleResize: ReturnType<typeof createWindowResizeSettleScheduler> | undefined;
    let stopObservingTheme: (() => void) | undefined;
    let stopObservingAppearance: (() => void) | undefined;
    let unsubscribeTerminal: (() => void) | undefined;
    setTerminalState("starting");
    pendingTerminalEventsRef.current.clear();

    function fitAndResize(): void {
      if (disposed || !terminal || !fitAddon) {
        return;
      }
      if (!fitOpenTerminal(container, fitAddon)) {
        return;
      }
      const id = sessionIDRef.current;
      if (id) {
        void window.wuu.resizeTerminalSession(id, terminal.cols, terminal.rows);
      }
    }

    fitRef.current = fitAndResize;

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

    void (async () => {
      let opened: { terminal: XtermTerminal; fitAddon: XtermFitAddon };
      try {
        opened = await openWorkspaceTerminal(
          container,
          workspaceTerminalOptions({ interactive: true, host: container }),
        );
      } catch {
        if (!disposed) {
          setTerminalState("error");
        }
        return;
      }
      if (disposed) {
        opened.terminal.dispose();
        return;
      }
      terminal = opened.terminal;
      fitAddon = opened.fitAddon;
      const openedTerminal = terminal;
      if (activeRef.current) {
        openedTerminal.focus();
      }
      terminalRef.current = openedTerminal;
      stopObservingTheme = observeAppliedTheme((theme) => {
        openedTerminal.options.theme = workspaceTerminalTheme(theme, container);
      });
      stopObservingAppearance = observeTerminalAppearance(openedTerminal, container, fitAndResize);

      function handleTerminalEvent(event: TerminalSessionEvent): void {
        if (event.type === "data") {
          openedTerminal.write(event.text);
          return;
        }
        if (event.type === "exit") {
          openedTerminal.writeln("");
          openedTerminal.writeln(`[${terminalExitText(event)}]`);
          setTerminalState("exited");
          sessionIDRef.current = undefined;
          return;
        }
        openedTerminal.writeln("");
        openedTerminal.writeln(`[${translateCurrent("workspace.terminal.error", { message: event.message })}]`);
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

      dataDisposable = openedTerminal.onData((data) => {
        const id = sessionIDRef.current;
        if (id) {
          void window.wuu.writeTerminalSession(id, data);
        }
      });
      settleResize = createWindowResizeSettleScheduler(() => {
        if (resizeFrame !== undefined) {
          window.cancelAnimationFrame(resizeFrame);
        }
        resizeFrame = window.requestAnimationFrame(fitAndResize);
      });
      resizeObserver = new ResizeObserver(() => {
        if (isWindowResizing()) {
          settleResize?.schedule();
          return;
        }
        if (resizeFrame !== undefined) {
          window.cancelAnimationFrame(resizeFrame);
        }
        resizeFrame = window.requestAnimationFrame(fitAndResize);
      });
      resizeObserver.observe(container);
      resizeFrame = window.requestAnimationFrame(fitAndResize);
      unsubscribeTerminal = window.wuu.onTerminalEvent((event) => {
        const sessionID = sessionIDRef.current;
        if (!sessionID) {
          bufferTerminalEvent(event);
          return;
        }
        if (event.id === sessionID) {
          handleTerminalEvent(event);
        }
      });

      try {
        fitAndResize();
        const started = await window.wuu.startTerminalSession({
          cols: openedTerminal.cols,
          rows: openedTerminal.rows,
          cwd: workspaceRoot,
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
        if (activeRef.current) {
          openedTerminal.focus();
        }
      } catch (error) {
        if (disposed) {
          return;
        }
        openedTerminal.writeln(desktopApiErrorMessage(error, translateCurrent("workspace.terminal.startFailed")));
        setTerminalState("error");
      }
    })();

    return () => {
      disposed = true;
      fitRef.current = () => {};
      if (resizeFrame !== undefined) {
        window.cancelAnimationFrame(resizeFrame);
      }
      const sessionID = sessionIDRef.current;
      sessionIDRef.current = undefined;
      if (sessionID) {
        void window.wuu.stopTerminalSession(sessionID);
      }
      unsubscribeTerminal?.();
      stopObservingTheme?.();
      stopObservingAppearance?.();
      dataDisposable?.dispose();
      settleResize?.cancel();
      resizeObserver?.disconnect();
      pendingTerminalEventsRef.current.clear();
      terminal?.dispose();
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
      {showStartingRail ? <WorkspacePanelLoading /> : null}
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
