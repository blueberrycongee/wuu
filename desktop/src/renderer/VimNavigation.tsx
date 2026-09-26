import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import { isTouchWebShell } from "./ComposerFocus";
import { FloatingMenuPortal } from "./ComposerFloatingMenu";
import { useI18n } from "./i18n";
import "./VimNavigation.css";

const storageKey = "wuu.vim-navigation.v1";
const changeEvent = "wuu-vim-navigation-change";

function readEnabled(): boolean {
  try { return localStorage.getItem(storageKey) === "true"; }
  catch { return false; }
}

function subscribe(listener: () => void): () => void {
  window.addEventListener(changeEvent, listener);
  window.addEventListener("storage", listener);
  return () => {
    window.removeEventListener(changeEvent, listener);
    window.removeEventListener("storage", listener);
  };
}

export function useVimNavigationEnabled(): boolean {
  return useSyncExternalStore(subscribe, readEnabled);
}

export function saveVimNavigationEnabled(enabled: boolean): void {
  localStorage.setItem(storageKey, String(enabled));
  window.dispatchEvent(new Event(changeEvent));
}

// One command list drives matching, prefix hints, and the settings reference.
export const vimCommands = [
  { keys: "j", action: "down", group: "reading" },
  { keys: "k", action: "up", group: "reading" },
  { keys: "Ctrl+d", action: "halfDown", group: "reading" },
  { keys: "Ctrl+u", action: "halfUp", group: "reading" },
  { keys: "gg", action: "top", group: "reading" },
  { keys: "G", action: "bottom", group: "reading" },
  { keys: "i", action: "compose", group: "conversations" },
  { keys: "/", action: "search", group: "conversations" },
  { keys: "gt", action: "next", group: "conversations" },
  { keys: "gT", action: "previous", group: "conversations" },
  { keys: " n", action: "new", group: "conversations" },
  { keys: " s", action: "sidebar", group: "workspace" },
  { keys: " w", action: "workspace", group: "workspace" },
  { keys: " f", action: "files", group: "workspace" },
  { keys: " t", action: "terminal", group: "workspace" },
  { keys: " d", action: "review", group: "workspace" },
  { keys: " b", action: "browser", group: "workspace" },
  { keys: " ,", action: "settings", group: "workspace" },
  { keys: "?", action: "help", group: "workspace" },
] as const;

export type VimAction = typeof vimCommands[number]["action"];

export function vimKeyLabel(keys: string, space: string): string {
  return keys.startsWith(" ") ? `${space} ${keys.slice(1)}` : keys;
}

function ownsKeyboard(element: Element | null): boolean {
  return Boolean(element?.closest(
    'input, textarea, select, [contenteditable]:not([contenteditable="false"]), ' +
    '[role="textbox"], [role="combobox"], [role="slider"], [role="spinbutton"], ' +
    '[role="tree"], [role="grid"], .monaco-editor, .xterm, [data-vim-ignore]',
  ));
}

function hasOpenPopup(): boolean {
  return [...document.querySelectorAll('[aria-modal="true"], [role="menu"], [role="listbox"], [data-wuu-layer="menu"]:not([data-floating-menu-owner="vim-navigation"])')]
    .some(element => element.getClientRects().length > 0);
}

export function useVimNavigation(onAction: (action: VimAction) => void): {
  enabled: boolean; editing: boolean; prefix: string;
} {
  const enabled = useVimNavigationEnabled();
  const [editing, setEditing] = useState(false);
  const [prefix, setPrefix] = useState("");
  const actionRef = useRef(onAction);
  actionRef.current = onAction;

  useEffect(() => {
    if (!enabled || isTouchWebShell()) return;
    let pending = "";
    let timer: number | undefined;
    const clear = () => {
      pending = "";
      setPrefix("");
      window.clearTimeout(timer);
    };
    const focusChanged = () => { clear(); setEditing(ownsKeyboard(document.activeElement)); };
    focusChanged();
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.isComposing || event.keyCode === 229 || hasOpenPopup()) {
        clear();
        return;
      }
      const target = event.composedPath().find(node => node instanceof Element) as Element | undefined;
      if (ownsKeyboard(target ?? null) || ownsKeyboard(document.activeElement)) {
        clear();
        // Composer Escape first belongs to slash menus and stopping a turn.
        // Only an unconsumed Escape leaves the composer; terminal/editor Escape
        // remains entirely owned by those tools.
        if (event.key === "Escape" && !event.repeat && !event.ctrlKey && !event.metaKey && !event.altKey &&
            target instanceof HTMLTextAreaElement && target.closest('[data-wuu-component="composer"], .split-composer-shell, .channel-composer')) {
          event.preventDefault();
          target.blur();
          setEditing(false);
        }
        return;
      }
      if (event.key === "Escape") {
        clear();
        if (!event.ctrlKey && !event.metaKey && !event.altKey && !event.shiftKey && target instanceof HTMLElement) target.blur();
        return;
      }
      if (event.metaKey || event.altKey || event.getModifierState("AltGraph")) { clear(); return; }
      if (["Shift", "Control"].includes(event.key)) return;
      if (event.shiftKey && !["G", "T", "?"].includes(event.key)) { clear(); return; }
      if (event.ctrlKey && (event.shiftKey || !["d", "u"].includes(event.key))) { clear(); return; }
      const key = event.ctrlKey ? `Ctrl+${event.key}` : event.key;
      // Space and character keys on native controls retain activation and
      // type-ahead semantics. Escape/Tab can move focus back to the page.
      if (target?.closest('button, a[href], [role="button"], [role="tab"], [role="switch"], summary')) {
        clear(); return;
      }
      const sequence = pending + key;
      const command = vimCommands.find(item => item.keys === sequence);
      if (event.repeat && !["j", "k", "Ctrl+d", "Ctrl+u"].includes(sequence)) return;
      if (command) {
        clear();
        event.preventDefault();
        actionRef.current(command.action);
      } else if (vimCommands.some(item => item.keys.startsWith(sequence))) {
        event.preventDefault();
        pending = sequence;
        setPrefix(sequence);
        window.clearTimeout(timer);
        timer = window.setTimeout(clear, 2000);
      } else {
        clear();
      }
    };
    window.addEventListener("keydown", onKeyDown);
    window.addEventListener("blur", clear);
    document.addEventListener("focusin", focusChanged);
    document.addEventListener("focusout", focusChanged);
    document.addEventListener("pointerdown", clear, true);
    document.addEventListener("compositionstart", clear);
    return () => {
      window.clearTimeout(timer);
      window.removeEventListener("keydown", onKeyDown);
      window.removeEventListener("blur", clear);
      document.removeEventListener("focusin", focusChanged);
      document.removeEventListener("focusout", focusChanged);
      document.removeEventListener("pointerdown", clear, true);
      document.removeEventListener("compositionstart", clear);
    };
  }, [enabled]);
  return { enabled: enabled && !isTouchWebShell(), editing, prefix: enabled ? prefix : "" };
}

export function VimNavigationStatus({ navigation, onHelp }: {
  navigation: ReturnType<typeof useVimNavigation>; onHelp: () => void;
}): JSX.Element | null {
  const { t } = useI18n();
  const anchor = useRef<HTMLButtonElement>(null);
  if (!navigation.enabled) return null;
  return <div className="vim-navigation-status" data-vim-mode={navigation.editing ? "insert" : "normal"}>
    <button ref={anchor} type="button" className="vim-mode-button" onClick={onHelp} title={t("vim.help")}>
      {navigation.editing ? "INSERT" : "NORMAL"}
      {navigation.prefix ? <kbd>{vimKeyLabel(navigation.prefix, t("vim.space"))}…</kbd> : null}
    </button>
    {navigation.prefix ? <FloatingMenuPortal anchorRef={anchor} owner="vim-navigation" placement="below" align="right" width={392}>
    <div className="vim-prefix-hints" role="status">
      {vimCommands.filter(command => command.keys.startsWith(navigation.prefix)).map(command =>
        <div key={command.action}><kbd className="vim-key">{vimKeyLabel(command.keys, t("vim.space"))}</kbd><span>{t(`vim.${command.action}`)}</span></div>,
      )}
    </div></FloatingMenuPortal> : null}
  </div>;
}
