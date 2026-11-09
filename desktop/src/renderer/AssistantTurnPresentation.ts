import { useEffect, useLayoutEffect, useRef, useState } from "react";
import type { ThreadItem } from "../shared/protocol";
import type { AssistantTurnDisplay, TurnEntry } from "./AssistantTurnDisplay";
import {
  useConversationBecameRenderActive,
  useConversationRenderActive,
  useConversationRevealSnap,
} from "./ConversationRenderActivity";

export const ASSISTANT_TURN_PRESENTATION_STABILIZE_MS = 120;

export function useAssistantTurnPresentation(
  turnID: string,
  display: AssistantTurnDisplay | undefined,
): AssistantTurnDisplay | undefined {
  const renderActive = useConversationRenderActive();
  const becameRenderActive = useConversationBecameRenderActive();
  const revealSnap = useConversationRevealSnap();
  const [presented, setPresented] = useState(display);
  const turnIDRef = useRef(turnID);
  // The display object whose entries produced the presented signatures. Item
  // references survive unchanged publications, so an all-identical input pass
  // proves the signatures unchanged without rebuilding them per publication.
  const presentedDisplayRef = useRef(display);
  const presentedStructureRef = useRef(displayStructureSignature(display));
  const presentedContentRef = useRef(displayContentSignature(display));
  const presentedStreamingRef = useRef(displayHasStreamingEntry(display));
  const pendingDisplayRef = useRef(display);
  const pendingStructureRef = useRef(presentedStructureRef.current);
  const pendingContentRef = useRef(presentedContentRef.current);
  const timerRef = useRef<number | undefined>(undefined);

  const clearPending = (): void => {
    if (timerRef.current !== undefined) {
      window.clearTimeout(timerRef.current);
      timerRef.current = undefined;
    }
  };

  const publish = (nextDisplay: AssistantTurnDisplay | undefined): void => {
    presentedDisplayRef.current = nextDisplay;
    presentedStructureRef.current = displayStructureSignature(nextDisplay);
    presentedContentRef.current = displayContentSignature(nextDisplay);
    presentedStreamingRef.current = displayHasStreamingEntry(nextDisplay);
    pendingDisplayRef.current = nextDisplay;
    pendingStructureRef.current = presentedStructureRef.current;
    pendingContentRef.current = presentedContentRef.current;
    setPresented(nextDisplay);
  };

  // Publish during render so the scroll restore that follows this commit
  // measures the live structure. A layout-effect publish lands one flush
  // later, and the viewport has already anchored to the frozen height.
  if (renderActive && (revealSnap || becameRenderActive) && display && !displaySignatureInputsEqual(presentedDisplayRef.current, display)) {
    const nextStructure = displayStructureSignature(display);
    const nextContent = displayContentSignature(display);
    if (
      nextStructure !== presentedStructureRef.current ||
      nextContent !== presentedContentRef.current
    ) {
      clearPending();
      presentedDisplayRef.current = display;
      presentedStructureRef.current = nextStructure;
      presentedContentRef.current = nextContent;
      presentedStreamingRef.current = displayHasStreamingEntry(display);
      pendingDisplayRef.current = display;
      pendingStructureRef.current = nextStructure;
      pendingContentRef.current = nextContent;
      setPresented(display);
    }
  }

  useLayoutEffect(() => {
    if (turnIDRef.current !== turnID || !display) {
      turnIDRef.current = turnID;
      clearPending();
      publish(display);
      return;
    }

    // Hidden cached panes must freeze without publishing. Parent turns rebuild
    // `display` every render; publishing that object setStates TurnContent in
    // a loop and whitescreens the app with "Maximum update depth exceeded".
    if (!renderActive) {
      clearPending();
      return;
    }

    // Identical signature inputs prove the signatures unchanged — skip the
    // O(turn text) comparison-string rebuild on unchanged publications.
    if (displaySignatureInputsEqual(presentedDisplayRef.current, display)) {
      clearPending();
      return;
    }

    const nextStructure = displayStructureSignature(display);
    const nextContent = displayContentSignature(display);
    // The first visible commit after a session switch must publish the live
    // structure immediately, or the 120ms buffer paints a stale process row
    // and then jumps when the aggregated toolcall / mascot layout finally lands.
    if (becameRenderActive) {
      clearPending();
      if (
        nextStructure !== presentedStructureRef.current ||
        nextContent !== presentedContentRef.current
      ) {
        publish(display);
      }
      return;
    }

    if (nextStructure === presentedStructureRef.current) {
      clearPending();
      if (nextContent !== presentedContentRef.current) {
        publish(display);
      }
      return;
    }

    // A completed stream is a presentation boundary, not transient structure.
    // Publish its final snapshot before a queued turn can add the next user
    // message below it. Keeping this transition in the stabilization window
    // makes the new turn appear while the previous final text is still stale.
    if (presentedStreamingRef.current && !displayHasStreamingEntry(display)) {
      clearPending();
      publish(display);
      return;
    }

    // A completed narration followed by a live tool/reasoning item is a
    // hand-off within the same process surface. Waiting for the structural
    // debounce here leaves the old layout on screen for one paint; the
    // ResizeObserver then re-anchors the outer conversation when the new row
    // finally mounts, which is visible as a one-frame up/down scroll wobble.
    // Publish this active process continuation in the current layout commit.
    if (
      !presented?.hasAnswer &&
      !display.hasAnswer &&
      !presentedStreamingRef.current &&
      displayHasStreamingEntry(display)
    ) {
      clearPending();
      publish(display);
      return;
    }

    pendingDisplayRef.current = display;
    pendingStructureRef.current = nextStructure;
    pendingContentRef.current = nextContent;
    if (timerRef.current !== undefined) {
      return;
    }
    timerRef.current = window.setTimeout(() => {
      timerRef.current = undefined;
      presentedDisplayRef.current = pendingDisplayRef.current;
      presentedStructureRef.current = pendingStructureRef.current;
      presentedContentRef.current = pendingContentRef.current;
      presentedStreamingRef.current = displayHasStreamingEntry(
        pendingDisplayRef.current,
      );
      setPresented(pendingDisplayRef.current);
    }, ASSISTANT_TURN_PRESENTATION_STABILIZE_MS);
  }, [becameRenderActive, display, renderActive, turnID]);

  useEffect(() => {
    return () => clearPending();
  }, []);

  return presented;
}

function displayHasStreamingEntry(
  display: AssistantTurnDisplay | undefined,
): boolean {
  return display?.entries.some((entry) => entry.streaming) ?? false;
}

// Reference-level equality over every value the signatures read. Equal inputs
// guarantee equal signatures; unequal inputs fall back to computing them, so
// this never changes outcomes — it only skips throwaway string building.
function displaySignatureInputsEqual(
  previous: AssistantTurnDisplay | undefined,
  next: AssistantTurnDisplay | undefined,
): boolean {
  if (previous === next) return true;
  if (!previous || !next) return false;
  if (previous.hasAnswer !== next.hasAnswer) return false;
  const prevPreview = previous.latestProcessPreview;
  const nextPreview = next.latestProcessPreview;
  if (
    prevPreview !== nextPreview &&
    (prevPreview?.kind !== nextPreview?.kind || prevPreview?.text !== nextPreview?.text)
  ) {
    return false;
  }
  if (previous.entries.length !== next.entries.length) return false;
  for (let i = 0; i < previous.entries.length; i += 1) {
    if (!entrySignatureInputsEqual(previous.entries[i], next.entries[i])) return false;
  }
  return true;
}

function entrySignatureInputsEqual(previous: TurnEntry, next: TurnEntry): boolean {
  if (previous === next) return true;
  if (
    previous.key !== next.key ||
    previous.position !== next.position ||
    previous.kind !== next.kind ||
    previous.settled !== next.settled ||
    previous.streaming !== next.streaming ||
    previous.count !== next.count
  ) {
    return false;
  }
  const previousItems = previous.items ?? (previous.item ? [previous.item] : []);
  const nextItems = next.items ?? (next.item ? [next.item] : []);
  if (previousItems.length !== nextItems.length) return false;
  for (let i = 0; i < previousItems.length; i += 1) {
    if (previousItems[i] !== nextItems[i]) return false;
  }
  return true;
}

function displayStructureSignature(
  display: AssistantTurnDisplay | undefined,
): string {
  if (!display) {
    return "none";
  }
  return [
    display.hasAnswer ? "answer" : "no-answer",
    display.latestProcessPreview?.kind ?? "no-preview",
    display.entries.map(entryStructureSignature).join("|"),
  ].join(";");
}

function entryStructureSignature(entry: TurnEntry): string {
  const items = entry.items ?? [entry.item];
  return [
    entry.key,
    entry.position,
    entry.kind,
    entry.settled ? "settled" : "live",
    entry.streaming ? "streaming" : "idle",
    entry.count ?? "",
    items.map(itemStructureSignature).join(","),
  ].join(":");
}

function itemStructureSignature(item: ThreadItem): string {
  const terminal = item.type === "agent_message" ? String(item.terminal ?? false) : "";
  return [item.id, item.type, item.status, terminal].join("/");
}

function displayContentSignature(
  display: AssistantTurnDisplay | undefined,
): string {
  if (!display) {
    return "none";
  }
  return [
    displayStructureSignature(display),
    display.latestProcessPreview?.text ?? "",
    display.entries.map(entryContentSignature).join("|"),
  ].join(";");
}

function entryContentSignature(entry: TurnEntry): string {
  const items = entry.items ?? [entry.item];
  return items.map(itemContentSignature).join(",");
}

function itemContentSignature(item: ThreadItem): string {
  return "text" in item && typeof item.text === "string" ? item.text : "";
}
