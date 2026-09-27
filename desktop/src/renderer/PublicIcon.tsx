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
import { PluginBlocksIcon } from "./PluginBlocksIcon";
import { useEffect, useState } from "react";

export const PUBLIC_ICON_COMPONENTS = {
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
} as const satisfies Readonly<Record<PublicIconName, IconComponent>>;

export function PublicIcon({
  name,
  className,
}: {
  name?: string;
  className?: string;
}): JSX.Element {
  if (!name || !isPublicIconName(name)) {
    return <PluginBlocksIcon className={className} />;
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
}: {
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
}: {
  icon: Exclude<ExtensionIconDescriptor, { name: string }>;
  pluginId: string;
  fingerprint: string;
  className?: string;
}): JSX.Element {
  const paths: string[] = "path" in icon && icon.path
    ? [icon.path]
    : [icon.light as string, icon.dark as string];
  const [urls, setURLs] = useState<readonly string[]>([]);

  useEffect(() => {
    let active = true;
    Promise.all(paths.map((path) => loadPluginIconURL(pluginId, fingerprint, path)))
      .then((loaded) => {
        if (active) setURLs(loaded);
      })
      .catch(() => {
        if (active) setURLs([]);
      });
    return () => { active = false; };
  }, [pluginId, fingerprint, paths.join("\u0000")]);

  if (urls.length !== paths.length) {
    return <PublicIcon className={className} />;
  }
  if (urls.length === 1) {
    return <img className={`plugin-icon-asset ${className ?? ""}`} src={urls[0]} alt="" />;
  }
  return (
    <span className={`plugin-icon-themed ${className ?? ""}`} aria-hidden="true">
      <img className="plugin-icon-asset plugin-icon-asset-light" src={urls[0]} alt="" />
      <img className="plugin-icon-asset plugin-icon-asset-dark" src={urls[1]} alt="" />
    </span>
  );
}

function loadPluginIconURL(pluginId: string, fingerprint: string, path: string): Promise<string> {
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
