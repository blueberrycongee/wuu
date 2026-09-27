import type { SVGProps } from "react";
import { isPublicIconName } from "../shared/themeContract.generated";

import { PUBLIC_ICON_COMPONENTS } from "./PublicIcon";
import {
  Archive, Blocks, Bookmark, Check, Clock, Code2, FileText, Globe, Hammer,
  ListTodo, MessageSquare, Moon, Network, Pencil, Presentation, Search,
  Settings2, Sparkles, type IconComponent,
} from "./WuuIcons";

const marks = {
  module: Blocks, create: Pencil, inspect: Search, verify: Check, build: Hammer,
  branch: Network, memory: Bookmark, condense: FileText, cycle: Clock, rest: Moon,
  appearance: Sparkles, dialogue: MessageSquare, tasks: ListTodo, browser: Globe,
  code: Code2, presentation: Presentation, archive: Archive, settings: Settings2,
} satisfies Record<string, IconComponent>;

type Motif = keyof typeof marks;

// Skills have no icon descriptor. Prefer capability words over arbitrary hashes;
// unknown skills receive the shared module mark rather than an invented meaning.
export function skillCapability(name: string): Motif {
  const words = name.toLowerCase().split(/[^a-z0-9]+/);
  const groups: [Motif, string[]][] = [
    ["inspect", ["debug", "diagnosis", "diagnose"]],
    ["verify", ["review", "check", "test", "audit"]],
    ["create", ["creator", "create"]],
    ["build", ["build", "install", "deploy", "release"]],
    ["browser", ["browser", "web"]],
    ["presentation", ["pptx", "presentation", "slides"]],
    ["branch", ["commit", "git", "delegate"]],
    ["memory", ["memory", "remember"]],
  ];
  return groups.find(([, keywords]) => keywords.some((word) => words.includes(word)))?.[0] ?? "module";
}

export function CapabilityMark({ motif, name, ...props }: SVGProps<SVGSVGElement> & {
  motif?: Motif;
  name?: string;
}): JSX.Element {
  const Icon = motif ? marks[motif]
    : name && isPublicIconName(name) ? PUBLIC_ICON_COMPONENTS[name] : Blocks;
  return <Icon data-icon={name ?? motif ?? "module"} {...props} />;
}
