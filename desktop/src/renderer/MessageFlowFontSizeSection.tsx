import { type ChangeEvent, type KeyboardEvent, useEffect, useState } from "react";
import {
  MESSAGE_FLOW_FONT_SIZE_RANGE,
  type MessageFlowFontSize,
} from "../shared/protocol";
import { useI18n } from "./i18n";

const { min, max, step, default: defaultSize } = MESSAGE_FLOW_FONT_SIZE_RANGE;

function clampSize(value: unknown): MessageFlowFontSize {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    return defaultSize;
  }
  return Math.min(max, Math.max(min, value));
}

function snapSize(value: unknown): MessageFlowFontSize {
  const clamped = clampSize(value);
  return Math.round(clamped / step) * step;
}

function formatSize(size: number): string {
  return Number.isInteger(size) ? String(size) : size.toFixed(1);
}

/**
 * Stamp --conversation-message-font-size on <html> from the chosen px
 * value. The inline style on the document element wins over the
 * `:root` declaration in conversation-shell.css so the change cascades
 * through UI text and message-flow surfaces. The persisted field and IPC
 * names remain unchanged so existing reading preferences survive upgrades. Mirrors the applyThemePreference
 * shape — caller-friendly side-effect helper.
 */
export function applyMessageFlowFontSize(size: MessageFlowFontSize): void {
  const clamped = clampSize(size);
  try {
    document.documentElement.style.setProperty(
      "--conversation-message-font-size",
      `${clamped}px`,
    );
    window.dispatchEvent(new Event("wuu-content-size-change"));
  } catch {
    // Same fall-back story as Theme.ts — losing the inline stamp only
    // costs a one-frame flash at the default size.
  }
}

/**
 * Settings row body for the user-facing message-stream reading size:
 * a compact numeric field. Reads and persists through `window.wuu`
 * directly and applies the choice on commit (blur / Enter), so an
 * in-progress edit does not spam persistence or clamp mid-keystroke.
 */
export function MessageFlowFontSizeControl(): JSX.Element {
  const { t } = useI18n();
  const [size, setSize] = useState<MessageFlowFontSize>(() =>
    clampSize(window.wuu?.initialMessageFlowFontSize),
  );
  const [draft, setDraft] = useState(() => formatSize(size));

  useEffect(() => {
    let cancelled = false;
    void window.wuu
      ?.getMessageFlowFontSize?.()
      .then((stored) => {
        if (cancelled) {
          return;
        }
        const next = clampSize(stored);
        setSize(next);
        setDraft(formatSize(next));
      })
      .catch(() => {
        // Keep the preload-provided initial value.
      });
    return () => {
      cancelled = true;
    };
  }, []);

  function persist(next: MessageFlowFontSize): void {
    setSize(next);
    applyMessageFlowFontSize(next);
    void window.wuu?.setMessageFlowFontSize?.(next).catch(() => {
      // Persistence failure leaves the applied size for this window;
      // the next launch falls back to the stored value.
    });
  }

  function commit(raw: string): void {
    const parsed = Number.parseFloat(raw.trim());
    const next = snapSize(Number.isFinite(parsed) ? parsed : size);
    setDraft(formatSize(next));
    persist(next);
  }

  function onChange(event: ChangeEvent<HTMLInputElement>): void {
    const raw = event.currentTarget.value;
    setDraft(raw);
    const parsed = Number.parseFloat(raw.trim());
    // Apply complete in-range values immediately so the native stepper
    // updates the conversation without waiting for blur. Incomplete
    // keystrokes ("1" while typing "16") and off-grid drafts stay in
    // the field until commit.
    if (Number.isFinite(parsed) && parsed >= min && parsed <= max && parsed === snapSize(parsed)) {
      persist(parsed);
    }
  }

  function onKeyDown(event: KeyboardEvent<HTMLInputElement>): void {
    if (event.key === "Enter") {
      event.currentTarget.blur();
    }
  }

  return (
    <input
      className="settings-input settings-input-num settings-input-num-center"
      type="number"
      min={min}
      max={max}
      step={step}
      value={draft}
      onChange={onChange}
      onBlur={(event) => commit(event.currentTarget.value)}
      onKeyDown={onKeyDown}
      aria-label={t("settings.uiSize")}
      data-testid="settings-message-flow-font-size-input"
    />
  );
}
