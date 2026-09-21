import {
  type RefObject,
  type ReactNode,
  useCallback,
  useEffect,
  useState,
} from "react";
import {
  atLatestScrollView,
  latestFollowScrollTop,
  observeAutoFollowResizeTargets,
  submitGlideActive,
  subscribeSubmitGlide,
} from "./AutoFollowScroll";
import { dockComposerVisualHeight } from "./ConversationScrollState";
import {
  createWindowResizeSettleScheduler,
  isWindowResizing,
} from "./WindowResizeState";
import { useI18n } from "./i18n";
import { UILayerPortal } from "./ui/layers/UILayerHost";

/**
 * Self-contained "scroll to bottom" pill. It watches the same container it
 * scrolls, then portals to document.body and stays measured above the dock
 * composer so nested overflow and paint containment cannot offset it.
 */
type JumpToLatestPillProps = {
  /**
   * Ref to the scroll container this pill watches and smooth-scrolls to
   * bottom on click.
   */
  containerRef: RefObject<HTMLElement | null>;
  /**
   * Element the pill should float just above (the dock composer). It may be
   * transiently null before the composer mounts.
   */
  bottomAnchor: HTMLElement | null;
  /**
   * Distance (in px) from the bottom of the scroll container below which the
   * pill is considered "scrolled away" and shown. Default 80 matches the
   * existing auto-follow threshold.
   */
  threshold?: number;
  /**
   * Accessible label for the pill button. Defaults to the localized label.
   */
  label?: string;
  /**
   * Fires whenever the pill's "scrolled away from bottom" boolean flips.
   * Used by callers that need to coordinate other composer-adjacent chrome
   * (e.g. swap a sibling progress pill out of the same slot while the user
   * is mid-conversation). Pass a stable callback (e.g. a state setter) —
   * the effect re-runs only when the boolean actually changes.
   */
  onScrolledAwayChange?: (scrolledAway: boolean) => void;
  /** In-flow status groups reserve space instead of covering conversation content. */
  inline?: boolean;
  /** Remains available at the bottom; shares one centered group with the jump action. */
  companion?: ReactNode;
};

const DEFAULT_THRESHOLD_PX = 80;
const PILL_BOTTOM_GAP_PX = 8;
const COMPOSER_FRAME_SELECTOR = ".composer-frame";

type PillPosition = { left: number; bottom: number };

function horizontalAnchorCenter(
  container: HTMLElement,
  bottomAnchor: HTMLElement,
): number {
  const composerFrame =
    bottomAnchor.querySelector<HTMLElement>(COMPOSER_FRAME_SELECTOR);
  const anchorRect = (composerFrame ?? container).getBoundingClientRect();
  return anchorRect.left + anchorRect.width / 2;
}

export function JumpToLatestPill({
  containerRef,
  bottomAnchor,
  threshold = DEFAULT_THRESHOLD_PX,
  label,
  onScrolledAwayChange,
  inline = false,
  companion,
}: JumpToLatestPillProps): React.ReactElement | null {
  const { t } = useI18n();
  const accessibleLabel = label ?? t("conversation.jumpToLatest");
  const [scrolledAway, setScrolledAway] = useState(false);
  const [position, setPosition] = useState<PillPosition | null>(null);

  // Mirror the scrolled-away boolean to the parent whenever it flips. The
  // parent uses this to swap a sibling progress pill out of the same
  // composer-adjacent slot — see App.tsx. Effect-based so we only fire on
  // real transitions, not on every scroll/resize tick.
  useEffect(() => {
    onScrolledAwayChange?.(scrolledAway);
  }, [scrolledAway, onScrolledAwayChange]);

  useEffect(() => {
    const node = containerRef.current;
    if (!node) {
      return undefined;
    }

    const update = (): void => {
      // Same skip as the turn rail: the glide already knows it is leaving the
      // bottom, and a per-frame scrollHeight read forces a layout.
      if (submitGlideActive()) return;
      setScrolledAway(!atLatestScrollView(node, threshold));
    };
    const resizeSettleUpdate = createWindowResizeSettleScheduler(update);
    const scheduleUpdate = (): void => {
      if (submitGlideActive()) return;
      if (isWindowResizing()) {
        resizeSettleUpdate.schedule();
        return;
      }
      update();
    };

    // Initial sync after mount (covers the case where the container is
    // already scrolled up at the time the pill mounts, e.g., when the
    // user navigated away and then re-entered the conversation).
    const unsubscribeGlide = subscribeSubmitGlide(scheduleUpdate);
    scheduleUpdate();
    node.addEventListener("scroll", scheduleUpdate, { passive: true });

    // Re-evaluate on container and content resize. Viewport changes mutate the
    // container's `clientHeight`, while expanding or collapsing a fold mutates
    // its child's height and therefore the container's `scrollHeight`.
    // Observing both prevents a transient fold layout from leaving the pill
    // stuck in its pre-settle state.
    // Guarded: test environments (jsdom) have no ResizeObserver, and the
    // pill degrades gracefully to scroll-event-only updates without it.
    const resizeObserver =
      typeof ResizeObserver !== "undefined"
        ? new ResizeObserver(scheduleUpdate)
        : undefined;
    if (resizeObserver) {
      observeAutoFollowResizeTargets(node, resizeObserver);
    }
    const childObserver =
      typeof MutationObserver !== "undefined"
        ? new MutationObserver(() => {
            if (resizeObserver) {
              observeAutoFollowResizeTargets(node, resizeObserver);
            }
            scheduleUpdate();
          })
        : undefined;
    childObserver?.observe(node, { childList: true });

    return () => {
      resizeSettleUpdate.cancel();
      unsubscribeGlide();
      node.removeEventListener("scroll", scheduleUpdate);
      childObserver?.disconnect();
      resizeObserver?.disconnect();
    };
  }, [containerRef, threshold]);

  const recomputePosition = useCallback((): void => {
    const container = containerRef.current;
    if (!container || !bottomAnchor) {
      return;
    }
    const anchorRect = bottomAnchor.getBoundingClientRect();
    const visualHeight = dockComposerVisualHeight(bottomAnchor);
    setPosition({
      // Follow the input's actual visual center. The environment panel reserves
      // room with right padding, which moves the composer without changing the
      // scroll container's bounding box.
      left: horizontalAnchorCenter(container, bottomAnchor),
      // Use the complete dock height so this shares the exact slot used by
      // the progress pill. In particular, queued-message drawers sit above
      // the frame and must move this pill up with them.
      bottom: Math.max(
        PILL_BOTTOM_GAP_PX,
        window.innerHeight - anchorRect.bottom + visualHeight + PILL_BOTTOM_GAP_PX,
      ),
    });
  }, [containerRef, bottomAnchor]);

  const recomputeHorizontalPosition = useCallback((): void => {
    const container = containerRef.current;
    if (!container || !bottomAnchor) {
      return;
    }
    const left = horizontalAnchorCenter(container, bottomAnchor);
    setPosition((current) => {
      if (!current || current.left === left) {
        return current;
      }
      return { ...current, left };
    });
  }, [containerRef, bottomAnchor]);

  // Measured positioning for the portaled pill. Recomputes on
  // scroll, window resize, and container/composer resize (typing grows the
  // composer, moving its top edge). The composer frame is observed separately
  // because expanded mode moves it upward without growing the outer anchor.
  useEffect(() => {
    if (inline || !scrolledAway || !bottomAnchor) {
      return undefined;
    }
    const resizeSettleRecompute =
      createWindowResizeSettleScheduler(recomputePosition);
    const recomputeWhenStable = (): void => {
      if (isWindowResizing()) {
        resizeSettleRecompute.schedule();
        return;
      }
      recomputePosition();
    };
    recomputeWhenStable();
    let frame = 0;
    const schedule = (): void => {
      if (isWindowResizing()) {
        resizeSettleRecompute.schedule();
      }
      if (frame) {
        return;
      }
      frame = window.requestAnimationFrame(() => {
        frame = 0;
        if (isWindowResizing()) {
          // Keep the pill centered with the CSS-driven composer during a live
          // window drag. Only read the container's horizontal bounds here;
          // visibility, composer height, and scroll metrics remain deferred
          // until layout settles so they cannot destabilize auto-follow.
          recomputeHorizontalPosition();
          resizeSettleRecompute.schedule();
          return;
        }
        recomputeWhenStable();
      });
    };
    const container = containerRef.current;
    container?.addEventListener("scroll", schedule, { passive: true });
    // Opening or closing the environment panel transitions padding-right on
    // the scroll region. Its border-box size does not change, so ResizeObserver
    // cannot see the composer's horizontal move; settle once the transition
    // finishes to keep the pill centered above the input.
    container?.addEventListener("transitionend", schedule);
    window.addEventListener("resize", schedule);
    const resizeObserver =
      typeof ResizeObserver !== "undefined"
        ? new ResizeObserver(schedule)
        : undefined;
    if (container) {
      resizeObserver?.observe(container);
    }
    const observeAnchorTargets = (): void => {
      resizeObserver?.observe(bottomAnchor);
      const visualAnchor =
        bottomAnchor.querySelector<HTMLElement>(COMPOSER_FRAME_SELECTOR);
      if (visualAnchor) {
        resizeObserver?.observe(visualAnchor);
      }
    };
    observeAnchorTargets();
    const anchorChildObserver =
      typeof MutationObserver !== "undefined"
        ? new MutationObserver(() => {
            observeAnchorTargets();
            schedule();
          })
        : undefined;
    anchorChildObserver?.observe(bottomAnchor, { childList: true, subtree: true });
    return () => {
      resizeSettleRecompute.cancel();
      if (frame) {
        window.cancelAnimationFrame(frame);
      }
      container?.removeEventListener("scroll", schedule);
      container?.removeEventListener("transitionend", schedule);
      window.removeEventListener("resize", schedule);
      anchorChildObserver?.disconnect();
      resizeObserver?.disconnect();
    };
  }, [
    bottomAnchor,
    inline,
    scrolledAway,
    recomputeHorizontalPosition,
    recomputePosition,
    containerRef,
  ]);

  if (!scrolledAway && !companion) {
    return null;
  }

  const scrollToBottom = (): void => {
    const node = containerRef.current;
    if (!node) {
      return;
    }
    node.scrollTo({ top: latestFollowScrollTop(node), behavior: "smooth" });
  };

  const pillBody = (
    <>
      <svg
        width="14"
        height="14"
        viewBox="0 0 14 14"
        fill="none"
        aria-hidden="true"
      >
        <path
          d="M7 1V11M7 11L3 7M7 11L11 7"
          stroke="currentColor"
          strokeWidth="1.5"
          strokeLinecap="round"
          strokeLinejoin="round"
        />
      </svg>
      <span>{accessibleLabel}</span>
    </>
  );

  if (inline) {
    return <div className="jump-to-latest-cluster jump-to-latest-cluster-inline">
      {scrolledAway ? <button type="button" className="jump-to-latest-pill" aria-label={accessibleLabel} onClick={scrollToBottom}>{pillBody}</button> : null}
      {companion}
    </div>;
  }

  if (!bottomAnchor || !position) {
    return null;
  }
  return (
    <UILayerPortal layer="navigation">
      <button
        type="button"
        className="jump-to-latest-pill jump-to-latest-pill-anchored"
        data-pip-obstacle="jump"
        data-wuu-component="jump-to-latest"
        data-wuu-layer="navigation"
        data-wuu-state="visible"
        aria-label={accessibleLabel}
        style={{
          left: `${position.left}px`,
          bottom: `${position.bottom}px`,
        }}
        onClick={scrollToBottom}
      >
        {pillBody}
      </button>
    </UILayerPortal>
  );
}
