import * as React from "react";
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
      <Button onClick={() => { void application.invoke("composer.submit", { ownerTabId: snapshot.tabs.activeTabId }); }}>Send</Button>
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

// Two independent UI authors use presentation registrations without an ambient
// application writer. React components receive the current scoped service.
export const researchPresentation: import("./ui.js").ApplicationPresentationModule = {
  activatePresentation(api) {
    api.registerUISlot({ id: "notes", cardinality: "many" });
    api.registerApplicationShell({ id: "ui.shell", render: ({ application, slots }) =>
      <main><h1>{application.getSnapshot().conversation.title}</h1><slots.Slot name={`${api.manifestId}/notes`} /></main> });
  },
};
export const compactPresentation: import("./ui.js").ApplicationPresentationModule = {
  activatePresentation(api) {
    api.registerUIContribution({ id: "ui.navigation", target: "independent-shell/navigation", render: ({ application }) =>
      <button onClick={() => { void application.invoke("draft.start", {}); }}>New conversation</button> });
  },
};
export function presentationHasNoBusinessAuthority(api: import("./ui.js").ApplicationPresentationAPI): void {
  // @ts-expect-error Registration cannot capture a business scope across rebinds.
  void api.application;
  // @ts-expect-error Presentation registrations cannot install a writer.
  api.registerApplicationProvider({});
  // @ts-expect-error Runtime service calls require a graph-owned activation.
  api.invokeRuntime("mutate");
}
