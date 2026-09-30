/**
 * QueryHistoryPopover — the list of past queries the query-history rail
 * opens. The rail owns focus, the active option and dismissal; this list only
 * renders the options. Pointer presses keep focus where it was, so the rail
 * stays a select-only combobox for assistive technology.
 */
import { useEffect, type JSX } from "react";
import { useI18n } from "./i18n";

export type QueryHistoryEntry = {
  turnID: string;
  itemID: string;
  text: string;
};

/** The option id the rail names as its active descendant. */
export function queryHistoryOptionID(listID: string, index: number): string {
  return `${listID}-option-${index}`;
}

export type QueryHistoryPopoverProps = {
  id: string;
  width: number;
  entries: QueryHistoryEntry[];
  activeIndex: number;
  onActivate: (index: number) => void;
  onSelect: (entry: QueryHistoryEntry) => void;
  onMouseEnter: () => void;
  onMouseLeave: () => void;
};

export function QueryHistoryPopover({
  id,
  width,
  entries,
  activeIndex,
  onActivate,
  onSelect,
  onMouseEnter,
  onMouseLeave,
}: QueryHistoryPopoverProps): JSX.Element {
  const { t } = useI18n();

  useEffect(() => {
    document.getElementById(queryHistoryOptionID(id, activeIndex))?.scrollIntoView({ block: "nearest" });
  }, [id, activeIndex]);

  return (
    <div
      id={id}
      className="select-menu-panel query-history-popover"
      role="listbox"
      aria-label={t("queryHistory.list")}
      style={{ width }}
      onMouseEnter={onMouseEnter}
      onMouseLeave={onMouseLeave}
      onMouseDown={(event) => event.preventDefault()}
    >
      <div className="select-menu-options">
        {entries.map((entry, index) => (
          <div
            key={`${entry.turnID}:${entry.itemID}`}
            id={queryHistoryOptionID(id, index)}
            className="select-menu-item query-history-item"
            role="option"
            aria-selected={index === activeIndex}
            onMouseEnter={() => onActivate(index)}
            onClick={() => onSelect(entry)}
          >
            <span className="query-history-text">{entry.text.trim()}</span>
          </div>
        ))}
      </div>
    </div>
  );
}
