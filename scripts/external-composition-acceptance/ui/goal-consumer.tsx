import type { GoalControlRequestV1, GoalControlResultV1, PluginGenerationApi, UISlotRenderProps } from "@wuu/plugin-sdk";

export function activate(api: PluginGenerationApi): void {
  if (!api.supportedFeatures?.includes("ui.selected-slot-target.v1")) throw new Error("Missing renderer feature ui.selected-slot-target.v1");
  const React = api.react;
  function Controls({ context }: UISlotRenderProps) {
    const threadId = String(context.threadId ?? "");
    const [value, setValue] = React.useState<GoalControlResultV1>({ goal: null });
    const [error, setError] = React.useState("");
    const [busy, setBusy] = React.useState(false);
    const request = (input: GoalControlRequestV1) => api.invokeService<GoalControlResultV1>({ service: "goal.control", method: "request", params: input });
    React.useEffect(() => {
      let alive = true;
      const refresh = () => { void request({ method: "get_goal", input: { thread_id: threadId } }).then(result => {
        if (alive) { setValue(result); setError(""); }
      }, cause => { if (alive) setError(String(cause)); }); };
      refresh(); const subscription = api.onHostEvent(refresh);
      return () => { alive = false; subscription.dispose(); };
    }, [threadId]);
    return <div data-independent-goal-ui>
      <output>{value.goal?.objective ?? "No goal"}</output>
      {error ? <p role="alert">{error}</p> : null}
      <button disabled={busy || !!error || !value.goal} onClick={() => {
        setBusy(true);
        void request({ method: "pause", input: { thread_id: threadId } }).then(setValue, cause => setError(String(cause))).finally(() => setBusy(false));
      }}>Pause replacement goal</button>
      <span data-independent-status>{value.goal?.status}</span>
    </div>;
  }
  api.registerUIContribution({ id: "independent-goal", target: { service: "ui.composer", slot: "composer.above" }, render: Controls });
}
