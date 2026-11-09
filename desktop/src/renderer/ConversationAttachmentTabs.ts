import { useCallback, useState } from "react";
import type { ConversationAttachmentRequest } from "./ConversationAttachmentPreview";

type AttachmentTab = ConversationAttachmentRequest & { id: string; title: string };
type AttachmentTabs = { tabs: AttachmentTab[]; activeTabID?: string; history: string[] };

/** Verified remote snapshots have a separate lifetime from editable workspace tabs. */
export function useConversationAttachmentTabs() {
  const [state, setState] = useState<AttachmentTabs>({ tabs: [], history: [] });
  const [open, setOpenState] = useState(false);
  const [manualGlobalized, setManualGlobalized] = useState(false);
  const [conversationSelected, setConversationSelected] = useState(false);
  const setOpen = useCallback((value: boolean) => {
    setOpenState(value);
    if (!value) { setManualGlobalized(false); setConversationSelected(false); }
  }, []);
  const openAttachment = useCallback((request: ConversationAttachmentRequest) => {
    const tab = { ...request, id: `conversation-attachment:${request.resourceId}`, title: request.name };
    setState(current => ({ tabs: current.tabs.some(row => row.id === tab.id) ? current.tabs.map(row => row.id === tab.id ? tab : row) : [...current.tabs, tab],
      activeTabID: tab.id, history: [...current.history.filter(id => id !== tab.id && id !== current.activeTabID), ...(current.activeTabID && current.activeTabID !== tab.id ? [current.activeTabID] : [])] }));
    setConversationSelected(false); setOpenState(true);
  }, []);
  const focusTab = useCallback((id: string) => {
    setState(current => !current.tabs.some(tab => tab.id === id) || current.activeTabID === id ? current : ({ ...current, activeTabID: id,
      history: [...current.history.filter(previous => previous !== id && previous !== current.activeTabID), ...(current.activeTabID ? [current.activeTabID] : [])] }));
    setConversationSelected(false);
  }, []);
  const closeTab = useCallback((id: string) => {
    setState(current => {
      const tabs = current.tabs.filter(tab => tab.id !== id), history = current.history.filter(previous => previous !== id);
      return { tabs, activeTabID: current.activeTabID === id ? history.at(-1) ?? tabs.at(-1)?.id : current.activeTabID, history };
    });
  }, []);
  const reorderTabs = useCallback((active: string, over: string) => setState(current => {
    const from = current.tabs.findIndex(tab => tab.id === active), to = current.tabs.findIndex(tab => tab.id === over);
    if (from < 0 || to < 0 || from === to) return current;
    const tabs = [...current.tabs]; tabs.splice(to, 0, tabs.splice(from, 1)[0]);
    return { ...current, tabs };
  }), []);
  const focusConversation = useCallback(() => setConversationSelected(true), []);
  return { state, open: open && state.tabs.length > 0, setOpen, manualGlobalized, setManualGlobalized, conversationSelected, openAttachment, focusTab, closeTab, reorderTabs, focusConversation };
}
