import { useLayoutEffect, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from "react";
import type { DesktopProject } from "../shared/protocol";
import { composerMenuWidth, FloatingMenuPortal, useFloatingMenuFocus } from "./ComposerFloatingMenu";
import { Tooltip } from "./Tooltip";
import { useI18n } from "./i18n";
import {
  Activity,
  ArrowUpDown,
  CalendarDays,
  Check,
  ChevronRight,
  CircleDot,
  Clock,
  Folder,
  GripHorizontal,
  Layers,
  List,
  ListFilter,
  ListTree,
  RotateCcw,
  SlidersHorizontal,
} from "./WuuIcons";

export type RecentSort = "manual" | "updated" | "created";
export type RecentStatusFilter = "all" | "unread" | "running";
export type RecentView = {
  sort: RecentSort;
  status: RecentStatusFilter;
  /** Absent shows every workspace. */
  workspaceID?: string;
  includeFoldered: boolean;
};

export const DEFAULT_RECENT_VIEW: RecentView = { sort: "manual", status: "all", includeFoldered: true };

const RECENT_VIEW_KEY = "wuu.desktop.sidebarRecentView.v1";
const SORTS: readonly RecentSort[] = ["manual", "updated", "created"];
const STATUSES: readonly RecentStatusFilter[] = ["all", "unread", "running"];

export function loadRecentView(): RecentView {
  try {
    const stored = JSON.parse(window.localStorage.getItem(RECENT_VIEW_KEY) || "null") as Partial<RecentView> | null;
    if (!stored || typeof stored !== "object") return DEFAULT_RECENT_VIEW;
    return {
      sort: SORTS.includes(stored.sort as RecentSort) ? stored.sort as RecentSort : DEFAULT_RECENT_VIEW.sort,
      status: STATUSES.includes(stored.status as RecentStatusFilter) ? stored.status as RecentStatusFilter : DEFAULT_RECENT_VIEW.status,
      workspaceID: typeof stored.workspaceID === "string" && stored.workspaceID ? stored.workspaceID : undefined,
      includeFoldered: typeof stored.includeFoldered === "boolean" ? stored.includeFoldered : DEFAULT_RECENT_VIEW.includeFoldered,
    };
  } catch {
    return DEFAULT_RECENT_VIEW;
  }
}

export function persistRecentView(view: RecentView): void {
  try { window.localStorage.setItem(RECENT_VIEW_KEY, JSON.stringify(view)); }
  catch { /* Blocked storage keeps the choice for this window only. */ }
}

/** Whether the view hides any conversation, as opposed to only reordering. */
export function recentViewFilters(view: RecentView): boolean {
  return view.status !== "all" || view.workspaceID !== undefined || !view.includeFoldered;
}

function sameView(left: RecentView, right: RecentView): boolean {
  return left.sort === right.sort && left.status === right.status
    && left.workspaceID === right.workspaceID && left.includeFoldered === right.includeFoldered;
}

type Submenu = "sort" | "status" | "workspace";
type Choice = { key: string; label: string; icon: ReactNode; checked: boolean; select: () => void };

const ITEM_SELECTOR = '[role^="menuitem"]:not(:disabled)';

function moveFocus(event: ReactKeyboardEvent<HTMLElement>, container: HTMLElement | null): boolean {
  const { key } = event;
  if (key !== "ArrowDown" && key !== "ArrowUp" && key !== "Home" && key !== "End") return false;
  event.preventDefault();
  const items = Array.from(container?.querySelectorAll<HTMLElement>(ITEM_SELECTOR) ?? []);
  if (items.length === 0) return true;
  const index = items.indexOf(document.activeElement as HTMLElement);
  const next = key === "Home" ? 0 : key === "End" ? items.length - 1
    : index < 0 ? (key === "ArrowDown" ? 0 : items.length - 1)
    : (index + (key === "ArrowDown" ? 1 : -1) + items.length) % items.length;
  items[next]?.focus();
  return true;
}

/**
 * Display options for Recent: one level of rows whose choices open in a
 * flyout beside them, so the current value of every option stays readable.
 */
export function SidebarRecentViewMenu({ view, workspaces, onChange }: {
  view: RecentView;
  workspaces: readonly DesktopProject[];
  onChange: (view: RecentView) => void;
}): JSX.Element {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const [submenu, setSubmenu] = useState<Submenu>();
  const [focusSubmenu, setFocusSubmenu] = useState(false);
  const [flyout, setFlyout] = useState<{ top: number; side: "right" | "left" }>();
  const triggerRef = useRef<HTMLButtonElement>(null);
  const shellRef = useRef<HTMLDivElement>(null);
  const panelRef = useRef<HTMLDivElement>(null);
  const submenuRef = useRef<HTMLDivElement>(null);
  const rowRefs = useRef<Partial<Record<Submenu, HTMLButtonElement | null>>>({});
  useFloatingMenuFocus(panelRef, "recent-view", open);

  const workspace = workspaces.find((candidate) => candidate.id === view.workspaceID);
  const filtered = recentViewFilters({ ...view, workspaceID: workspace?.id });
  const customized = !sameView({ ...view, workspaceID: workspace?.id }, DEFAULT_RECENT_VIEW);
  const showWorkspace = workspaces.length > 1 || workspace !== undefined;

  function close(returnFocus = false): void {
    setOpen(false);
    setSubmenu(undefined);
    if (returnFocus) triggerRef.current?.focus();
  }
  function openSubmenu(next: Submenu | undefined, focus = false): void {
    setSubmenu(next);
    setFocusSubmenu(focus);
  }
  function update(patch: Partial<RecentView>): void {
    onChange({ ...view, ...patch });
  }

  useLayoutEffect(() => {
    if (!submenu) { setFlyout(undefined); return; }
    const shell = shellRef.current?.getBoundingClientRect();
    const row = rowRefs.current[submenu];
    const list = submenuRef.current;
    if (!shell || !row || !list) return;
    const width = list.offsetWidth;
    const gap = 4;
    const side = shell.right + gap + width <= window.innerWidth - 8 || shell.left - gap - width < 8 ? "right" : "left";
    // Align the first choice with its row; stay inside the window bottom.
    const inset = Number.parseFloat(getComputedStyle(list).paddingTop) || 0;
    const preferred = row.getBoundingClientRect().top - inset;
    const top = Math.max(8, Math.min(preferred, window.innerHeight - 8 - list.offsetHeight)) - shell.top;
    setFlyout((current) => current?.top === top && current.side === side ? current : { top, side });
  }, [submenu, view, workspaces]);

  useLayoutEffect(() => {
    if (!focusSubmenu || !flyout) return;
    const list = submenuRef.current;
    (list?.querySelector<HTMLElement>('[aria-checked="true"]') ?? list?.querySelector<HTMLElement>(ITEM_SELECTOR))?.focus();
    setFocusSubmenu(false);
  }, [flyout, focusSubmenu]);

  const sortLabels: Record<RecentSort, string> = {
    manual: t("sidebar.recentSortManual"),
    updated: t("sidebar.recentSortUpdated"),
    created: t("sidebar.recentSortCreated"),
  };
  const statusLabels: Record<RecentStatusFilter, string> = {
    all: t("sidebar.recentStatusAll"),
    unread: t("sidebar.unreadConversations"),
    running: t("sidebar.runningConversations"),
  };
  const choices: Record<Submenu, Choice[]> = {
    sort: SORTS.map((sort) => ({
      key: sort,
      label: sortLabels[sort],
      icon: sort === "manual" ? <GripHorizontal /> : sort === "updated" ? <Clock /> : <CalendarDays />,
      checked: view.sort === sort,
      select: () => update({ sort }),
    })),
    status: STATUSES.map((status) => ({
      key: status,
      label: statusLabels[status],
      icon: status === "all" ? <List /> : status === "unread" ? <CircleDot /> : <Activity />,
      checked: view.status === status,
      select: () => update({ status }),
    })),
    workspace: [
      { key: "", label: t("sidebar.recentWorkspaceAll"), icon: <Layers />, checked: !workspace, select: () => update({ workspaceID: undefined }) },
      ...workspaces.map((candidate) => ({
        key: candidate.id,
        label: candidate.name,
        icon: <Folder />,
        checked: candidate.id === workspace?.id,
        select: () => update({ workspaceID: candidate.id }),
      })),
    ],
  };
  const rows: { id: Submenu; label: string; value: string; icon: ReactNode }[] = [
    { id: "sort", label: t("sidebar.recentSort"), value: sortLabels[view.sort], icon: <ArrowUpDown /> },
    { id: "status", label: t("sidebar.recentStatus"), value: statusLabels[view.status], icon: <ListFilter /> },
    ...(showWorkspace ? [{
      id: "workspace" as const,
      label: t("sidebar.workspace"),
      value: workspace?.name ?? t("sidebar.recentWorkspaceAll"),
      icon: <Layers />,
    }] : []),
  ];

  return (
    <>
      <Tooltip asChild content={t("sidebar.recentViewOptions")}>
        <button
          ref={triggerRef}
          className="sidebar-row-icon-button sidebar-recents-view-trigger"
          type="button"
          aria-label={t("sidebar.recentViewOptions")}
          aria-haspopup="menu"
          aria-expanded={open}
          data-filtered={filtered || undefined}
          onClick={() => (open ? close() : setOpen(true))}
        >
          <SlidersHorizontal className="icon" aria-hidden="true" />
        </button>
      </Tooltip>
      {open ? (
        <FloatingMenuPortal
          anchorRef={triggerRef}
          owner="sidebar-recent-view"
          onDismiss={() => close()}
          placement="below"
          align="left"
          offset={4}
          width={composerMenuWidth(264)}
          flip
        >
          <div
            className="sidebar-view-menu"
            ref={shellRef}
            onKeyDown={(event) => {
              if (event.key === "Tab") close(true);
              else if (event.key === "Escape") {
                event.preventDefault();
                event.stopPropagation();
                close(true);
              }
            }}
          >
            <div
              className="sidebar-view-menu-panel"
              role="menu"
              aria-label={t("sidebar.recentViewOptions")}
              ref={panelRef}
              onKeyDown={(event) => {
                if (moveFocus(event, panelRef.current)) return;
                const id = (event.target as HTMLElement).dataset.submenu as Submenu | undefined;
                if (id && (event.key === "ArrowRight" || event.key === "Enter" || event.key === " ")) {
                  event.preventDefault();
                  openSubmenu(id, true);
                }
              }}
            >
              {rows.map((row) => (
                <button
                  key={row.id}
                  ref={(node) => { rowRefs.current[row.id] = node; }}
                  type="button"
                  role="menuitem"
                  className="sidebar-view-menu-row"
                  aria-haspopup="menu"
                  aria-expanded={submenu === row.id}
                  data-submenu={row.id}
                  onPointerEnter={(event) => {
                    if (event.pointerType !== "touch") openSubmenu(row.id);
                  }}
                  onClick={() => openSubmenu(submenu === row.id ? undefined : row.id)}
                >
                  {row.icon}
                  <span className="sidebar-view-menu-label">{row.label}</span>
                  <span className="sidebar-view-menu-value">{row.value}</span>
                  <ChevronRight className="sidebar-view-menu-chevron" />
                </button>
              ))}
              <div className="sidebar-view-menu-separator" role="separator" />
              <button
                type="button"
                role="menuitemcheckbox"
                aria-checked={view.includeFoldered}
                className="sidebar-view-menu-row"
                onPointerEnter={() => openSubmenu(undefined)}
                onFocus={() => openSubmenu(undefined)}
                onClick={() => update({ includeFoldered: !view.includeFoldered })}
              >
                <ListTree />
                <span className="sidebar-view-menu-label">{t("sidebar.recentIncludeFoldered")}</span>
                <span className="settings-switch sidebar-view-menu-switch" data-checked={view.includeFoldered || undefined} aria-hidden="true">
                  <span className="settings-switch-thumb" />
                </span>
              </button>
              {customized ? (
                <button
                  type="button"
                  role="menuitem"
                  className="sidebar-view-menu-row sidebar-view-menu-reset"
                  onPointerEnter={() => openSubmenu(undefined)}
                  onFocus={() => openSubmenu(undefined)}
                  onClick={() => {
                    onChange(DEFAULT_RECENT_VIEW);
                    triggerRef.current?.focus();
                    close();
                  }}
                >
                  <RotateCcw />
                  <span className="sidebar-view-menu-label">{t("sidebar.recentViewReset")}</span>
                </button>
              ) : null}
            </div>
            {submenu ? (
              <div
                key={submenu}
                ref={submenuRef}
                className="sidebar-view-submenu"
                role="menu"
                aria-label={rows.find((row) => row.id === submenu)?.label}
                data-side={flyout?.side}
                style={{ top: flyout?.top ?? 0, visibility: flyout ? undefined : "hidden" }}
                onKeyDown={(event) => {
                  if (moveFocus(event, submenuRef.current)) return;
                  if (event.key === "ArrowLeft" || event.key === "Escape") {
                    event.preventDefault();
                    event.stopPropagation();
                    rowRefs.current[submenu]?.focus();
                    openSubmenu(undefined);
                  }
                }}
              >
                {choices[submenu].map((choice) => (
                  <button
                    key={choice.key}
                    type="button"
                    role="menuitemradio"
                    aria-checked={choice.checked}
                    data-value={choice.key}
                    className="sidebar-view-menu-row"
                    onClick={() => {
                      choice.select();
                      rowRefs.current[submenu]?.focus();
                      openSubmenu(undefined);
                    }}
                  >
                    {choice.icon}
                    <span className="sidebar-view-menu-label">{choice.label}</span>
                    <Check className="sidebar-view-menu-check" />
                  </button>
                ))}
              </div>
            ) : null}
          </div>
        </FloatingMenuPortal>
      ) : null}
    </>
  );
}
