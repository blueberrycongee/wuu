import { forwardRef } from "react";
import type { IconProps } from "./WuuIcons";

/** Raster identities keep the same sizing and accessibility contract as SVG marks. */
export function createArtworkIcon(artwork: string, name: string) {
  return forwardRef<SVGSVGElement, IconProps>(function ArtworkIcon({ size = 24, className = "", children, ...props }, ref) {
    return (
      <svg ref={ref} width={size} height={size} viewBox="0 0 100 100" fill="none"
        className={`plugin-icon-asset ${className}`}
        aria-hidden={props["aria-label"] || props["aria-labelledby"] || children ? undefined : true}
        focusable="false" data-icon={name} {...props}>
        <image href={artwork} width="100" height="100" />
        {children}
      </svg>
    );
  });
}
