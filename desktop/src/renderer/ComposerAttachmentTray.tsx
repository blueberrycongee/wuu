import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { AttachmentImage } from "./AttachmentImage";
import { fileNameParts, formatFileSize } from "./AttachmentFormat";
import {
  CollapsedComposerPromptCard,
  type CollapsedComposerPromptBlock
} from "./ComposerCollapsedPrompt";
import { ComposerDocumentCard } from "./ComposerDocumentCard";
import { isComposerImagePending, type ComposerFile, type ComposerImage } from "./ComposerMessages";
import { useOptionalImagePreview } from "./ImagePreview";
import { useI18n } from "./i18n";
import { motionDurationMs, prefersReducedMotion } from "./motion";
import { formatVideoDuration, useVideoObjectURL } from "./VideoAttachment";
import { FileText, Film, X } from "./WuuIcons";

type TrayCard =
  | { key: string; kind: "image"; image: ComposerImage; number: number }
  | { key: string; kind: "file"; file: ComposerFile; number: number }
  | { key: string; kind: "text"; block: CollapsedComposerPromptBlock; index: number };

type ExitingCard = { card: TrayCard; index: number };
type Point = { left: number; top: number };
type Lift = { stack: Animation; frame: Animation; closing: boolean };

function trayCards(
  images: ComposerImage[],
  files: ComposerFile[],
  pastedTexts: CollapsedComposerPromptBlock[],
): TrayCard[] {
  return [
    ...images.map((image, index) => ({ key: `image:${image.id}`, kind: "image" as const, image, number: index + 1 })),
    ...files.map((file, index) => ({ key: `file:${file.id}`, kind: "file" as const, file, number: index + 1 })),
    ...pastedTexts.map((block, index) => ({ key: `text:${block.id}`, kind: "text" as const, block, index })),
  ];
}

function translateOffset(element: HTMLElement, axis: 0 | 1): number {
  const value = getComputedStyle(element).translate;
  if (!value || value === "none") return 0;
  return Number.parseFloat(value.split(" ")[axis] ?? "0") || 0;
}

/**
 * Attachments and folded pastes wait in a tray that slides out from behind
 * the input's top edge, so adding one never changes the input's own size.
 *
 * Every animation runs on the compositor (opacity, scale, translate): a paste
 * is exactly when the main thread is busy encoding, and layout-driven motion
 * would stall with it. Opening the tray changes layout once; the stack is
 * translated back by the new height and the frame counter-translated, so the
 * content above rises smoothly while the input stays put. Cards removed
 * through the tray leave in place while their neighbours glide over (FLIP);
 * anything else — sending, a draft swap — disappears at once.
 *
 * The tray must render immediately before `.composer-frame` in its
 * `.composer-stack`, and it tucks behind that frame's top edge.
 */
export function ComposerAttachmentTray({
  images,
  files,
  pastedTexts,
  resetKey,
  onRemoveImage,
  onRemoveFile,
  onRevealText,
  onRemoveText,
}: {
  images: ComposerImage[];
  files: ComposerFile[];
  pastedTexts: CollapsedComposerPromptBlock[];
  /** Changing identity (a session or draft swap) skips all motion. */
  resetKey?: string;
  onRemoveImage: (id: string) => void;
  onRemoveFile: (id: string) => void;
  onRevealText: (index: number) => void;
  onRemoveText: (index: number) => void;
}): JSX.Element | null {
  const { t } = useI18n();
  const cards = trayCards(images, files, pastedTexts);
  const signature = cards.map((card) => card.key).join("\n");
  const trayRef = useRef<HTMLDivElement>(null);
  const listRef = useRef<HTMLUListElement>(null);
  const committedCards = useRef<TrayCard[]>(cards);
  const dismissed = useRef(new Set<string>());
  const offsets = useRef(new Map<string, Point>());
  const flips = useRef(new Map<string, Animation>());
  const exitAnimations = useRef(new Map<string, Animation>());
  const lift = useRef<Lift | null>(null);
  const committed = useRef<{ resetKey?: string; open: boolean } | null>(null);
  const [exiting, setExiting] = useState<ExitingCard[]>([]);
  const [seen, setSeen] = useState({ signature, resetKey });

  // Derive leaving cards during render, so a dismissed card keeps its DOM
  // node (a revoked blob preview would otherwise flash broken on remount).
  let visibleExiting = exiting;
  if (seen.signature !== signature || seen.resetKey !== resetKey) {
    const present = new Set(cards.map((card) => card.key));
    visibleExiting = seen.resetKey !== resetKey ? [] : [
      ...exiting.filter(({ card }) => !present.has(card.key)),
      ...committedCards.current.flatMap((card, index) =>
        !present.has(card.key) && dismissed.current.has(card.key) &&
          !exiting.some((entry) => entry.card.key === card.key)
          ? [{ card, index }]
          : []),
    ];
    setSeen({ signature, resetKey });
    setExiting(visibleExiting);
  }
  const rendered: Array<{ card: TrayCard; exiting: boolean }> = cards.map((card) => ({ card, exiting: false }));
  for (const { card, index } of [...visibleExiting].sort((a, b) => a.index - b.index)) {
    rendered.splice(Math.min(index, rendered.length), 0, { card, exiting: true });
  }
  const open = rendered.length > 0;

  useLayoutEffect(() => {
    committedCards.current = cards;
  });

  useLayoutEffect(() => {
    const previous = committed.current;
    committed.current = { resetKey, open };
    const reset = previous === null || previous.resetKey !== resetKey;
    if (reset) dismissed.current.clear();
    for (const key of dismissed.current) {
      if (!cards.some((card) => card.key === key)) dismissed.current.delete(key);
    }
    const slow = motionDurationMs("--motion-slow", 280);
    const base = motionDurationMs("--motion-base", 180);
    const rootStyle = getComputedStyle(document.documentElement);
    const easeOut = rootStyle.getPropertyValue("--ease-out").trim() || "cubic-bezier(0.16, 1, 0.3, 1)";
    const easeIn = rootStyle.getPropertyValue("--ease-in").trim() || "cubic-bezier(0.4, 0, 1, 1)";
    const tray = trayRef.current;
    const list = listRef.current;
    const animate = !reset && !document.hidden && !prefersReducedMotion() && slow > 0 &&
      typeof tray?.animate === "function";

    const settleLift = (): void => {
      lift.current?.stack.cancel();
      lift.current?.frame.cancel();
      lift.current = null;
    };
    // Moves everything above the frame by `to` while the frame holds still.
    const liftTo = (to: number, closing: boolean, duration: number, easing: string): void => {
      const stack = tray?.closest<HTMLElement>(".composer-stack");
      const frame = tray?.nextElementSibling;
      if (!stack || !(frame instanceof HTMLElement)) return;
      const from = lift.current ? translateOffset(stack, 1) : closing ? 0 : to;
      settleLift();
      const target = closing ? to : 0;
      const timing: KeyframeAnimationOptions = { duration, easing, fill: closing ? "forwards" : "none" };
      lift.current = {
        stack: stack.animate([{ translate: `0 ${from}px` }, { translate: `0 ${target}px` }], timing),
        frame: frame.animate([{ translate: `0 ${-from}px` }, { translate: `0 ${-target}px` }], timing),
        closing,
      };
    };

    if (!open || !tray || !list) {
      settleLift();
      offsets.current = new Map();
      return;
    }
    const trayHeight = tray.offsetHeight + Number.parseFloat(getComputedStyle(tray).marginBottom || "0");
    const presentCount = rendered.filter((entry) => !entry.exiting).length;
    if (!animate) {
      settleLift();
    } else if (!previous?.open) {
      liftTo(trayHeight, false, slow, easeOut);
    } else if (presentCount === 0 && !lift.current?.closing) {
      liftTo(trayHeight, true, base, easeIn);
    } else if (presentCount > 0 && lift.current?.closing) {
      liftTo(trayHeight, false, slow, easeOut);
    }

    const before = offsets.current;
    const next = new Map<string, Point>();
    const elements = Array.from(list.children) as HTMLElement[];
    // Take leaving cards out of flow first, so the rest measure where they go.
    for (const element of elements) {
      const key = element.dataset.key!;
      if (element.dataset.exiting === undefined) continue;
      const point = before.get(key);
      if (point) {
        next.set(key, point);
        element.style.left = `${point.left}px`;
        element.style.top = `${point.top}px`;
      }
      if (exitAnimations.current.has(key)) continue;
      const finish = (): void => {
        exitAnimations.current.delete(key);
        flips.current.delete(key);
        setExiting((current) => current.filter((entry) => entry.card.key !== key));
      };
      if (!animate || !point) {
        finish();
        continue;
      }
      flips.current.get(key)?.cancel();
      const exit = element.animate(
        [{ opacity: 1, scale: "1" }, { opacity: 0, scale: "0.9" }],
        { duration: base, easing: easeIn, fill: "forwards" },
      );
      exitAnimations.current.set(key, exit);
      // Unmounting the last card shrinks the stack; wait for the lift to
      // arrive there so the layout change lands on the frame it matches. A
      // reopening cancels that lift, which must not strand this card.
      const closing = lift.current?.closing ? lift.current.stack.finished.catch(() => undefined) : undefined;
      void Promise.all([exit.finished, closing]).then(finish, () => {});
    }
    let newest: HTMLElement | undefined;
    for (const element of elements) {
      const key = element.dataset.key!;
      if (element.dataset.exiting !== undefined) continue;
      const revived = exitAnimations.current.get(key);
      if (revived) {
        exitAnimations.current.delete(key);
        revived.cancel();
        element.style.removeProperty("left");
        element.style.removeProperty("top");
      }
      const point = { left: element.offsetLeft, top: element.offsetTop };
      next.set(key, point);
      if (!animate) continue;
      const origin = before.get(key);
      if (!origin) {
        newest = element;
        element.animate(
          [{ opacity: 0, scale: "0.9" }, { opacity: 1, scale: "1" }],
          { duration: slow, easing: easeOut },
        );
        continue;
      }
      if (origin.left === point.left && origin.top === point.top) continue;
      // Retargeting mid-glide starts from where the card is drawn, not from
      // its last layout slot.
      const running = flips.current.get(key);
      const active = running?.playState === "running";
      const dx = origin.left - point.left + (active ? translateOffset(element, 0) : 0);
      const dy = origin.top - point.top + (active ? translateOffset(element, 1) : 0);
      running?.cancel();
      flips.current.set(key, element.animate(
        [{ translate: `${dx}px ${dy}px` }, { translate: "0 0" }],
        { duration: slow, easing: easeOut },
      ));
    }
    offsets.current = next;
    if (newest) {
      // Clear the edge fade too, so the new card is not revealed half-masked.
      const margin = Number.parseFloat(getComputedStyle(list).getPropertyValue("--scroll-fade-size")) || 0;
      const start = newest.offsetLeft - margin;
      const end = newest.offsetLeft + newest.offsetWidth + margin;
      const left = end > list.scrollLeft + list.clientWidth
        ? end - list.clientWidth
        : start < list.scrollLeft ? start : undefined;
      if (left !== undefined) list.scrollTo({ left, behavior: "smooth" });
    }
  }, [signature, resetKey, exiting]);

  useEffect(() => () => {
    lift.current?.stack.cancel();
    lift.current?.frame.cancel();
  }, []);

  useEffect(() => {
    const list = listRef.current;
    if (!open || !list) return;
    // A mouse wheel only reports vertical deltas; trackpads scroll natively.
    const onWheel = (event: WheelEvent): void => {
      if (event.ctrlKey || Math.abs(event.deltaX) >= Math.abs(event.deltaY)) return;
      if (list.scrollWidth <= list.clientWidth) return;
      event.preventDefault();
      list.scrollLeft += event.deltaY;
    };
    list.addEventListener("wheel", onWheel, { passive: false });
    return () => list.removeEventListener("wheel", onWheel);
  }, [open]);

  if (!open) return null;

  function dismiss(key: string, action: () => void): void {
    dismissed.current.add(key);
    action();
  }

  return (
    <div className="composer-attachment-tray" ref={trayRef} data-wuu-component="composer-attachments">
      <ul className="composer-attachment-tray-list scrollbar-hidden" ref={listRef} data-scroll-fade="inline" aria-label={t("composer.attachments")}>
        {rendered.map(({ card, exiting: leaving }) => (
          <li
            className="composer-attachment-tray-item"
            key={card.key}
            data-key={card.key}
            data-exiting={leaving ? "" : undefined}
            aria-hidden={leaving || undefined}
            inert={leaving}
          >
            {card.kind === "image" ? (
              <ComposerImageCard
                image={card.image}
                number={card.number}
                previewDisabled={leaving}
                onRemove={() => dismiss(card.key, () => onRemoveImage(card.image.id))}
              />
            ) : card.kind === "file" ? (
              card.file.media_type.startsWith("video/") ? (
                <ComposerVideoCard
                  file={card.file}
                  number={card.number}
                  onRemove={() => dismiss(card.key, () => onRemoveFile(card.file.id))}
                />
              ) : (
                <ComposerFileCard
                  file={card.file}
                  number={card.number}
                  onRemove={() => dismiss(card.key, () => onRemoveFile(card.file.id))}
                />
              )
            ) : (
              <CollapsedComposerPromptCard
                text={card.block.text}
                onReveal={() => dismiss(card.key, () => onRevealText(card.index))}
                onRemove={() => dismiss(card.key, () => onRemoveText(card.index))}
              />
            )}
          </li>
        ))}
      </ul>
    </div>
  );
}

function ComposerImageCard({
  image,
  number,
  previewDisabled,
  onRemove,
}: {
  image: ComposerImage;
  number: number;
  previewDisabled: boolean;
  onRemove: () => void;
}): JSX.Element {
  const { t } = useI18n();
  const imagePreview = useOptionalImagePreview();
  const label = t("composer.imageNumber", { number });
  return (
    <div className="composer-attachment-card composer-media-card" data-pending={isComposerImagePending(image) || undefined}>
      <AttachmentImage
        image={image}
        label={label}
        className="composer-media-card-image"
        decoding="async"
        previewDisabled={previewDisabled}
        onOpen={(src, origin) => imagePreview?.openPreview({ src, alt: label, title: label }, origin)}
      />
      <button
        className="composer-attachment-card-remove"
        type="button"
        aria-label={t("composer.removeImage", { number })}
        onClick={onRemove}
      >
        <X aria-hidden="true" />
      </button>
    </div>
  );
}

function ComposerVideoCard({
  file,
  number,
  onRemove,
}: {
  file: ComposerFile;
  number: number;
  onRemove: () => void;
}): JSX.Element {
  const { t } = useI18n();
  const src = useVideoObjectURL(file);
  const videoRef = useRef<HTMLVideoElement>(null);
  const [duration, setDuration] = useState(0);
  const [playing, setPlaying] = useState(false);
  const filename = file.filename?.trim() || "video";
  return (
    <div className="composer-attachment-card composer-media-card composer-video-card">
      <button
        className="composer-video-card-main"
        type="button"
        title={filename}
        aria-label={t("composer.attachment.playVideo", { name: filename })}
        aria-pressed={playing}
        disabled={!src}
        onClick={() => {
          const video = videoRef.current;
          if (!video) return;
          if (video.paused) void video.play().catch(() => {});
          else video.pause();
        }}
      >
        {src ? (
          <video
            ref={videoRef}
            src={src}
            preload="metadata"
            playsInline
            onLoadedMetadata={(event) => setDuration(event.currentTarget.duration)}
            onPlay={() => setPlaying(true)}
            onPause={() => setPlaying(false)}
          />
        ) : null}
        <span className="composer-video-card-badge">
          <Film aria-hidden="true" />
          {duration > 0 && Number.isFinite(duration) ? formatVideoDuration(duration) : formatFileSize(file.data)}
        </span>
      </button>
      <button
        className="composer-attachment-card-remove"
        type="button"
        aria-label={t("composer.removeFile", { number })}
        onClick={onRemove}
      >
        <X aria-hidden="true" />
      </button>
    </div>
  );
}

function ComposerFileCard({
  file,
  number,
  onRemove,
}: {
  file: ComposerFile;
  number: number;
  onRemove: () => void;
}): JSX.Element {
  const { t } = useI18n();
  const filename = file.filename?.trim() || t("composer.pdfNumber", { number });
  const { stem, extension } = fileNameParts(filename);
  const kind = extension ? extension.slice(1).toUpperCase() : file.media_type.split("/").pop()?.toUpperCase();
  return (
    <ComposerDocumentCard
      className="composer-file-card"
      icon={<FileText className="icon" />}
      title={
        <strong className="composer-document-card-title composer-file-card-name" title={filename}>
          <span className="composer-file-card-stem">{stem}</span>
          {extension ? <span className="composer-file-card-extension">{extension}</span> : null}
        </strong>
      }
      meta={`${kind ? `${kind} · ` : ""}${formatFileSize(file.data)}`}
      removeLabel={t("composer.removeFile", { number })}
      onRemove={onRemove}
    />
  );
}
