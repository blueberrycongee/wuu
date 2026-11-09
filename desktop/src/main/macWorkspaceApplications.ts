import { execFile as execFileCallback } from "node:child_process";
import { basename } from "node:path";
import { promisify } from "node:util";
import type { MenuItemConstructorOptions, NativeImage } from "electron";

const execFile = promisify(execFileCallback);

const LAUNCH_SERVICES_SCRIPT = String.raw`
ObjC.import("AppKit");

function run(argv) {
  const fileURL = $.NSURL.fileURLWithPath(argv[0]);
  const workspace = $.NSWorkspace.sharedWorkspace;
  const defaultURL = workspace.URLForApplicationToOpenURL(fileURL);
  const applicationURLs = ObjC.unwrap(workspace.URLsForApplicationsToOpenURL(fileURL));

  function iconPNG(path) {
    try {
      const rect = $.NSMakeRect(0, 0, 16, 16);
      const source = workspace.iconForFile(path);
      const target = $.NSImage.alloc.initWithSize(rect.size);
      target.lockFocus;
      source.drawInRectFromRectOperationFraction(
        rect,
        $.NSZeroRect,
        $.NSCompositingOperationCopy,
        1,
      );
      const representation = $.NSBitmapImageRep.alloc.initWithFocusedViewRect(rect);
      target.unlockFocus;
      const data = representation.representationUsingTypeProperties(
        $.NSBitmapImageFileTypePNG,
        $(),
      );
      return ObjC.unwrap(data.base64EncodedStringWithOptions(0));
    } catch (_error) {
      return null;
    }
  }

  function describe(applicationURL) {
    const bundle = $.NSBundle.bundleWithURL(applicationURL);
    const displayName = bundle.objectForInfoDictionaryKey("CFBundleDisplayName")
      || bundle.objectForInfoDictionaryKey("CFBundleName")
      || applicationURL.deletingPathExtension.lastPathComponent;
    return {
      path: ObjC.unwrap(applicationURL.path),
      name: displayName ? ObjC.unwrap(displayName) : null,
      bundle_id: bundle.bundleIdentifier ? ObjC.unwrap(bundle.bundleIdentifier) : null,
      icon_png: iconPNG(ObjC.unwrap(applicationURL.path)),
    };
  }

  return JSON.stringify({
    default_path: defaultURL ? ObjC.unwrap(defaultURL.path) : null,
    applications: applicationURLs.map(describe),
  });
}`;

export type MacWorkspaceApplication = {
  path: string;
  name: string;
  bundleId?: string;
  iconPng?: string;
};

export type MacWorkspaceApplicationAssociations = {
  defaultApplication?: MacWorkspaceApplication;
  applications: MacWorkspaceApplication[];
};

export type MacWorkspaceItemMenuLabels = {
  open: string;
  openInApplication: (application: string) => string;
  openWith: string;
  copyPath: string;
};

export function macWorkspaceItemMenuTemplate({
  associations,
  icons,
  labels,
  onOpenDefault,
  onOpenWith,
  onCopyPath,
}: {
  associations: MacWorkspaceApplicationAssociations;
  icons: ReadonlyMap<string, NativeImage | undefined>;
  labels: MacWorkspaceItemMenuLabels;
  onOpenDefault: () => void;
  onOpenWith: (application: MacWorkspaceApplication) => void;
  onCopyPath: () => void;
}): MenuItemConstructorOptions[] {
  const defaultApplication = associations.defaultApplication;
  return [
    {
      label: defaultApplication
        ? labels.openInApplication(defaultApplication.name)
        : labels.open,
      icon: defaultApplication ? icons.get(defaultApplication.path) : undefined,
      click: onOpenDefault,
    },
    {
      label: labels.openWith,
      enabled: associations.applications.length > 0,
      submenu: associations.applications.map((application) => ({
        label: application.name,
        icon: icons.get(application.path),
        click: () => onOpenWith(application),
      })),
    },
    { type: "separator" },
    { label: labels.copyPath, click: onCopyPath },
  ];
}

type RawApplication = {
  path?: unknown;
  name?: unknown;
  bundle_id?: unknown;
  icon_png?: unknown;
};

type RawAssociations = {
  default_path?: unknown;
  applications?: unknown;
};

export async function macWorkspaceApplications(
  filePath: string,
  run: typeof execFile = execFile,
): Promise<MacWorkspaceApplicationAssociations> {
  const { stdout } = await run("/usr/bin/osascript", [
    "-l",
    "JavaScript",
    "-e",
    LAUNCH_SERVICES_SCRIPT,
    "--",
    filePath,
  ], { encoding: "utf8", timeout: 1000, maxBuffer: 1024 * 1024 });
  return normalizeMacWorkspaceApplications(JSON.parse(stdout) as RawAssociations);
}

export async function openMacWorkspaceItemWithApplication(
  filePath: string,
  applicationPath: string,
  run: typeof execFile = execFile,
): Promise<void> {
  await run("/usr/bin/open", ["-a", applicationPath, "--", filePath]);
}

export function normalizeMacWorkspaceApplications(
  raw: RawAssociations,
): MacWorkspaceApplicationAssociations {
  const rawApplications = Array.isArray(raw.applications)
    ? raw.applications as RawApplication[]
    : [];
  const defaultPath = typeof raw.default_path === "string" ? raw.default_path : undefined;
  const candidates = rawApplications
    .map(normalizeApplication)
    .filter((application): application is MacWorkspaceApplication => application !== undefined);

  const defaultApplication = defaultPath
    ? candidates.find((application) => application.path === defaultPath)
      ?? normalizeApplication({ path: defaultPath })
    : undefined;
  const applications: MacWorkspaceApplication[] = [];
  const seen = new Set<string>();
  for (const application of candidates) {
    if (isGeneratedApplication(application.path)) continue;
    const key = application.bundleId?.toLowerCase() ?? application.path.toLowerCase();
    if (seen.has(key)) continue;
    seen.add(key);
    applications.push(application);
  }

  return { defaultApplication, applications };
}

function normalizeApplication(raw: RawApplication): MacWorkspaceApplication | undefined {
  if (typeof raw.path !== "string" || !raw.path.endsWith(".app")) return undefined;
  const fallbackName = basename(raw.path, ".app");
  return {
    path: raw.path,
    name: typeof raw.name === "string" && raw.name.trim() ? raw.name.trim() : fallbackName,
    bundleId: typeof raw.bundle_id === "string" && raw.bundle_id.trim()
      ? raw.bundle_id.trim()
      : undefined,
    iconPng: typeof raw.icon_png === "string" && raw.icon_png.trim()
      ? raw.icon_png
      : undefined,
  };
}

function isGeneratedApplication(path: string): boolean {
  return path.includes("/Library/Caches/")
    || path.includes("/node_modules/")
    || path.includes("/.cache/")
    || path.includes("/.Trash/")
    || path.includes("/AppTranslocation/")
    || path.includes("/private/var/folders/")
    || path.includes(".app/Contents/");
}
