import { useState } from "react";
import type { FileSelectionSource } from "../shared/protocol";
import { ComposerQuoteCard } from "./ComposerResponseSelectionCard";
import { fileSelectionNavigation } from "./FileSelectionNavigation";
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
  const [notice, setNotice] = useState<string | null>(null);
  async function openSource(): Promise<boolean> {
    let currentText: string | undefined;
    try {
      const file = await window.wuu.readWorkspaceFile(source.path, source.workspace);
      currentText = file.binary || file.truncated ? undefined : file.text;
    } catch {
      // Opening remains useful when the original excerpt cannot be located.
    }
    const target = fileSelectionNavigation(source, currentText);
    setNotice(target.locationChanged
      ? currentText === undefined ? t("selectionChip.locationUnavailable") : t("selectionChip.locationChanged")
      : null);
    onOpenFile?.(target.path);
    return !target.locationChanged;
  }
  return <ComposerQuoteCard className="composer-file-selection-card" text={source.quote} comment={comment}
    showComment={Boolean(onChangeComment || comment)} meta={`${source.path.split(/[\\/]/).pop()}:${source.start_line}`} notice={notice}
    onChangeComment={onChangeComment} onRemove={onRemove} onOpenSource={onOpenFile ? openSource : undefined} />;
}
