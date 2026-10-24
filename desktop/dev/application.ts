import type { ApplicationServiceV1 } from "../../packages/plugin-sdk/src/ui";
import { createDraftSessionTab, initialState } from "../src/renderer/AppState";
import { ApplicationController } from "../src/renderer/plugins/ApplicationController";

/** Synthetic draft owner for isolated UI fixtures; never starts a runtime connection. */
export function createFixtureApplication(): ApplicationServiceV1 {
  const context = { kind: "no_project" as const, cwd: "/tmp/wuu-ui-fixture" };
  return new ApplicationController({ ...initialState, activeContext: context,
    activeSessionTabID: "fixture", sessionTabs: [createDraftSessionTab("fixture", context)] });
}
