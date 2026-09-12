import { useId } from "react";
import { _layout } from "blobatar";
import { superellipse } from "../../vendor/blobatar/src/shape";
import icon from "../../../assets/app-icon-source.json";
import { WUU_MASCOT_NAME, WUU_MASCOT_TRAITS } from "./wuu-mascot-spec";
import "./styles/wuu-icon-mascot.css";

const { body } = _layout(WUU_MASCOT_NAME, { traits: WUU_MASCOT_TRAITS });
const bodyPath = superellipse(body);
/** The approved icon composition, with a flat body and live eyes in-app. */
export function WuuIconMascot({ className, composing = false }: {
  className?: string;
  composing?: boolean;
}): JSX.Element {
  const id = useId();
  const clip = `${id}-clip`;
  return (
    <svg className={`wuu-icon-mascot ${className ?? ""}`} viewBox="0 0 1024 1024"
      aria-hidden="true" focusable="false" data-composing={composing || undefined}>
      <defs>
        <clipPath id={clip}><rect width="1024" height="1024" rx="224" /></clipPath>
      </defs>
      <g clipPath={`url(#${clip})`}>
        <rect width="1024" height="1024" fill={icon.background} />
        <g transform={`translate(${icon.bodyX} ${icon.bodyY}) scale(${icon.radius / body.rx}) translate(${-body.cx} ${-body.cy})`}>
          <path d={bodyPath} fill={icon.bodyColor} />
        </g>
        <g transform={`translate(${icon.bodyX + icon.radius * icon.faceX} ${icon.bodyY + icon.radius * icon.faceY}) rotate(${icon.tilt})`} fill={icon.eyeColor}>
          <g className="wuu-icon-gaze">
            {[-1, 1].map((side) => (
              <g key={side} transform={`translate(${side * icon.eyeGap / 2} 0)`}>
                <g className="wuu-icon-blink">
                  <rect x={-icon.eyeWidth / 2} y={-icon.eyeHeight / 2}
                    width={icon.eyeWidth} height={icon.eyeHeight} rx={icon.eyeWidth / 2} />
                </g>
              </g>
            ))}
          </g>
        </g>
        <g fill={icon.markColor} opacity={icon.markOpacity}>
          {icon.marks.map((mark, i) => {
            const angle = icon.fanAngle + (i - 1) * icon.fanSpread;
            const radians = angle * Math.PI / 180;
            const distance = icon.radius + icon.fanGap + mark.gap + mark.length / 2;
            return <rect key={i} x={-mark.length / 2} y={-mark.width / 2}
              width={mark.length} height={mark.width} rx={mark.width / 2}
              transform={`translate(${icon.bodyX + Math.cos(radians) * distance} ${icon.bodyY + Math.sin(radians) * distance}) rotate(${angle + mark.angle})`} />;
          })}
        </g>
      </g>
    </svg>
  );
}
