import { useEffect, useId, useState, type KeyboardEvent, type RefObject } from "react";
import type { ComposerInputHandle } from "./ComposerInput";
import type { ComposerSlashCommand } from "./ComposerSlashCommands";
import { composerSlashPrompt, filterComposerSlashCommands, firstEnabledSlashCommandIndex, nextEnabledSlashCommandIndex, parseComposerSlashDraft } from "./ComposerSlashCommands";
import { ComposerSlashMenu } from "./ComposerSlashMenu";
import { FloatingMenuPortal, composerMenuWidth } from "./ComposerFloatingMenu";
import { COMPOSER_COMMAND_MENU_WIDTH } from "./ComposerTypes";
import { useI18n } from "./i18n";

export function useConversationCommands({ commands, value, setValue, disabled, inputRef, anchorRef, onAction, loading, error, refresh }: {
  commands?: ComposerSlashCommand[]; value: string; setValue(value: string): void; disabled: boolean;
  inputRef: RefObject<ComposerInputHandle | null>; anchorRef: RefObject<HTMLDivElement | null>;
  onAction?(command: ComposerSlashCommand): void; loading?: boolean; error?: string; refresh?(): void;
}) {
  const { t } = useI18n();
  const id = useId();
  const [focused, setFocused] = useState(false);
  const [dismissed, setDismissed] = useState<string>();
  const [selected, setSelected] = useState(0);
  const draft = commands && !disabled && !value.includes("\n") ? parseComposerSlashDraft(value) : undefined;
  const visible = filterComposerSlashCommands(commands ?? [], draft?.query ?? "");
  const open = focused && !draft?.query.includes("/") && Boolean(draft) && dismissed !== value;
  const selectionKey = JSON.stringify(visible.map(command => [command.id, command.disabledReason]));
  useEffect(() => setSelected(firstEnabledSlashCommandIndex(visible)), [draft?.query, selectionKey]);
  useEffect(() => {
    if (open) document.getElementById(`${id}-${visible[selected]?.id}`)?.scrollIntoView({ block: "nearest" });
  }, [id, open, selected]);
  const apply = (command: ComposerSlashCommand) => {
    if (disabled || command.disabledReason) return;
    if (command.kind === "action") onAction?.(command);
    else setValue(composerSlashPrompt(command, draft?.args ?? ""));
    setDismissed(value);
    inputRef.current?.focus({ preventScroll: true });
  };
  const handleKeyDown = (event: KeyboardEvent<HTMLElement>): boolean => {
    if (!open || event.nativeEvent.isComposing || event.keyCode === 229) return false;
    if (event.key === "Escape") { event.preventDefault(); event.stopPropagation(); setDismissed(value); return true; }
    if (event.shiftKey || event.metaKey || event.ctrlKey || event.altKey) return false;
    if (["ArrowDown", "ArrowUp"].includes(event.key) && visible.length) {
      event.preventDefault(); setSelected(current => nextEnabledSlashCommandIndex(visible, current, event.key === "ArrowDown" ? 1 : -1)); return true;
    }
    if (["Enter", "Tab"].includes(event.key)) {
      const command = visible[selected] ?? visible[firstEnabledSlashCommandIndex(visible)];
      if (command) { event.preventDefault(); apply(command); return true; }
      // A failed/loading catalog must not turn a potential skill invocation into a message.
      if (loading || error) { event.preventDefault(); return true; }
    }
    return false;
  };
  const width = composerMenuWidth(COMPOSER_COMMAND_MENU_WIDTH);
  return { open, apply, handleKeyDown,
    onFocus: () => { setFocused(true); }, onBlur: () => setFocused(false),
    controls: open ? id : undefined, activeDescendant: open && visible[selected] ? `${id}-${visible[selected].id}` : undefined,
    menu: open ? <FloatingMenuPortal anchorRef={anchorRef} owner="composer-slash" placement="above" align="left" offset={8} width={width}>
      <ComposerSlashMenu id={id} width={width} commands={visible} selectedIndex={selected} onSelectIndex={setSelected}
        onSelect={apply} query={draft?.query ?? ""} status={loading ? <div role="status" className="slash-command-empty">{t("common.loading")}</div>
          : error ? <div role="alert" className="slash-command-empty">{error} {refresh && <button type="button" onMouseDown={event => event.preventDefault()} onClick={refresh}>{t("composer.retryCommands")}</button>}</div> : undefined} />
    </FloatingMenuPortal> : null };
}
