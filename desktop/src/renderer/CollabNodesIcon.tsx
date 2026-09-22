import type { SVGProps } from "react";
import { CollabNodes } from "./WuuIcons";

/** Equal peer nodes stay solid so their centers remain legible at row size. */
export function CollabNodesIcon(props: SVGProps<SVGSVGElement>): JSX.Element {
  return <CollabNodes {...props} />;
}
