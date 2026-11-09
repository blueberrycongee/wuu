import type { CSSProperties, ReactNode } from "react";
import type { ComposerSlashCommand } from "./ComposerSlashCommands";
import { ComposerCommandRow } from "./ComposerRuntimeMenus";
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
            return (
              <ComposerCommandRow
                command={command}
                id={`${id}-${command.id}`}
                key={command.id}
                role="option"
                className={`slash-command-item${selected ? " selected" : ""}`}
                selected={selected}
                onHover={() => onSelectIndex(index)}
                onSelect={onSelect}
              />
            );
          })}
        </div>
      ) : status ? null : (
        <div className="slash-command-empty">{t("composer.noMatchingCommand", { query: query })}</div>
      )}
    </div>
  );
}
