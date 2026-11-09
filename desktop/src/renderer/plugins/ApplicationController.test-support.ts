import * as React from "react";
import { afterEach } from "vitest";
import { ApplicationController as Implementation } from "./ApplicationController";
import { PluginHost } from "./PluginHost";
import { WorkbenchController } from "./Workbench";
import type { AppState } from "../AppState";

const resources = new Set<WorkbenchController>();
afterEach(() => { for (const workbench of resources) workbench.dispose(); resources.clear(); desktopApplication = undefined; });

/** Explicit isolated dependencies for controller behavior tests; never a production fallback. */
export class ApplicationController extends Implementation {
  constructor(state?: AppState) {
    let application!: Implementation;
    const bridge = new Proxy({} as import("./HostApplicationService").HostApplicationService, { has: (_target, key) => Reflect.has(application, key), get: (_target, key) => {
      const value = Reflect.get(application, key, application);
      return typeof value === "function" ? value.bind(application) : value;
    } });
    const host = new PluginHost({ react: React, application: bridge });
    const workbench = new WorkbenchController(host);
    super(state, { contributions: host, workbench });
    application = this;
    resources.add(workbench);
  }
}

let desktopApplication: ApplicationController | undefined;
export function getDesktopApplication(): ApplicationController { return desktopApplication ??= new ApplicationController(); }
