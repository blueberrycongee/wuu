type AuthWindow = {
  isDestroyed(): boolean;
  isMinimized(): boolean;
  restore(): void;
  show(): void;
  focus(): void;
};

/** Capture an existing window, never create a replacement after sign-in. */
export function authWindowReturn(window: AuthWindow | null): () => void {
  let returned = false;
  return () => {
    if (returned) return;
    returned = true;
    if (!window || window.isDestroyed()) return;
    if (window.isMinimized()) window.restore();
    window.show();
    window.focus();
  };
}
