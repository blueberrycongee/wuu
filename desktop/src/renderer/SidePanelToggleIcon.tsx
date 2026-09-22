import { PanelLeft, PanelRight } from "./WuuIcons";

export function SidePanelToggleIcon({ side, open, size }: {
  side: "left" | "right";
  open: boolean;
  size?: number;
}): JSX.Element {
  const Icon = side === "left" ? PanelLeft : PanelRight;
  return <Icon className="side-panel-toggle-icon" data-open={open} size={size}
    style={{ width: size ?? "var(--icon-size)", height: size ?? "var(--icon-size)" }} />;
}
