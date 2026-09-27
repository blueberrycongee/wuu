// Last measured open height of each disclosure. A turn skipped by
// content-visibility keeps the height it last rendered at, so its disclosures
// still contribute what they measured then.
const renderedDisclosureHeights = new WeakMap<HTMLDetailsElement, number>();

/** Native assistant disclosures expose optional inspection content, not new output. */
export function conversationDisclosureHeight(viewport: HTMLElement | null | undefined): number {
  let height = 0;
  for (const details of viewport?.querySelectorAll<HTMLDetailsElement>("details") ?? []) {
    if (details.closest('[data-user-message-id], [aria-hidden="true"]') ||
      details.parentElement?.closest("details")) continue;
    // Reading geometry inside a skipped turn would force the layout it skips.
    if (details.checkVisibility?.({ contentVisibilityAuto: true }) === false) {
      height += renderedDisclosureHeights.get(details) ?? 0;
      continue;
    }
    const summary = details.querySelector<HTMLElement>(":scope > summary");
    if (!summary) continue;
    // Measure the animated outer box, not the full (possibly clipped) body.
    // Do not check `open`: a closing ::details-content can still occupy space.
    const open = Math.max(0, details.getBoundingClientRect().height - summary.getBoundingClientRect().height);
    renderedDisclosureHeights.set(details, open);
    height += open;
  }
  return height;
}

export function eventTargetsConversationDisclosure(target: EventTarget | null): boolean {
  if (!(target instanceof Element)) return false;
  const summary = target.closest("summary");
  return Boolean(summary?.parentElement?.matches("details") && !summary.closest("[data-user-message-id]"));
}
