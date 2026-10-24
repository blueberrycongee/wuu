import type { PluginGenerationApi, ApplicationServiceV1, ApplicationShellRenderProps } from "./index.js";

// Public authoring failures: host components must be JSX-compatible, event
// targets must be typed, and invalid action payloads must fail compilation.
export function authoringContract(api: PluginGenerationApi): void {
  const React = api.react;
  const { Button, TextInput, TextArea, Select, Checkbox, Section } = api.ui;
  function Shell({ application, slots: { Slot } }: ApplicationShellRenderProps) {
    const snapshot = React.useSyncExternalStore(application.subscribe, application.getSnapshot);
    return <Section title="Research">
      <TextInput label="Title" value={snapshot.conversation.title ?? ""} onChange={(event) => {
        void application.invoke("thread.rename", { threadId: "thread", title: event.currentTarget.value });
      }} />
      <TextArea label="Message" value={snapshot.composer.draftText ?? ""} onChange={(event) => {
        void application.invoke("composer.set-draft", { text: event.currentTarget.value });
      }} />
      <Select label="Theme" value={snapshot.appearance.theme} onChange={(event) => {
        if (event.currentTarget.value === "dark") void application.invoke("appearance.set", { theme: "dark" });
      }}><option value="dark">Dark</option></Select>
      <Checkbox label="Enabled" checked onChange={(event) => { void event.currentTarget.checked; }} />
      <Button onClick={() => { void application.invoke("composer.submit", {}); }}>Send</Button>
      <Slot name="research-workbench/evidence" context={{ threadId: snapshot.conversation.threadId }} />
    </Section>;
  }
  api.registerApplicationShell({ id: "ui.shell", render: Shell });
  api.registerUISlot({ id: "evidence", cardinality: "many" });
  api.registerUIContribution({ id: "summary", target: "research-workbench/evidence", render: ({ application }) =>
    <p>{application.getSnapshot().conversation.title}</p> });
}
export function invalidActionContracts(application: ApplicationServiceV1): void {
  // @ts-expect-error Missing thread identity must not compile.
  void application.invoke("thread.rename", { title: "Research" });
  // @ts-expect-error A shell cannot invent an action outside the public contract.
  void application.invoke("session.private-send", {});
  // @ts-expect-error Invalid theme must not compile.
  void application.invoke("appearance.set", { theme: "opaque" });
  // @ts-expect-error Provider secrets are not part of presentation state.
  void application.getSnapshot().settings.apiKey;
}
