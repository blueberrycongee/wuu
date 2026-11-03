import { forwardRef, useImperativeHandle, useLayoutEffect, useRef, useState, type TextareaHTMLAttributes } from "react";
import { draftPluginReferences, pluginReferencePlainText } from "./ComposerPluginReferences";
import "./ComposerInput.css";

export interface RichComposerInput {
  readonly element: HTMLElement;
  readonly value: string;
  readonly selectionStart: number;
  readonly selectionEnd: number;
  readonly selectionDirection: "forward" | "backward" | "none";
  readonly disabled: boolean;
  readonly readOnly: boolean;
  focus(options?: FocusOptions): void;
  blur(): void;
  select(): void;
  setRangeText(text: string, start: number, end: number, selectionMode?: SelectionMode): void;
  setSelectionRange(start: number, end: number, direction?: "forward" | "backward" | "none"): void;
}
export type ComposerInputHandle = HTMLTextAreaElement | RichComposerInput;
export function composerInputElement(input: ComposerInputHandle): HTMLElement {
  return "element" in input ? input.element : input;
}

type Props = Omit<TextareaHTMLAttributes<HTMLElement>, "value" | "onChange" | "defaultValue"> & {
  value: string;
  resetKey?: string;
  onValueChange(value: string): void;
};

function nodeValue(node: Node): string {
  if (node instanceof HTMLElement && node.dataset.pluginToken) return node.dataset.pluginToken;
  if (node.nodeType === Node.TEXT_NODE) return node.textContent ?? "";
  if (node.nodeName === "BR") return "\n";
  return Array.from(node.childNodes).map(nodeValue).join("");
}

function readSelection(root: HTMLElement): [number, number] | undefined {
  const selection = window.getSelection();
  if (!selection?.rangeCount || !root.contains(selection.anchorNode) || !root.contains(selection.focusNode)) return;
  const offset = (node: Node, position: number): number => {
    let total = node.nodeType === Node.TEXT_NODE ? position : Array.from(node.childNodes).slice(0, position).reduce((sum, child) => sum + nodeValue(child).length, 0);
    for (let current = node; current !== root && current.parentNode; current = current.parentNode) {
      if (current instanceof HTMLElement && current.dataset.pluginToken) total = position ? nodeValue(current).length : 0;
      for (let sibling = current.previousSibling; sibling; sibling = sibling.previousSibling) total += nodeValue(sibling).length;
    }
    return total;
  };
  const start = offset(selection.anchorNode!, selection.anchorOffset);
  const end = offset(selection.focusNode!, selection.focusOffset);
  return [Math.min(start, end), Math.max(start, end)];
}

function placeSelection(root: HTMLElement, start: number, end: number): void {
  function point(position: number): [Node, number] {
    let remaining = position;
    const children = Array.from(root.childNodes);
    for (let i = 0; i < children.length; i++) {
      const child = children[i];
      const length = nodeValue(child).length;
      if (remaining <= length) {
        if (child.nodeType === Node.TEXT_NODE) return [child, remaining];
        return [root, i + (remaining > 0 ? 1 : 0)];
      }
      remaining -= length;
    }
    return [root, children.length];
  }
  const range = document.createRange();
  range.setStart(...point(start));
  range.setEnd(...point(end));
  const selection = window.getSelection();
  selection?.removeAllRanges();
  selection?.addRange(range);
}

function renderValue(root: HTMLElement, value: string): void {
  const nodes: Node[] = [];
  let offset = 0;
  for (const ref of draftPluginReferences(value)) {
    nodes.push(document.createTextNode(value.slice(offset, ref.start)));
    const chip = document.createElement("span");
    chip.contentEditable = "false";
    chip.className = "composer-plugin-reference";
    chip.dataset.pluginToken = ref.token;
    chip.textContent = ref.text;
    chip.title = [ref.plugin.plugin_id, ref.plugin.mcp_server, ref.plugin.account_id, ref.plugin.workspace_id].filter(Boolean).join(" · ");
    nodes.push(chip);
    offset = ref.end;
  }
  nodes.push(document.createTextNode(value.slice(offset)));
  root.replaceChildren(...nodes);
}

/** Native text editing stays in place until the draft contains an atomic reference. */
export const ComposerInput = forwardRef<ComposerInputHandle, Props>(function ComposerInput({ value, resetKey, onValueChange, ...props }, ref) {
  const [rich, setRich] = useState(() => draftPluginReferences(value).length > 0);
  const textarea = useRef<HTMLTextAreaElement>(null);
  const editor = useRef<HTMLDivElement>(null);
  const live = useRef(value);
  const previousResetKey = useRef(resetKey);
  const composing = useRef(false);
  const selection = useRef<[number, number]>([0, 0]);
  const restoreFocus = useRef(false);
  const undo = useRef<{ value: string; selection: [number, number] }[]>([]);
  const redo = useRef<{ value: string; selection: [number, number] }[]>([]);
  const callbacks = useRef({ onValueChange, props });
  callbacks.current = { onValueChange, props };
  if (!rich && draftPluginReferences(value).length) {
    restoreFocus.current = document.activeElement === textarea.current;
    selection.current = [textarea.current?.selectionStart ?? value.length, textarea.current?.selectionEnd ?? value.length];
    setRich(true);
  }
  function currentSelection(): [number, number] {
    if (editor.current) selection.current = readSelection(editor.current) ?? selection.current;
    return selection.current;
  }
  useImperativeHandle(ref, () => ({
    get element() { return editor.current ?? textarea.current!; },
    get value() { return textarea.current?.value ?? live.current; },
    get selectionStart() { return textarea.current?.selectionStart ?? currentSelection()[0]; },
    get selectionEnd() { return textarea.current?.selectionEnd ?? currentSelection()[1]; },
    get selectionDirection() { return textarea.current?.selectionDirection ?? "none"; },
    get disabled() { return !!callbacks.current.props.disabled; },
    get readOnly() { return !!callbacks.current.props.readOnly; },
    focus(options) { (editor.current ?? textarea.current)?.focus(options); },
    blur() { (editor.current ?? textarea.current)?.blur(); },
    select() { this.setSelectionRange(0, this.value.length); },
    setRangeText(text, start, end, mode) {
      if (textarea.current) textarea.current.setRangeText(text, start, end, mode);
      else {
        const next = live.current.slice(0, start) + text + live.current.slice(end);
        publish(next, [start + text.length, start + text.length]);
      }
    },
    setSelectionRange(start: number, end: number, direction?: "forward" | "backward" | "none") {
      selection.current = [start, end];
      if (textarea.current) textarea.current.setSelectionRange(start, end, direction);
      else if (editor.current) placeSelection(editor.current, start, end);
    },
  }), []);

  useLayoutEffect(() => {
    if (previousResetKey.current !== resetKey || (value === "" && live.current !== value)) {
      undo.current = [];
      redo.current = [];
      previousResetKey.current = resetKey;
    } else if (rich && live.current !== value) {
      undo.current.push({ value: live.current, selection: selection.current });
      if (undo.current.length > 100) undo.current.shift();
      redo.current = [];
    }
    if (!rich || !editor.current || composing.current) { live.current = value; return; }
    const root = editor.current;
    if (nodeValue(root) !== value) {
      const selected = currentSelection();
      renderValue(root, value);
      if (document.activeElement === root || restoreFocus.current) {
        root.focus({ preventScroll: true });
        placeSelection(root, Math.min(selected[0], value.length), Math.min(selected[1], value.length));
      }
    }
    restoreFocus.current = false;
    live.current = value;
  }, [value, rich, resetKey]);

  function publish(next: string, selected: [number, number], record = true): void {
    if (record && next !== live.current) {
      undo.current.push({ value: live.current, selection: currentSelection() });
      if (undo.current.length > 100) undo.current.shift();
      redo.current = [];
    }
    live.current = next;
    selection.current = selected;
    if (editor.current && !composing.current) {
      renderValue(editor.current, next);
      placeSelection(editor.current, ...selected);
    }
    callbacks.current.onValueChange(next);
  }
  function replaceSelection(text: string): void {
    const [start, end] = currentSelection();
    publish(live.current.slice(0, start) + text + live.current.slice(end), [start + text.length, start + text.length]);
  }
  if (!rich) return <textarea {...props} ref={textarea} value={value} onChange={event => {
    live.current = event.target.value;
    selection.current = [event.target.selectionStart, event.target.selectionEnd];
    onValueChange(event.target.value);
  }} />;
  return <div
    {...props}
    ref={editor}
    className={`composer-rich-input ${props.className ?? ""}`}
    role="textbox"
    aria-multiline="true"
    aria-disabled={props.disabled || undefined}
    aria-readonly={props.readOnly || undefined}
    aria-label={props["aria-label"] ?? props.placeholder}
    data-placeholder={props.placeholder}
    contentEditable={!props.disabled && !props.readOnly}
    suppressContentEditableWarning
    onFocus={props.onFocus}
    onBlur={event => { currentSelection(); props.onBlur?.(event); }}
    onSelect={event => { currentSelection(); props.onSelect?.(event); }}
    onCompositionStart={event => { composing.current = true; props.onCompositionStart?.(event); }}
    onCompositionEnd={event => {
      composing.current = false;
      publish(nodeValue(event.currentTarget), currentSelection());
      props.onCompositionEnd?.(event);
    }}
    onInput={event => {
      if (!composing.current) publish(nodeValue(event.currentTarget), currentSelection());
    }}
    onPaste={event => {
      props.onPaste?.(event);
      if (event.defaultPrevented) return;
      event.preventDefault();
      replaceSelection(event.clipboardData.getData("text/plain"));
    }}
    onCopy={event => {
      const [start, end] = currentSelection();
      event.preventDefault();
      event.clipboardData.setData("text/plain", pluginReferencePlainText(live.current.slice(start, end)));
    }}
    onCut={event => {
      const [start, end] = currentSelection();
      event.preventDefault();
      event.clipboardData.setData("text/plain", pluginReferencePlainText(live.current.slice(start, end)));
      if (!props.disabled && !props.readOnly) replaceSelection("");
    }}
    onKeyDown={event => {
      if (event.nativeEvent.isComposing) return;
      if (!props.disabled && !props.readOnly && (event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "z") {
        event.preventDefault();
        const from = event.shiftKey ? redo : undo;
        const to = event.shiftKey ? undo : redo;
        const previous = from.current.pop();
        if (previous) {
          to.current.push({ value: live.current, selection: currentSelection() });
          publish(previous.value, previous.selection, false);
        }
        return;
      }
      props.onKeyDown?.(event);
      if (event.defaultPrevented || props.disabled || props.readOnly) return;
      if (event.key === "Enter") { event.preventDefault(); replaceSelection("\n"); return; }
      if (event.key !== "Backspace" && event.key !== "Delete") return;
      const [start, end] = currentSelection();
      const ref = draftPluginReferences(live.current).find(item => start === end && (event.key === "Backspace" ? item.end === start : item.start === start));
      if (ref) {
        event.preventDefault();
        publish(live.current.slice(0, ref.start) + live.current.slice(ref.end), [ref.start, ref.start]);
      }
    }}
  />;
});
