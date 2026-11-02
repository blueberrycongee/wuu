import type { CSSProperties, ReactNode } from "react";
import type { ComposerSlashCommand } from "./ComposerSlashCommands";
import { SlashCommandIcon } from "./ComposerRuntimeMenus";
import { Tooltip } from "./Tooltip";
import { useI18n } from "./i18n";

export function ComposerSlashMenu({ id, width, commands, selectedIndex, onSelectIndex, onSelect, query, status }: {
  id: string; width: number; commands: ComposerSlashCommand[]; selectedIndex: number;
  onSelectIndex(index: number): void; onSelect(command: ComposerSlashCommand): void; query: string; status?: ReactNode;
}): JSX.Element {
  const { t } = useI18n();
  return (
    <div
      className="composer-context-menu composer-plus-menu slash-command-menu"
      id={id}
      role="listbox"
      aria-label={t("composer.slashCommands")}
      style={{ "--composer-menu-width": `${width}px` } as CSSProperties}
    >
      {status}
      {commands.length > 0 ? (
        <div className="slash-command-list scrollbar-hidden">
          {commands.map((command, index) => {
            const selected = index === selectedIndex;
            const optionID = `${id}-${command.id}`;
            return (
              <Tooltip
                content={
                  command.disabledReason || command.kind === "skill"
                    ? undefined
                    : command.description
                }
                key={command.id}
              >
                <button
                  className={`slash-command-item${selected ? " selected" : ""}`}
                  data-command-name={command.name}
                  id={optionID}
                  role="option"
                  type="button"
                  aria-selected={selected}
                  disabled={Boolean(command.disabledReason)}
                  onMouseEnter={() => {
                    if (!command.disabledReason) {
                      onSelectIndex(index);
                    }
                  }}
                  onMouseDown={(event) => event.preventDefault()}
                  onClick={() => onSelect(command)}
                >
                  <SlashCommandIcon command={command} />
                  <span className="composer-plus-menu-item-title slash-command-name">
                    /{command.name}
                  </span>
                  {command.disabledReason || command.title ? (
                    <span
                      className={`composer-plus-menu-item-desc ${
                        command.disabledReason ? "slash-command-meta" : "slash-command-summary"
                      }`}
                    >
                      {command.disabledReason ?? command.title}
                    </span>
                  ) : null}
                </button>
              </Tooltip>
            );
          })}
        </div>
      ) : status ? null : (
        <div className="slash-command-empty">{t("composer.noMatchingCommand", { query: query })}</div>
      )}
    </div>
  );
}
