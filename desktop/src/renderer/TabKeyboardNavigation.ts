import {
  useCallback,
  useLayoutEffect,
  useRef,
  type KeyboardEvent as ReactKeyboardEvent,
  type RefObject,
} from "react";

const NAVIGATION_KEYS = new Set(["ArrowLeft", "ArrowRight", "Home", "End"]);

export function handleTabListKeyDown(event: ReactKeyboardEvent<HTMLElement>): void {
  if (
    !NAVIGATION_KEYS.has(event.key) ||
    event.altKey ||
    event.ctrlKey ||
    event.metaKey
  ) {
    return;
  }
  const eventTarget = event.target;
  if (!(eventTarget instanceof Element)) {
    return;
  }
  const currentTab = eventTarget.closest<HTMLButtonElement>('[role="tab"]');
  if (!currentTab || !event.currentTarget.contains(currentTab)) {
    return;
  }
  const tabs = Array.from(
    event.currentTarget.querySelectorAll<HTMLButtonElement>('[role="tab"]:not(:disabled)'),
  );
  const currentIndex = tabs.indexOf(currentTab);
  if (currentIndex < 0 || tabs.length < 2) {
    return;
  }

  let nextIndex: number;
  if (event.key === "Home") {
    nextIndex = 0;
  } else if (event.key === "End") {
    nextIndex = tabs.length - 1;
  } else if (event.key === "ArrowRight") {
    nextIndex = (currentIndex + 1) % tabs.length;
  } else {
    nextIndex = (currentIndex - 1 + tabs.length) % tabs.length;
  }

  event.preventDefault();
  const nextTab = tabs[nextIndex];
  nextTab.focus();
  nextTab.click();
}

export function useTabCloseFocusRestoration(
  activeTabID: string | undefined,
  tabIDs: readonly string[],
  fallbackRef: RefObject<HTMLElement | null>,
): {
  requestFocusRestoration: () => void;
  tabListRef: RefObject<HTMLDivElement | null>;
} {
  const tabListRef = useRef<HTMLDivElement>(null);
  const pendingTabOrderRef = useRef<string | undefined>(undefined);
  const tabOrder = tabIDs.join("\0");

  const requestFocusRestoration = useCallback(() => {
    if (tabListRef.current?.contains(document.activeElement)) {
      pendingTabOrderRef.current = tabOrder;
    }
  }, [tabOrder]);

  useLayoutEffect(() => {
    const previousTabOrder = pendingTabOrderRef.current;
    if (previousTabOrder === undefined || previousTabOrder === tabOrder) {
      return;
    }
    pendingTabOrderRef.current = undefined;
    const activeTab = tabListRef.current?.querySelector<HTMLButtonElement>(
      '[role="tab"][aria-selected="true"]',
    );
    (activeTab ?? fallbackRef.current)?.focus();
  }, [activeTabID, fallbackRef, tabOrder]);

  return { requestFocusRestoration, tabListRef };
}
