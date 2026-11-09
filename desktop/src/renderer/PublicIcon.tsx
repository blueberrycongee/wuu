import {
  Activity,
  Archive,
  Bot,
  Brain,
  CalendarDays,
  Check,
  CircleCheck,
  Clock,
  Code2,
  Database,
  FileText,
  Folder,
  Gauge,
  Globe2,
  Inbox,
  LayoutGrid,
  ListTodo,
  MessageSquare,
  Moon,
  Plug,
  Search,
  Settings,
  Shield,
  SlidersHorizontal,
  Sparkles,
  Terminal,
  Users,
  Workflow,
  Wrench,
  type IconComponent,
} from "./WuuIcons";
import { isPublicIconName, type PublicIconName } from "../shared/themeContract.generated";
import type { ExtensionIconDescriptor } from "../shared/protocol";
import { DefaultExtensionIcon } from "./DefaultExtensionIcon";
import { MODULE_IDENTITY_ICONS } from "./ModuleIdentityIcons";
import { PluginAvatar } from "./PluginAvatar";
import { useEffect, useRef, useState } from "react";

export const PUBLIC_ICON_COMPONENTS = {
  ...MODULE_IDENTITY_ICONS,
  archive: Archive,
  bot: Bot,
  brain: Brain,
  calendar: CalendarDays,
  check: Check,
  "check-circle": CircleCheck,
  clock: Clock,
  code: Code2,
  database: Database,
  "file-text": FileText,
  folder: Folder,
  gauge: Gauge,
  globe: Globe2,
  inbox: Inbox,
  "layout-grid": LayoutGrid,
  "list-todo": ListTodo,
  mascot: PluginAvatar,
  "message-square": MessageSquare,
  moon: Moon,
  plug: Plug,
  pulse: Activity,
  search: Search,
  settings: Settings,
  shield: Shield,
  sliders: SlidersHorizontal,
  sparkles: Sparkles,
  terminal: Terminal,
  users: Users,
  workflow: Workflow,
  wrench: Wrench,
} as const satisfies Readonly<Record<PublicIconName, IconComponent | typeof PluginAvatar>>;

export function PublicIcon({
  name,
  className,
}: {
  name?: string;
  className?: string;
}): JSX.Element {
  if (!name || !isPublicIconName(name)) {
    return <DefaultExtensionIcon className={className} />;
  }
  const Icon = PUBLIC_ICON_COMPONENTS[name];
  return <Icon className={className} data-icon={name} />;
}

const pluginIconURLs = new Map<string, Promise<string>>();

export function PluginIcon({
  icon,
  pluginId,
  fingerprint,
  className,
  loadIcon = loadPluginIconURL,
}: {
  loadIcon?: typeof loadPluginIconURL;
  icon?: ExtensionIconDescriptor;
  pluginId: string;
  fingerprint: string;
  className?: string;
}): JSX.Element {
  if (!icon || "name" in icon) {
    return <PublicIcon name={icon?.name} className={className} />;
  }
  return (
    <PluginAssetIcon
      loadIcon={loadIcon}
      icon={icon}
      pluginId={pluginId}
      fingerprint={fingerprint}
      className={className}
    />
  );
}

function PluginAssetIcon({
  icon,
  pluginId,
  fingerprint,
  className,
  loadIcon = loadPluginIconURL,
}: {
  loadIcon?: typeof loadPluginIconURL;
  icon: Exclude<ExtensionIconDescriptor, { name: string }>;
  pluginId: string;
  fingerprint: string;
  className?: string;
}): JSX.Element {
  const paths: string[] = "path" in icon && icon.path
    ? [icon.path]
    : [icon.light as string, icon.dark as string];
  const pathsKey = paths.join("\u0000");
  const [urls, setURLs] = useState<readonly string[]>([]);
  const [cycle, setCycle] = useState(0);
  const reloads = useRef(2);

  useEffect(() => { reloads.current = 2; }, [pluginId, fingerprint, pathsKey]);
  useEffect(() => {
    let active = true;
    Promise.all(paths.map((path) => loadIcon(pluginId, fingerprint, path)))
      .then((loaded) => {
        if (active) setURLs(loaded);
      })
      .catch(() => {
        if (active) setURLs([]);
      });
    return () => { active = false; };
  }, [pluginId, fingerprint, pathsKey, loadIcon, cycle]);

  // A cached URL outlives the bounded main-process asset cache; a failed <img>
  // drops the dead entries and refetches so the asset is recached.
  const reload = () => {
    if (reloads.current <= 0) { setURLs([]); return; }
    reloads.current -= 1;
    for (const path of paths) pluginIconURLs.delete(`${pluginId}\u0000${fingerprint}\u0000${path}`);
    setURLs([]);
    setCycle(count => count + 1);
  };

  if (urls.length !== paths.length) {
    return <PublicIcon className={className} />;
  }
  if (urls.length === 1) {
    return <img key={cycle} className={`plugin-icon-asset ${className ?? ""}`} src={urls[0]} alt="" onError={reload} />;
  }
  return (
    <span className={`plugin-icon-themed ${className ?? ""}`} aria-hidden="true">
      <img key={`${cycle}:light`} className="plugin-icon-asset plugin-icon-asset-light" src={urls[0]} alt="" onError={reload} />
      <img key={`${cycle}:dark`} className="plugin-icon-asset plugin-icon-asset-dark" src={urls[1]} alt="" onError={reload} />
    </span>
  );
}

export function loadPluginIconURL(pluginId: string, fingerprint: string, path: string): Promise<string> {
  const key = `${pluginId}\u0000${fingerprint}\u0000${path}`;
  const existing = pluginIconURLs.get(key);
  if (existing) return existing;
  const loaded = window.wuu?.loadPluginIcon?.({ id: pluginId, fingerprint, path })
    .then((result) => {
      if (result.id !== pluginId || result.fingerprint !== fingerprint || result.path !== path) {
        throw new Error("Plugin icon identity mismatch");
      }
      return result.url;
    }) ?? Promise.reject(new Error("Plugin icon loading is unavailable"));
  pluginIconURLs.set(key, loaded);
  loaded.catch(() => pluginIconURLs.delete(key));
  return loaded;
}
