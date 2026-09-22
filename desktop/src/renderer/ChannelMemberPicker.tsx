import { Check, Search } from "./WuuIcons";
import { useMemo, useRef, useState, type KeyboardEvent } from "react";
import type { NamedAgent } from "../shared/protocol";
import { AgentAvatarMark } from "./AgentAvatarMark";
import { useI18n } from "./i18n";

function normalizeSearchText(value: string): string {
  return value.normalize("NFKD").replace(/\p{M}/gu, "").toLocaleLowerCase();
}

export function ChannelMemberPicker({
  agents,
  selectedAgentIDs,
  onToggle,
  label,
  maxSelected,
}: {
  agents: NamedAgent[];
  selectedAgentIDs: string[];
  onToggle: (agentID: string) => void;
  label?: string;
  maxSelected?: number;
}): JSX.Element {
  const { t } = useI18n();
  const [query, setQuery] = useState("");
  const optionRefs = useRef<Array<HTMLButtonElement | null>>([]);
  const normalizedQuery = normalizeSearchText(query.trim());
  const visibleAgents = useMemo(() => {
    if (!normalizedQuery) return agents;
    return agents.filter((agent) => normalizeSearchText([
      agent.name,
      agent.model_override,
      agent.provider_override,
    ].filter(Boolean).join(" ")).includes(normalizedQuery));
  }, [agents, normalizedQuery]);

  function focusOption(index: number): void {
    const resolvedIndex = Math.max(0, Math.min(index, visibleAgents.length - 1));
    optionRefs.current[resolvedIndex]?.focus();
  }

  function handleOptionKeyDown(event: KeyboardEvent<HTMLButtonElement>, index: number): void {
    if (event.key === "ArrowDown") {
      event.preventDefault();
      focusOption(index + 1);
    } else if (event.key === "ArrowUp") {
      event.preventDefault();
      focusOption(index - 1);
    } else if (event.key === "Home") {
      event.preventDefault();
      focusOption(0);
    } else if (event.key === "End") {
      event.preventDefault();
      focusOption(visibleAgents.length - 1);
    }
  }

  return (
    <section className="channel-member-picker" aria-labelledby="channel-member-picker-label">
      <div className="channel-member-picker-heading">
        <span id="channel-member-picker-label">{label ?? t("channels.groupMembers")}</span>
        <span>{maxSelected == null
          ? t("channels.selectedMembers", { count: selectedAgentIDs.length })
          : t("channels.selectedMembersWithLimit", { count: selectedAgentIDs.length, max: maxSelected })}</span>
      </div>
      <div className="channel-member-picker-control">
        <label className="channel-member-picker-search">
          <Search className="icon" aria-hidden="true" />
          <input
            type="search"
            value={query}
            placeholder={t("channels.searchAgents")}
            aria-label={t("channels.searchAgents")}
            onChange={(event) => setQuery(event.currentTarget.value)}
            onKeyDown={(event) => {
              if (event.key === "ArrowDown" && visibleAgents.length > 0) {
                event.preventDefault();
                focusOption(0);
              }
            }}
          />
        </label>
        <div
          className="channel-member-picker-options"
          role="listbox"
          aria-labelledby="channel-member-picker-label"
          aria-multiselectable="true"
        >
          {visibleAgents.length === 0 ? (
            <div className="channel-member-picker-empty">{t(query ? "channels.noMatchingAgents" : "channels.noAgents")}</div>
          ) : visibleAgents.map((agent, index) => {
            const selected = selectedAgentIDs.includes(agent.id);
            const disabled = !selected && maxSelected != null && selectedAgentIDs.length >= maxSelected;
            return (
              <button
                className="channel-member-picker-option"
                type="button"
                role="option"
                aria-selected={selected}
                disabled={disabled}
                key={agent.id}
                ref={(node) => { optionRefs.current[index] = node; }}
                onClick={() => onToggle(agent.id)}
                onKeyDown={(event) => handleOptionKeyDown(event, index)}
              >
                <span className="channel-member-picker-avatar" aria-hidden="true">
                  <AgentAvatarMark seed={agent.id} avatarKey={agent.avatar_key} avatarImage={agent.avatar_image} />
                </span>
                <span className="channel-member-picker-identity">
                  <strong>{agent.name}</strong>
                  <small>{agent.model_override || t("channels.inheritModel")}</small>
                </span>
                {selected ? <Check className="channel-member-picker-check icon" aria-hidden="true" /> : null}
              </button>
            );
          })}
        </div>
      </div>
    </section>
  );
}
