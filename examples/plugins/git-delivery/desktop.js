export function activate(api) {
  api.registerCommand({
    id: "open-pr",
    title: "GitHub draft PR",
    contexts: ["work-candidate.publish"],
    execute(input) { return api.invokeRuntime("publish", input); },
  });
}
