import { fileSelectionLocation } from "./PdfSelection";
import { usePdfSelectionNavigation } from "./FileSelectionContext";
import { useState } from "react";
import type { FileSelectionSource } from "../shared/protocol";
import { ComposerQuoteCard } from "./ComposerResponseSelectionCard";
import { readFileSelectionNavigation } from "./FileSelectionNavigation";
import { useI18n } from "./i18n";

/** File navigation adapts to the existing quote card without changing its chrome. */
export function ComposerFileSelectionCard({ source, comment, onChangeComment, onRemove, onOpenFile }: {
  source: FileSelectionSource;
  comment?: string;
  onChangeComment?: (comment: string) => void;
  onRemove?: () => void;
  onOpenFile?: (path: string) => void;
}): JSX.Element {
  const { t } = useI18n();
  const openPdf = usePdfSelectionNavigation();
  const [notice, setNotice] = useState<string | null>(null);
  async function openSource(): Promise<boolean> {
    if (source.pdf?.artifact_uri) {
      const opened = openPdf?.(source) ?? false;
      setNotice(opened ? null : t("selectionChip.pdfOriginalConversation"));
      return opened;
    }
    const target = await readFileSelectionNavigation(source);
    setNotice(target.locationChanged
      ? !target.available ? t("selectionChip.locationUnavailable") : t("selectionChip.locationChanged")
      : null);
    onOpenFile?.(target.path);
    return !target.locationChanged;
  }
  return <ComposerQuoteCard className="composer-file-selection-card" text={source.quote} comment={comment}
    showComment={Boolean(onChangeComment || comment)} meta={source.pdf ? `${source.path.split(/[\\/]/).pop()} · ${fileSelectionLocation(source)}` : `${source.path.split(/[\\/]/).pop()}:${source.start_line}`} notice={notice}
    onChangeComment={onChangeComment} onRemove={onRemove} onOpenSource={source.pdf?.artifact_uri ? openPdf ? openSource : undefined : onOpenFile ? openSource : undefined} />;
}
