import {
  Camera,
  Images,
  Paperclip,
  ChevronLeft,
} from "./WuuIcons";
import {
  type ChangeEvent,
  type RefObject,
  useCallback,
  useEffect,
  useRef,
  useState,
} from "react";
import { FloatingMenuPortal, isInsideFloatingMenu } from "./ComposerFloatingMenu";
import { translateCurrent, useI18n } from "./i18n";
import type { ComposerVariant } from "./ComposerTypes";

export type ComposerCameraStatus =
  | "idle"
  | "starting"
  | "active"
  | "paused"
  | "unsupported"
  | "denied"
  | "error";

const CAMERA_VIDEO_CONSTRAINTS: MediaStreamConstraints = {
  // `facingMode: "environment"` prefers the rear camera on phones, which is
  // what a point-and-shoot flow wants. Browsers fall back to the only camera
  // when no rear camera exists.
  video: { facingMode: "environment" },
  audio: false,
};

/**
 * True when getUserMedia is actually reachable. In insecure (non-HTTPS,
 * non-localhost) contexts `navigator.mediaDevices` is absent entirely, so the
 * presence check is also the secure-context gate; callers fall back to a
 * native `<input capture>` which does not need getUserMedia.
 */
export function cameraCaptureSupported(): boolean {
  if (typeof navigator === "undefined") {
    return false;
  }
  return typeof navigator.mediaDevices?.getUserMedia === "function";
}

/** Encode the visible, center-cropped preview as a JPEG attachment. */
export function captureVideoFrameToFile(video: HTMLVideoElement): Promise<File | null> {
  return new Promise((resolve) => {
    const width = video.videoWidth;
    const height = video.videoHeight;
    if (!width || !height) {
      resolve(null);
      return;
    }
    const canvas = document.createElement("canvas");
    const aspect = video.clientWidth && video.clientHeight
      ? video.clientWidth / video.clientHeight
      : width / height;
    const cropWidth = Math.min(width, height * aspect);
    const cropHeight = Math.min(height, width / aspect);
    canvas.width = Math.round(cropWidth);
    canvas.height = Math.round(cropHeight);
    const context = canvas.getContext("2d");
    if (!context) {
      resolve(null);
      return;
    }
    context.drawImage(video, (width - cropWidth) / 2, (height - cropHeight) / 2,
      cropWidth, cropHeight, 0, 0, canvas.width, canvas.height);
    canvas.toBlob((blob) => {
      if (!blob) {
        resolve(null);
        return;
      }
      resolve(new File([blob], `camera-${Date.now()}.jpg`, { type: "image/jpeg" }));
    }, "image/jpeg", 0.92);
  });
}

export function useComposerCamera(onCapture: (file: File) => void): {
  status: ComposerCameraStatus;
  errorMessage: string | null;
  videoRef: RefObject<HTMLVideoElement | null>;
  stream: MediaStream | null;
  open: () => void;
  close: () => void;
  capture: () => void;
  capturing: boolean;
} {
  const [status, setStatus] = useState<ComposerCameraStatus>("idle");
  const [errorMessage, setErrorMessage] = useState<string | null>(null);
  const [stream, setStream] = useState<MediaStream | null>(null);
  const [capturing, setCapturing] = useState(false);
  const capturingRef = useRef(false);
  const streamRef = useRef<MediaStream | null>(null);
  const videoRef = useRef<HTMLVideoElement | null>(null);
  // Bumping the generation invalidates any in-flight getUserMedia request so
  // a close/unmount/background switch cannot attach a stream after the panel
  // has already been torn down.
  const generationRef = useRef(0);

  const stopStream = useCallback(() => {
    capturingRef.current = false;
    const current = streamRef.current;
    streamRef.current = null;
    if (current) {
      for (const track of current.getTracks()) {
        track.stop();
      }
    }
  }, []);

  const close = useCallback(() => {
    generationRef.current += 1;
    stopStream();
    setStream(null);
    setStatus("idle");
    setCapturing(false);
    setErrorMessage(null);
  }, [stopStream]);

  const open = useCallback(() => {
    generationRef.current += 1;
    const generation = generationRef.current;
    stopStream();
    setStream(null);
    setStatus("starting");
    setCapturing(false);
    setErrorMessage(null);

    if (!cameraCaptureSupported()) {
      setStatus("unsupported");
      return;
    }

    void navigator.mediaDevices!.getUserMedia(CAMERA_VIDEO_CONSTRAINTS)
      .then((next) => {
        if (generation !== generationRef.current) {
          next.getTracks().forEach((track) => track.stop());
          return;
        }
        streamRef.current = next;
        setStream(next);
        setStatus("active");
      })
      .catch((error: unknown) => {
        if (generation !== generationRef.current) {
          return;
        }
        stopStream();
        const name =
          error && typeof error === "object" && "name" in error
            ? String((error as { name?: unknown }).name)
            : "";
        if (name === "NotAllowedError" || name === "PermissionDeniedError" || name === "SecurityError") {
          setStatus("denied");
          setErrorMessage(translateCurrent("composer.camera.permissionDenied"));
        } else {
          setStatus("error");
          setErrorMessage(translateCurrent("composer.camera.unavailable"));
        }
      });
  }, [stopStream]);

  const capture = useCallback(() => {
    const video = videoRef.current;
    if (!video || !streamRef.current || capturingRef.current) {
      return;
    }
    if (video.readyState < 2) {
      setErrorMessage(translateCurrent("composer.camera.notReady"));
      return;
    }
    const generation = generationRef.current;
    capturingRef.current = true;
    setCapturing(true);
    setErrorMessage(null);
    void (async () => {
      let file: File | null;
      try {
        file = await captureVideoFrameToFile(video);
      } catch {
        file = null;
      }
      // Encoding can finish after cancel, backgrounding, or a new camera session.
      if (generation !== generationRef.current) return;
      capturingRef.current = false;
      setCapturing(false);
      if (!file) {
        stopStream();
        setStream(null);
        setStatus("error");
        setErrorMessage(translateCurrent("composer.camera.captureFailed"));
        return;
      }
      close();
      onCapture(file);
    })();
  }, [close, onCapture, stopStream]);

  useEffect(() => {
    const video = videoRef.current;
    if (!video) {
      return;
    }
    video.srcObject = stream;
    if (stream) {
      const generation = generationRef.current;
      void video.play().catch(() => {
        if (generation !== generationRef.current) return;
        stopStream();
        setStream(null);
        setStatus("error");
        setErrorMessage(translateCurrent("composer.camera.unavailable"));
      });
    }
    return () => { video.srcObject = null; };
  }, [stream, stopStream]);

  useEffect(() => {
    return () => {
      generationRef.current += 1;
      stopStream();
    };
  }, [stopStream]);

  useEffect(() => {
    function pause(): void {
      generationRef.current += 1;
      stopStream();
      setStream(null);
      setCapturing(false);
      // Native pickers may background the page too; keep their input mounted.
      setStatus((current) => current === "active" || current === "starting" ? "paused" : current);
      setErrorMessage(null);
    }
    function handleVisibilityChange(): void {
      if (document.visibilityState === "hidden") {
        pause();
      }
    }
    document.addEventListener("visibilitychange", handleVisibilityChange);
    window.addEventListener("pagehide", pause);
    return () => {
      document.removeEventListener("visibilitychange", handleVisibilityChange);
      window.removeEventListener("pagehide", pause);
    };
  }, [stopStream]);

  return { status, errorMessage, videoRef, stream, open, close, capture, capturing };
}

export function ComposerMobileAttachmentChoices({
  onTakePhoto,
  onPickPhotos,
  onPickFiles,
}: {
  onTakePhoto: () => void;
  onPickPhotos: () => void;
  onPickFiles: () => void;
}): JSX.Element {
  const { t } = useI18n();
  return (
    <>
      <button role="menuitem" type="button" onClick={onTakePhoto}>
        <Camera className="icon-lg" aria-hidden="true" />
        <span className="composer-plus-menu-item-title">{t("composer.takePhoto")}</span>
        <span className="composer-plus-menu-item-desc">{t("composer.takePhotoHint")}</span>
      </button>
      <button role="menuitem" type="button" onClick={onPickPhotos}>
        <Images className="icon-lg" aria-hidden="true" />
        <span className="composer-plus-menu-item-title">{t("composer.choosePhotos")}</span>
        <span className="composer-plus-menu-item-desc">{t("composer.choosePhotosHint")}</span>
      </button>
      <button role="menuitem" type="button" onClick={onPickFiles}>
        <Paperclip className="icon-lg" aria-hidden="true" />
        <span className="composer-plus-menu-item-title">{t("composer.chooseFiles")}</span>
        <span className="composer-plus-menu-item-desc">{t("composer.addAttachmentHint")}</span>
      </button>
    </>
  );
}

export function ComposerCameraPanel({
  onCapture,
  onClose,
}: {
  onCapture: (file: File) => void;
  onClose: () => void;
}): JSX.Element {
  const { t } = useI18n();
  const dialogRef = useRef<HTMLDialogElement>(null);
  const panelRef = useRef<HTMLDivElement>(null);
  const stillRef = useRef<HTMLCanvasElement>(null);
  const exitingRef = useRef(false);
  const exitAnimationRef = useRef<Animation | null>(null);
  const [exiting, setExiting] = useState(false);
  const photoInputRef = useRef<HTMLInputElement>(null);
  const { status, errorMessage, videoRef, open, close, capture, capturing } = useComposerCamera(
    (file) => finish(() => onCapture(file)),
  );
  const nativeCaptureInputRef = useRef<HTMLInputElement>(null);
  const closeButtonRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    open();
  }, [open]);

  useEffect(() => {
    dialogRef.current?.showModal?.();
    closeButtonRef.current?.focus({ preventScroll: true });
    return () => { exitAnimationRef.current?.cancel(); };
  }, []);

  function finish(done: () => void): void {
    if (exitingRef.current) return;
    exitingRef.current = true;
    // Keep a local still for the exit transition while releasing the camera immediately.
    const video = videoRef.current;
    const still = stillRef.current;
    if (still && video && video.readyState >= 2 && video.videoWidth && video.videoHeight) {
      still.width = video.videoWidth;
      still.height = video.videoHeight;
      try { still.getContext("2d")?.drawImage(video, 0, 0); } catch { /* Closing must always release the stream. */ }
    }
    close();
    setExiting(true);
    const panel = panelRef.current;
    if (!panel?.animate || window.matchMedia?.("(prefers-reduced-motion: reduce)").matches) {
      done();
      return;
    }
    const animation = panel.animate([
      { transform: "translateY(0)", opacity: 1 },
      { transform: "translateY(100%)", opacity: 0 },
    ], { duration: 240, easing: "cubic-bezier(.4,0,1,1)", fill: "forwards" });
    exitAnimationRef.current = animation;
    void animation.finished.then(done, () => {});
  }

  function handleClose(): void {
    finish(onClose);
  }

  function handleNativeCapture(event: ChangeEvent<HTMLInputElement>): void {
    const selected = Array.from(event.currentTarget.files ?? []);
    event.currentTarget.value = "";
    if (selected.length > 0) {
      finish(() => onCapture(selected[0]));
    }
  }

  const previewVisible = status === "starting" || status === "active";
  const fallbackVisible = status === "unsupported" || status === "denied" || status === "error";

  return (
    <dialog
      ref={dialogRef}
      className="composer-camera-dialog"
      aria-label={t("composer.camera.title")}
      onCancel={(event) => {
        // File pickers also emit cancel; dismissing one must keep the camera open.
        if (event.target !== event.currentTarget) return;
        event.preventDefault();
        handleClose();
      }}
      onKeyDown={(event) => {
        if (event.key === "Escape") {
          event.preventDefault();
          handleClose();
        }
      }}
    >
    <div ref={panelRef} className="composer-camera" data-exiting={exiting || undefined} aria-busy={exiting}>
      <input
        ref={nativeCaptureInputRef}
        className="composer-file-input"
        type="file"
        accept="image/*"
        capture="environment"
        tabIndex={-1}
        onChange={handleNativeCapture}
      />
      <input ref={photoInputRef} className="composer-file-input" type="file" accept="image/*"
        tabIndex={-1} onChange={handleNativeCapture} />
      <div className="composer-camera-viewport">
        {previewVisible ? <video ref={videoRef} autoPlay playsInline muted aria-hidden="true" /> : null}
        <canvas ref={stillRef} className="composer-camera-still" hidden={!exiting} aria-hidden="true" />
        {capturing ? <div className="composer-camera-flash" aria-hidden="true" /> : null}
        {status === "starting" ? (
          <div className="composer-camera-status" role="status">
            {t("composer.camera.starting")}
          </div>
        ) : null}
        {status === "active" && errorMessage ? <div className="composer-camera-status" role="status">{errorMessage}</div> : null}
        {status === "paused" ? (
          <div className="composer-camera-error" role="status">
            <p>{t("composer.camera.paused")}</p>
            <button type="button" onClick={open}>{t("composer.camera.resume")}</button>
          </div>
        ) : null}
        {fallbackVisible ? (
          <div className="composer-camera-error" role="alert">
            <p>{errorMessage ?? t("composer.camera.unavailable")}</p>
            <button type="button" onClick={() => nativeCaptureInputRef.current?.click()}>
              <Camera aria-hidden="true" />
              {t("composer.camera.useNativeCamera")}
            </button>
          </div>
        ) : null}
      </div>
      <div className="composer-camera-controls">
        <button
          ref={closeButtonRef}
          type="button"
          className="composer-camera-close"
          aria-label={t("composer.camera.close")}
          title={t("composer.camera.close")}
          onClick={handleClose}
          disabled={exiting}
        >
          <ChevronLeft aria-hidden="true" />
        </button>
        <button
          type="button"
          className="composer-camera-shutter"
          aria-label={t("composer.camera.capture")}
          title={t("composer.camera.capture")}
          disabled={status !== "active" || capturing || exiting}
          aria-busy={capturing}
          onClick={capture}
        >
          <span aria-hidden="true" />
        </button>
        <button type="button" className="composer-camera-library" aria-label={t("composer.choosePhotos")}
          disabled={exiting || capturing} onClick={() => photoInputRef.current?.click()}>
          <Images aria-hidden="true" />
        </button>
      </div>
    </div>
    </dialog>
  );
}

export function ComposerAttachMenuButton({
  variant = "dock",
  disabled,
  menuAnchorRef,
  onTakePhoto,
  onPickPhotos,
  onPickFiles,
}: {
  variant?: ComposerVariant;
  disabled: boolean;
  menuAnchorRef: RefObject<HTMLElement | null>;
  onTakePhoto: () => void;
  onPickPhotos: () => void;
  onPickFiles: () => void;
}): JSX.Element {
  const { t } = useI18n();
  const triggerRef = useRef<HTMLDivElement>(null);
  const [open, setOpen] = useState(false);

  useEffect(() => {
    if (!open) {
      return undefined;
    }
    function handlePointerDown(event: PointerEvent): void {
      const target = event.target;
      if (!(target instanceof Node)) {
        return;
      }
      if (triggerRef.current?.contains(target)) {
        return;
      }
      if (isInsideFloatingMenu(target, "composer-attach")) {
        return;
      }
      setOpen(false);
    }
    function handleKeyDown(event: KeyboardEvent): void {
      if (event.key === "Escape") {
        setOpen(false);
      }
    }
    document.addEventListener("pointerdown", handlePointerDown);
    document.addEventListener("keydown", handleKeyDown);
    return () => {
      document.removeEventListener("pointerdown", handlePointerDown);
      document.removeEventListener("keydown", handleKeyDown);
    };
  }, [open]);

  function pick(action: () => void): void {
    setOpen(false);
    action();
  }

  return (
    <div className="composer-plus-menu-anchor" ref={triggerRef}>
      <button
        className="composer-tool-button composer-attach-button"
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={t("composer.addAttachment")}
        title={t("composer.addAttachment")}
        disabled={disabled}
        onClick={() => setOpen((current) => !current)}
      >
        <Paperclip aria-hidden="true" />
      </button>
      {open ? (
        <FloatingMenuPortal
          anchorRef={menuAnchorRef}
          owner="composer-attach"
          placement="above"
          align="left"
          offset={variant === "hero" ? 10 : 8}
          width={320}
          matchAnchorWidth
        >
          <div className="composer-context-menu composer-plus-menu" role="menu" aria-label={t("composer.addAttachment")}>
            <div className="composer-plus-menu-section" role="presentation">{t("composer.plusSectionAdd")}</div>
            <ComposerMobileAttachmentChoices
              onTakePhoto={() => pick(onTakePhoto)}
              onPickPhotos={() => pick(onPickPhotos)}
              onPickFiles={() => pick(onPickFiles)}
            />
          </div>
        </FloatingMenuPortal>
      ) : null}
    </div>
  );
}
