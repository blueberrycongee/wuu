// React 19.2 keeps component/scheduler measures in Chromium's User Timing
// buffer on every dev render. Streaming otherwise retains them for the entire
// window lifetime, even without DevTools open. Remove this workaround when
// React clears its own User Timing entries. Clearing the timeline preserves
// events already delivered to observers or recorded in a DevTools trace.
if (import.meta.env.DEV) {
  const observer = new PerformanceObserver((list) => {
    const names = new Set<string>();
    for (const entry of list.getEntries()) {
      const devtools = (entry as PerformanceMeasure).detail?.devtools;
      if (devtools?.track === "Components ⚛" || devtools?.trackGroup === "Scheduler ⚛") {
        names.add(entry.name);
      }
    }
    for (const name of names) performance.clearMeasures(name);
  });
  observer.observe({ type: "measure", buffered: true });
  import.meta.hot?.dispose(() => observer.disconnect());
}
