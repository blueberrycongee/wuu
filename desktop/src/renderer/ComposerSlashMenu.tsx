import type { CSSProperties, ReactNode } from "react";
import type { ComposerSlashCommand } from "./ComposerSlashCommands";
import { SlashCommandIcon } from "./ComposerRuntimeMenus";
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
        <div className="slash-command-list composer-menu-scroll scrollbar-hidden" data-scroll-fade="compact">
          {commands.map((command, index) => {
            const selected = index === selectedIndex;
            const optionID = `${id}-${command.id}`;
            return (
              <button
                className={`slash-command-item${selected ? " selected" : ""}`}
                data-command-name={command.name}
                id={optionID}
                key={command.id}
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
                <span className="composer-plus-menu-item-title slash-command-title">
                  {command.kind === "skill" ? command.name : command.title}
                </span>
                {command.kind === "skill" ? (
                  <span className={`composer-plus-menu-item-desc ${command.disabledReason ? "slash-command-meta" : "slash-command-summary"}`}>
                    {command.disabledReason ?? command.description}
                  </span>
                ) : (
                  <span
                    className={`composer-plus-menu-item-desc ${
                      command.disabledReason ? "slash-command-meta" : "slash-command-command"
                    }`}
                  >
                    {command.disabledReason ?? `/${command.name}`}
                  </span>
                )}
              </button>
            );
          })}
        </div>
      ) : status ? null : (
        <div className="slash-command-empty">{t("composer.noMatchingCommand", { query: query })}</div>
      )}
    </div>
  );
}
