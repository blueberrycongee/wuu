export function SidePanelToggleIcon({
  side,
  open,
  size
}: {
  side: "left" | "right";
  open: boolean;
  size?: number;
}): JSX.Element {
  const paneX = side === "left" ? 5 : 15;
  const dividerX = side === "left" ? 10 : 14;
  return (
    <svg
      className="side-panel-toggle-icon"
      data-open={open}
      width={size}
      height={size}
      style={{ width: size ?? "var(--icon-size)", height: size ?? "var(--icon-size)" }}
      viewBox="0 0 24 24"
      aria-hidden="true"
      focusable="false"
    >
      <rect className="side-panel-toggle-frame" x="3" y="3" width="18" height="18" rx="3.5" />
      <path className="side-panel-toggle-divider" d={`M${dividerX} 3.8v16.4`} />
      <rect className="side-panel-toggle-pane" x={paneX} y="5" width="4" height="14" rx="1" />
    </svg>
  );
}
