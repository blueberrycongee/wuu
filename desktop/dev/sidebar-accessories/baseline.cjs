// Main-branch appearance before the sidebar surface refinement, for visual review.
module.exports = `.sidebar-content {
  --sidebar-content-pad-x: var(--space-2) !important;
  --sidebar-row-radius: var(--radius-xs) !important;
  --sidebar-row-selected-bg: var(--wuu-nav-item-active-background, var(--wuu-color-surface-elevated, var(--paper))) !important;
  --sidebar-row-selected-shadow: var(--shadow-soft) !important;
  --sidebar-row-hover-bg: var(--wuu-nav-item-hover-background, var(--ink-overlay-4)) !important;
  --sidebar-label-ink: color-mix(in srgb, var(--ink) 92%, var(--paper)) !important;
}
html[data-theme="dark"] .sidebar-content {
  --sidebar-row-selected-bg: var(--wuu-nav-item-active-background, var(--wuu-color-surface-elevated, var(--gray-100))) !important;
  --sidebar-row-selected-shadow: 0 0 0 0 transparent !important;
}`;
