import { useState, type ComponentProps } from "react";
import { ContentPreview } from "./ContentPreview";
import { ImagePreviewGallery } from "./ImagePreviewGallery";
import { MessageCopyButton } from "./MessageActions";
import { Tooltip } from "./Tooltip";
import { Maximize2 } from "./WuuIcons";
import { useI18n } from "./i18n";
import { UILayerPortal } from "./ui/layers/UILayerHost";

export function RichTable({ children, source, ...props }: ComponentProps<"div"> & { source: string }): JSX.Element {
  const { t } = useI18n();
  const [expanded, setExpanded] = useState(false);
  return (
    <div {...props} className="rich-table-wrap">
      <div className="rich-table-scroll" data-scroll-fade="inline" tabIndex={0} role="region" aria-label={t("rich.table")}>
        <table>{children}</table>
      </div>
      <div className="rich-table-actions">
        <Tooltip content={t("rich.copyTable")}><MessageCopyButton getText={() => source}
          className="icon-button" iconSize={16} idleLabel={t("rich.copyTable")} copiedLabel={t("rich.tableCopied")} /></Tooltip>
        <Tooltip asChild content={t("rich.expandTable")}><button type="button" className="icon-button"
          aria-label={t("rich.expandTable")} aria-haspopup="dialog" onClick={event => {
            event.currentTarget.focus();
            setExpanded(true);
          }}>
          <Maximize2 className="icon" aria-hidden="true" />
        </button></Tooltip>
      </div>
      {expanded && <UILayerPortal layer="dialog">
        <ContentPreview label={t("rich.table")} className="rich-table-preview" onClose={() => setExpanded(false)}>
          <ImagePreviewGallery><div className="rich-content rich-table-preview-panel">
            <div className="rich-table-wrap rich-table-preview-scroll" tabIndex={0} role="region" aria-label={t("rich.table")}>
              <table>{children}</table>
            </div>
          </div></ImagePreviewGallery>
        </ContentPreview>
      </UILayerPortal>}
    </div>
  );
}
