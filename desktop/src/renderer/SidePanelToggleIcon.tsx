import { PanelLeft, PanelRight } from "./WuuIcons";

export function SidePanelToggleIcon({ side, open, size }: {
  side: "left" | "right";
  open: boolean;
  size?: number;
}): JSX.Element {
  const Icon = side === "left" ? PanelLeft : PanelRight;
  return <Icon className="side-panel-toggle-icon" data-open={open} size={size}
    aria-hidden="true"
    style={size === undefined ? undefined : { width: size, height: size }}>
    <rect className="side-panel-toggle-pane" x={side === "left" ? 5 : 16.3}
      y={5.5} width={2.7} height={13} rx={1} />
  </Icon>;
}
