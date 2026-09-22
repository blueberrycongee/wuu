import { Archive, CircleAlert } from "./WuuIcons";
import { useI18n } from "./i18n";
import { TopNotice } from "./TopNotice";

export type ArchiveTipProps = {
  threadTitle: string;
  errorMessage?: string;
  onViewArchive: () => void;
  onDismiss: () => void;
  // Escape hatch shown when archiving was rejected because the conversation
  // is (or appears to be) still running: retries the archive with force.
  onForceArchive?: () => void;
};

/**
 * Archive result toast. Reuses the generic TopNotice so success and failure
 * notices share the same pill styling and auto-dismiss behavior.
 */
export function ArchiveTip({
  threadTitle,
  errorMessage,
  onViewArchive,
  onDismiss,
  onForceArchive,
}: ArchiveTipProps): JSX.Element {
  const { t } = useI18n();
  const failed = Boolean(errorMessage);
  const trimmedTitle = threadTitle.trim();

  const message = errorMessage ? (
    <span>{errorMessage}</span>
  ) : trimmedTitle ? (
    <>
      <strong>{trimmedTitle}</strong>
      <span> {t("archive.archivedSuffix")}</span>
    </>
  ) : (
    <span>{t("archive.conversationArchived")}</span>
  );

  const action = failed
    ? onForceArchive
      ? { label: t("thread.archive.force"), onClick: onForceArchive }
      : undefined
    : { label: t("archive.viewArchive"), onClick: onViewArchive };

  return (
    <TopNotice
      message={message}
      icon={failed ? CircleAlert : Archive}
      onDismiss={onDismiss}
      isError={failed}
      dismissAriaLabel={t("common.closeNotice")}
      action={action}
    />
  );
}
