import type { EngineInfo } from "../shared/protocol";

export function engineLabel(id: string, info?: EngineInfo): string {
  return info?.display_name || ({ wuu: "Wuu", codex: "Codex", claude: "Claude Code" } as Record<string, string>)[id] || id;
}
