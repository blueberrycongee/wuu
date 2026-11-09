import type { ComponentProps, Ref } from "react";
import { RichContent } from "./RichContent";

export function isMarkdownDocument(path: string, contentType?: string): boolean {
  const mimeType = contentType?.split(";", 1)[0].trim().toLowerCase();
  return mimeType === "text/markdown" || mimeType === "text/x-markdown"
    || mimeType === "text/mdx" || /\.(md|mdx|markdown)$/i.test(path);
}

export function MarkdownDocumentReading({
  ref,
  ...props
}: ComponentProps<typeof RichContent> & { ref?: Ref<HTMLDivElement> }): JSX.Element {
  return (
    <div className="workspace-markdown-reading" ref={ref}>
      <RichContent {...props} />
    </div>
  );
}
