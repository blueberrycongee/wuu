type SessionSwitchPhase =
  | "action-start"
  | "state-update-issued"
  | "pane-layout-effect"
  | "scroll-restore-start"
  | "scroll-restore-end"
  | "runtime-loaded"
  | "next-animation-frame";

export type SessionSwitchPerformanceSample = {
  id: number;
  threadID: string;
  kind: "same-runtime" | "cross-runtime" | "unknown";
  startedAt: number;
  phases: Partial<Record<SessionSwitchPhase, number>>;
  durations: Partial<Record<SessionSwitchPhase, number>>;
  completedAt?: number;
  totalDuration?: number;
  paneRenders: Array<{
    phase: "mount" | "update" | "nested-update";
    actualDuration: number;
    baseDuration: number;
  }>;
};

export const SESSION_SWITCH_PERF_ENABLED = import.meta.env.DEV;
const MAX_SAMPLES = 24;
let nextID = 0;
const samples: SessionSwitchPerformanceSample[] = [];
const activeByThreadID = new Map<string, SessionSwitchPerformanceSample>();

function logSample(sample: SessionSwitchPerformanceSample, message: string): void {
  if (!SESSION_SWITCH_PERF_ENABLED) {
    return;
  }
  console.info(
    `[session-perf] ${message} ${JSON.stringify({
      id: sample.id,
      threadID: sample.threadID,
      kind: sample.kind,
      phases: sample.phases,
      durations: sample.durations,
      totalDuration: sample.totalDuration,
      paneRenders: sample.paneRenders,
    })}`,
  );
}

export function beginSessionSwitch(
  threadID: string,
  kind: SessionSwitchPerformanceSample["kind"],
): number | undefined {
  if (!SESSION_SWITCH_PERF_ENABLED) {
    return undefined;
  }
  const sample: SessionSwitchPerformanceSample = {
    id: ++nextID,
    threadID,
    kind,
    startedAt: performance.now(),
    phases: { "action-start": performance.now() },
    durations: { "action-start": 0 },
    paneRenders: [],
  };
  samples.push(sample);
  if (samples.length > MAX_SAMPLES) {
    samples.shift();
  }
  activeByThreadID.set(threadID, sample);
  logSample(sample, "start");
  return sample.id;
}

function sampleForThread(threadID: string): SessionSwitchPerformanceSample | undefined {
  return activeByThreadID.get(threadID);
}

export function markSessionSwitch(
  threadID: string,
  phase: SessionSwitchPhase,
): void {
  if (!SESSION_SWITCH_PERF_ENABLED) {
    return;
  }
  const sample = sampleForThread(threadID);
  if (!sample) {
    return;
  }
  sample.phases[phase] = performance.now();
  const elapsed = sample.phases[phase]! - sample.startedAt;
  sample.durations[phase] = elapsed;
  if (phase === "next-animation-frame") {
    sample.completedAt = sample.phases[phase];
    sample.totalDuration = elapsed;
  }
  logSample(sample, `${phase} +${elapsed.toFixed(2)}ms`);
  if (phase === "next-animation-frame" || phase === "runtime-loaded") {
    window.setTimeout(() => {
      if (activeByThreadID.get(threadID) === sample) {
        activeByThreadID.delete(threadID);
      }
    }, 2_000);
  }
}

export function recordSessionSwitchPaneRender(
  threadID: string,
  phase: "mount" | "update" | "nested-update",
  actualDuration: number,
  baseDuration: number,
): void {
  if (!SESSION_SWITCH_PERF_ENABLED) {
    return;
  }
  const sample = sampleForThread(threadID);
  if (!sample) {
    return;
  }
  sample.paneRenders.push({ phase, actualDuration, baseDuration });
  logSample(
    sample,
    `pane-render ${phase} actual=${actualDuration.toFixed(2)}ms base=${baseDuration.toFixed(2)}ms`,
  );
}

export function sessionSwitchPerformanceSamples(): SessionSwitchPerformanceSample[] {
  return samples.map((sample) => ({
    ...sample,
    phases: { ...sample.phases },
    durations: { ...sample.durations },
    paneRenders: sample.paneRenders.map((render) => ({ ...render })),
  }));
}

if (SESSION_SWITCH_PERF_ENABLED) {
  (window as Window & {
    __wuuSessionSwitchPerf?: {
      samples: () => SessionSwitchPerformanceSample[];
      clear: () => void;
    };
  }).__wuuSessionSwitchPerf = {
    samples: sessionSwitchPerformanceSamples,
    clear: () => {
      samples.length = 0;
      activeByThreadID.clear();
    },
  };
}
