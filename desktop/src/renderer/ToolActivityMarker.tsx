import type { JSX } from "react";
import {
  BookOpen, FilePlus, FileText, FolderOpen, Globe, Layers, ListTodo,
  MessageCircle, NotebookPen, Pencil, Search, Terminal, Users,
  type LucideIcon,
} from "lucide-react";
import type { ToolActivityKind } from "./ToolActivityHelpers";

const ACTIVITY_ICONS: Record<ToolActivityKind, LucideIcon> = {
  read: FileText,
  list: FolderOpen,
  search: Search,
  command: Terminal,
  edit: Pencil,
  create: FilePlus,
  agent: Users,
  todo: ListTodo,
  interaction: MessageCircle,
  browser: Globe,
  skill: BookOpen,
  context: NotebookPen,
  unknown: Layers,
};

export function ToolActivityMarker({
  kind = "unknown",
  running = false,
}: {
  kind?: ToolActivityKind;
  running?: boolean;
}): JSX.Element {
  const Icon = ACTIVITY_ICONS[kind];
  const stateClass = running ? "is-running" : "is-settled";
  return (
    <Icon
      className={`tool-activity-marker ${stateClass}`}
      size={14}
      strokeWidth={1.6}
      aria-hidden
      focusable="false"
    />
  );
}
