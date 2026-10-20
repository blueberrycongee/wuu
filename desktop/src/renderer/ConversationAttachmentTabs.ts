import { useCallback, useState } from "react";
import type { ConversationAttachmentRequest } from "./ConversationAttachmentPreview";
import { closeViewTab, focusViewTab, initialWorkspaceViewTabsState, openViewTab, reorderViewTabs, workspaceConversationAttachmentTab } from "./WorkspaceViewTabs";

// Reuse tab identity/history mechanics, never Coding's state or saved layout setters.
export function useConversationAttachmentTabs() {
  const [state, setState] = useState(initialWorkspaceViewTabsState);
  const [open, setOpenState] = useState(false);
  const [manualGlobalized, setManualGlobalized] = useState(false);
  const [conversationSelected, setConversationSelected] = useState(false);
  const setOpen = useCallback((value: boolean) => {
    setOpenState(value);
    if (!value) { setManualGlobalized(false); setConversationSelected(false); }
  }, []);
  const openAttachment = useCallback((request: ConversationAttachmentRequest) => {
    setState(current => openViewTab(current, workspaceConversationAttachmentTab(request)));
    setConversationSelected(false); setOpenState(true);
  }, []);
  const focusTab = useCallback((id: string) => {
    setState(current => focusViewTab(current, id)); setConversationSelected(false);
  }, []);
  const closeTab = useCallback((id: string) => {
    setState(current => closeViewTab(current, id));
    if (state.tabs.length === 1 && state.tabs[0].id === id) setOpen(false);
  }, [state.tabs, setOpen]);
  const reorderTabs = useCallback((active: string, over: string) => setState(current => reorderViewTabs(current, active, over)), []);
  const focusConversation = useCallback(() => setConversationSelected(true), []);
  return { state, open, setOpen, manualGlobalized, setManualGlobalized, conversationSelected, openAttachment, focusTab, closeTab, reorderTabs, focusConversation };
}
