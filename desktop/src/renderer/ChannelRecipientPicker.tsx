import { MessagesSquare, Plus, X } from "lucide-react";
import { useEffect, useMemo, useRef, useState, type KeyboardEvent } from "react";
import type { NamedAgent } from "../shared/protocol";
import { AgentAvatarMark } from "./AgentAvatarMark";
import { useI18n } from "./i18n";

function normalizeSearchText(value: string): string {
  return value.normalize("NFKD").replace(/\p{M}/gu, "").toLocaleLowerCase();
}

export function ChannelRecipientPicker({
  agents,
  selectedAgentIDs,
  onToggle,
  maxSelected,
  onCancel,
  onCreateAgent,
  onCreateGroup,
  disabled = false,
}: {
  agents: NamedAgent[];
  selectedAgentIDs: string[];
  onToggle: (agentID: string) => void;
  maxSelected: number;
  onCancel: () => void;
  onCreateAgent?: () => void;
  onCreateGroup?: () => void;
  disabled?: boolean;
}): JSX.Element {
  const { t } = useI18n();
  const [query, setQuery] = useState("");
  const [activeIndex, setActiveIndex] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const selectedAgents = useMemo(() => selectedAgentIDs
    .map((id) => agents.find((agent) => agent.id === id))
    .filter((agent): agent is NamedAgent => Boolean(agent)), [agents, selectedAgentIDs]);
  const availableAgents = useMemo(() => {
    if (selectedAgentIDs.length >= maxSelected) return [];
    const selected = new Set(selectedAgentIDs);
    const normalizedQuery = normalizeSearchText(query.trim());
    return agents.filter((agent) => !selected.has(agent.id) && (
      !normalizedQuery || normalizeSearchText([
        agent.name,
        agent.role,
        agent.model_override,
        agent.provider_override,
      ].filter(Boolean).join(" ")).includes(normalizedQuery)
    ));
  }, [agents, maxSelected, query, selectedAgentIDs]);
  const actions = [
    ...(onCreateAgent ? [{ id: "create-agent", label: t("channels.newAgent"), run: onCreateAgent, icon: Plus }] : []),
    ...(onCreateGroup ? [{ id: "create-group", label: t("channels.newGroup"), run: onCreateGroup, icon: MessagesSquare }] : []),
  ];
  const optionCount = actions.length + availableAgents.length;
  const activeID = actions[activeIndex]?.id ?? availableAgents[activeIndex - actions.length]?.id;
  const optionsVisible = optionCount > 0 || Boolean(query.trim())
    || (selectedAgentIDs.length >= maxSelected && agents.length > selectedAgentIDs.length);

  function chooseOption(index: number): void {
    if (disabled) return;
    if (actions[index]) {
      actions[index].run();
      setQuery("");
      setActiveIndex(0);
    }
    else if (availableAgents[index - actions.length]) choose(availableAgents[index - actions.length].id);
  }

  useEffect(() => {
    inputRef.current?.focus();
  }, []);
  useEffect(() => {
    setActiveIndex((current) => Math.min(current, Math.max(0, optionCount - 1)));
  }, [optionCount]);

  const choose = (agentID: string): void => {
    if (disabled || selectedAgentIDs.length >= maxSelected) return;
    onToggle(agentID);
    setQuery("");
    setActiveIndex(0);
    inputRef.current?.focus();
  };

  function handleKeyDown(event: KeyboardEvent<HTMLInputElement>): void {
    if ((event.metaKey || event.ctrlKey) && /^Digit[1-9]$/u.test(event.code)) {
      const index = Number(event.code.slice(-1)) - 1;
      if (index < optionCount) {
        event.preventDefault();
        chooseOption(index);
      }
      return;
    }
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      if (optionCount === 0) return;
      event.preventDefault();
      const direction = event.key === "ArrowDown" ? 1 : -1;
      setActiveIndex((current) => (current + direction + optionCount) % optionCount);
    } else if (event.key === "Enter" && activeID) {
      event.preventDefault();
      chooseOption(activeIndex);
    } else if (event.key === "Backspace" && !query && selectedAgentIDs.length > 0) {
      onToggle(selectedAgentIDs[selectedAgentIDs.length - 1]);
    } else if (event.key === "Escape") {
      event.preventDefault();
      if (query) setQuery("");
      else onCancel();
    }
  }

  return (
    <div className="channel-recipient-picker">
      <span className="channel-recipient-label">{t(onCreateAgent ? "channels.chatRecipient" : "channels.recipients")}</span>
      <div className="channel-recipient-field">
        <div className="channel-recipient-control" onClick={() => inputRef.current?.focus()}>
          {selectedAgents.map((agent) => (
            <span className="channel-recipient-chip" key={agent.id}>
              <span aria-hidden="true"><AgentAvatarMark seed={agent.id} avatarKey={agent.avatar_key} avatarImage={agent.avatar_image} /></span>
              <span>{agent.name}</span>
              <button type="button" aria-label={t("channels.removeRecipient", { name: agent.name })} onClick={(event) => {
                event.stopPropagation();
                onToggle(agent.id);
              }} disabled={disabled}><X aria-hidden="true" /></button>
            </span>
          ))}
          <input
            ref={inputRef}
            role="combobox"
            aria-expanded={optionsVisible}
            aria-controls={optionsVisible ? "channel-recipient-options" : undefined}
            aria-activedescendant={activeID ? `channel-recipient-${activeID}` : undefined}
            value={query}
            disabled={disabled}
            aria-label={t(onCreateAgent ? "channels.searchOrCreateAgent" : "channels.searchRecipients")}
            placeholder={selectedAgentIDs.length === 0 ? t(onCreateAgent ? "channels.searchOrCreateAgent" : "channels.searchRecipients") : ""}
            onChange={(event) => { setQuery(event.currentTarget.value); setActiveIndex(0); }}
            onKeyDown={handleKeyDown}
          />
        </div>
        {optionsVisible ? <div id="channel-recipient-options" className="channel-recipient-options" role="listbox" aria-label={t("channels.availableAgents")}>
          {actions.map((action, index) => <button id={`channel-recipient-${action.id}`} className={index === activeIndex ? "active" : ""} type="button" role="option" aria-selected="false" disabled={disabled} key={action.id} onMouseEnter={() => setActiveIndex(index)} onClick={() => chooseOption(index)}>
            <span className="channel-recipient-option-avatar channel-recipient-action-icon" aria-hidden="true"><action.icon size={18} /></span>
            <span className="channel-recipient-option-name">{action.label}</span>
            <kbd aria-hidden="true"><span>{navigator.platform.includes("Mac") ? "⌘" : "Ctrl"}</span>{index + 1}</kbd>
          </button>)}
          {availableAgents.length > 0 ? availableAgents.map((agent, index) => (
            <button
              id={`channel-recipient-${agent.id}`}
              className={index + actions.length === activeIndex ? "active" : ""}
              type="button"
              role="option"
              aria-selected="false"
              disabled={disabled}
              key={agent.id}
              onMouseEnter={() => setActiveIndex(index + actions.length)}
              onClick={() => choose(agent.id)}
            >
              <span className="channel-recipient-option-avatar" aria-hidden="true">
                <AgentAvatarMark seed={agent.id} avatarKey={agent.avatar_key} avatarImage={agent.avatar_image} />
              </span>
              <span className="channel-recipient-option-name">{agent.name}</span>
              {index + actions.length < 9 ? <kbd aria-hidden="true"><span>{navigator.platform.includes("Mac") ? "⌘" : "Ctrl"}</span>{index + actions.length + 1}</kbd> : null}
            </button>
          )) : optionCount === 0 ? (
            <div className="channel-recipient-empty">{t(query ? "channels.noMatchingAgents" : selectedAgentIDs.length >= maxSelected ? "channels.recipientLimitReached" : "channels.noAgents")}</div>
          ) : null}
        </div> : null}
      </div>
    </div>
  );
}
