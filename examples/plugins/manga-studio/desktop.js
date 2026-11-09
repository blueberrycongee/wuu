export async function activate(api) {
  api.registerCSSSnippet({
    id: "manga-ink-details",
    order: 20,
    css: `
      [data-wuu-component="plugin-ui-panel"],
      [data-wuu-component="plugin-ui-card"] {
        border-width: 2px;
        box-shadow: 5px 5px 0 var(--wuu-color-text);
      }
      [data-wuu-component="jump-to-latest"] {
        border-radius: var(--wuu-radius-control);
      }
      :focus-visible {
        outline-width: 3px;
        outline-offset: 3px;
      }
    `,
  });
}
