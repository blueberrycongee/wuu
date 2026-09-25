/** Feedback has its own reading area so it never competes with send controls. */
export function ComposerFeedback({
  text,
  liveProgress = false,
}: {
  text: string;
  liveProgress?: boolean;
}): JSX.Element | null {
  if (!text) return null;
  // Start each new message at the top after the user scrolls a long error.
  return (
    <div key={text} className="composer-feedback" role="status" tabIndex={0}>
      <span className={liveProgress ? "live-progress-chip" : undefined}>{text}</span>
    </div>
  );
}
