/**
 * QueryHistoryRail — a quiet column of ticks on the conversation's right
 * edge, one per past query, oldest at the top. Resting on it (or clicking it)
 * opens the list of those queries beside it; choosing one jumps to it.
 *
 * The rail is a select-only combobox: focus stays on it while the arrow keys
 * move through the list, Enter jumps and Escape closes. A tick names the same
 * query as its row, so hovering either highlights both.
 */
import { useEffect, useId, useRef, useState, type JSX, type KeyboardEvent, type MouseEvent } from "react";

import { composerMenuWidth, FloatingMenuPortal } from "./ComposerFloatingMenu";
import { QueryHistoryPopover, queryHistoryOptionID, type QueryHistoryEntry } from "./QueryHistoryPopover";
import { useI18n } from "./i18n";

// The column stays an at-a-glance index: past this many queries it shows the
// latest ones, and the list still holds them all.
const MAX_TICKS = 20;
// Resting briefly opens the list, so a pointer crossing the rail on its way
// to the scrollbar does not; leaving waits long enough to reach the list.
const OPEN_DELAY_MS = 150;
const CLOSE_DELAY_MS = 200;
const LIST_GAP_PX = 4;

export type QueryHistoryRailProps = {
  entries: QueryHistoryEntry[];
  onSelect: (entry: QueryHistoryEntry) => void;
};

export function QueryHistoryRail({ entries, onSelect }: QueryHistoryRailProps): JSX.Element | null {
  const { t } = useI18n();
  const railRef = useRef<HTMLDivElement | null>(null);
  const openTimerRef = useRef<number | undefined>(undefined);
  const closeTimerRef = useRef<number | undefined>(undefined);
  const [open, setOpen] = useState(false);
  const [activeIndex, setActiveIndex] = useState(-1);
  const [railWidth, setRailWidth] = useState(0);
  const [listWidth, setListWidth] = useState(0);
  const listID = useId();
  const last = entries.length - 1;
  const tickOffset = Math.max(0, entries.length - MAX_TICKS);
  const expanded = open && entries.length > 1;
  const active = Math.min(activeIndex, last);

  useEffect(() => () => {
    window.clearTimeout(openTimerRef.current);
    window.clearTimeout(closeTimerRef.current);
  }, []);

  if (entries.length < 2) {
    return null;
  }

  function cancelTimers(): void {
    window.clearTimeout(openTimerRef.current);
    window.clearTimeout(closeTimerRef.current);
    openTimerRef.current = undefined;
    closeTimerRef.current = undefined;
  }

  function openAt(index: number): void {
    cancelTimers();
    setRailWidth(railRef.current?.offsetWidth ?? 0);
    setListWidth(Math.min(composerMenuWidth(320), window.innerWidth - 32));
    setActiveIndex(Math.min(Math.max(index, 0), last));
    setOpen(true);
  }

  function close(): void {
    cancelTimers();
    setOpen(false);
  }

  function scheduleClose(): void {
    window.clearTimeout(openTimerRef.current);
    openTimerRef.current = undefined;
    window.clearTimeout(closeTimerRef.current);
    closeTimerRef.current = window.setTimeout(close, CLOSE_DELAY_MS);
  }

  function select(entry: QueryHistoryEntry): void {
    close();
    onSelect(entry);
  }

  function tickIndexAt(event: MouseEvent<HTMLDivElement>): number | undefined {
    const tick = (event.target as Element).closest<HTMLElement>("[data-tick]");
    return tick ? tickOffset + Number(tick.dataset.tick) : undefined;
  }

  function handleMouseEnter(event: MouseEvent<HTMLDivElement>): void {
    window.clearTimeout(closeTimerRef.current);
    closeTimerRef.current = undefined;
    // A pressed button means a drag, such as the scrollbar's, is passing by.
    if (expanded || event.buttons !== 0) return;
    const index = tickIndexAt(event) ?? last;
    window.clearTimeout(openTimerRef.current);
    openTimerRef.current = window.setTimeout(() => openAt(index), OPEN_DELAY_MS);
  }

  function handleMouseMove(event: MouseEvent<HTMLDivElement>): void {
    const index = tickIndexAt(event);
    if (expanded && index !== undefined) setActiveIndex(index);
  }

  function handleClick(event: MouseEvent<HTMLDivElement>): void {
    const index = tickIndexAt(event);
    if (index !== undefined) {
      select(entries[index]);
    } else if (expanded) {
      close();
    } else {
      openAt(last);
    }
  }

  function handleKeyDown(event: KeyboardEvent<HTMLDivElement>): void {
    const move = (index: number): void => {
      event.preventDefault();
      if (expanded) setActiveIndex(Math.min(Math.max(index, 0), last));
      else openAt(index);
    };
    switch (event.key) {
      case "ArrowDown":
        move(expanded ? active + 1 : last);
        break;
      case "ArrowUp":
        move(expanded ? active - 1 : last);
        break;
      case "Home":
        move(0);
        break;
      case "End":
        move(last);
        break;
      case "Enter":
      case " ":
        event.preventDefault();
        if (expanded && entries[active]) select(entries[active]);
        else openAt(last);
        break;
      case "Escape":
        if (!expanded) return;
        // The list is the only thing Escape should dismiss here.
        event.preventDefault();
        event.stopPropagation();
        close();
        break;
      case "Tab":
        close();
        break;
    }
  }

  const activeTick = expanded ? active - tickOffset : -1;

  return (
    <div className="query-history-rail">
      <div
        ref={railRef}
        className="query-history-rail-ticks"
        role="combobox"
        tabIndex={0}
        aria-label={t("queryHistory.index")}
        aria-haspopup="listbox"
        aria-expanded={expanded}
        aria-controls={expanded ? listID : undefined}
        aria-activedescendant={expanded && active >= 0 ? queryHistoryOptionID(listID, active) : undefined}
        onMouseEnter={handleMouseEnter}
        onMouseMove={handleMouseMove}
        onMouseLeave={scheduleClose}
        onClick={handleClick}
        onKeyDown={handleKeyDown}
        onBlur={close}
      >
        {entries.slice(tickOffset).map((entry, index) => (
          <span
            key={`${entry.turnID}:${entry.itemID}`}
            className="query-history-rail-tick"
            data-tick={index}
            data-active={index === activeTick || undefined}
          />
        ))}
      </div>
      {expanded ? (
        <FloatingMenuPortal
          anchorRef={railRef}
          owner="composer-query-history"
          placement="middle"
          align="right"
          crossAxisOffset={-(railWidth + LIST_GAP_PX)}
          width={listWidth}
        >
          <QueryHistoryPopover
            id={listID}
            width={listWidth}
            entries={entries}
            activeIndex={active}
            onActivate={setActiveIndex}
            onSelect={select}
            onMouseEnter={cancelTimers}
            onMouseLeave={scheduleClose}
          />
        </FloatingMenuPortal>
      ) : null}
    </div>
  );
}
