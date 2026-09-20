/** Native assistant disclosures expose optional inspection content, not new output. */
export function conversationDisclosureHeight(viewport: HTMLElement | null | undefined): number {
  let height = 0;
  for (const details of viewport?.querySelectorAll<HTMLDetailsElement>("details") ?? []) {
    if (details.closest('[data-user-message-id], [aria-hidden="true"]') ||
      details.parentElement?.closest("details")) continue;
    const summary = details.querySelector<HTMLElement>(":scope > summary");
    if (!summary) continue;
    // Measure the animated outer box, not the full (possibly clipped) body.
    // Do not check `open`: a closing ::details-content can still occupy space.
    height += Math.max(0, details.getBoundingClientRect().height - summary.getBoundingClientRect().height);
  }
  return height;
}

export function eventTargetsConversationDisclosure(target: EventTarget | null): boolean {
  if (!(target instanceof Element)) return false;
  const summary = target.closest("summary");
  return Boolean(summary?.parentElement?.matches("details") && !summary.closest("[data-user-message-id]"));
}
