import { PointerSensor } from "@dnd-kit/core";
import type { PointerSensorOptions } from "@dnd-kit/core";
import type { PointerEvent as ReactPointerEvent } from "react";

export type SidebarPointerActivationEvent = {
  nativeEvent: {
    isPrimary: boolean;
    button: number;
    pointerType: string;
  };
};

/**
 * Sidebar reorder is mouse-only: touch and pen must keep native scrolling
 * instead of starting a drag. dnd-kit's default PointerSensor activates for
 * every pointer type, so we filter on the actual event's pointerType rather
 * than UA or viewport width.
 */
export function shouldStartSidebarReorder(
  event: SidebarPointerActivationEvent,
): boolean {
  const pointer = event.nativeEvent;
  return (
    pointer.isPrimary &&
    pointer.button === 0 &&
    pointer.pointerType === "mouse"
  );
}

export class SidebarPointerSensor extends PointerSensor {
  static activators = [
    {
      eventName: "onPointerDown" as const,
      handler: (
        event: ReactPointerEvent,
        options: PointerSensorOptions,
      ): boolean => {
        if (!shouldStartSidebarReorder(event)) {
          return false;
        }
        options.onActivation?.({ event: event.nativeEvent });
        return true;
      },
    },
  ];
}
