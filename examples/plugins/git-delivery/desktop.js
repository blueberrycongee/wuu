export function activate(api) {
  api.registerCommand({
    id: "open-pr",
    title: "GitHub draft PR",
    contexts: ["project-candidate.publish"],
    execute(input) { return api.invokeRuntime("publish", input); },
  });
}
