/** @param {import("@wuu/plugin-sdk").ApplicationPresentationAPI} api */
export function activatePresentation(api) {
  const React = api.react;
  const h = React.createElement;
  for (const owner of ["wuu.default-conversation", "wuu.default-preview"]) {
    api.registerUIContribution({
      id: `usage-${owner}`,
      target: `${owner}/conversation.turn.after`,
      order: 20,
      render({ context }) {
        const { snapshot, turnId } = /** @type {import("@wuu/plugin-sdk").ConversationTurnSlotContextV1} */ (context);
        const parts = [];
        if (snapshot.duration_ms !== undefined) parts.push(`${snapshot.duration_ms} ms`);
        if (snapshot.input_tokens !== undefined) parts.push(`${snapshot.input_tokens} input tokens`);
        if (snapshot.output_tokens !== undefined) parts.push(`${snapshot.output_tokens} output tokens`);
        if (!parts.length) return null;
        return h("p", { "data-conversation-metrics-turn": turnId, "aria-label": "Reported turn usage",
          style: { margin: "0.5em 0", color: "var(--wuu-color-text-muted)", fontSize: "inherit" } }, parts.join(" · "));
      },
    });
    api.registerUIContribution({
      id: `tool-result-${owner}`,
      target: `${owner}/conversation.tool-activity.after`,
      order: 20,
      render({ context }) {
        const { snapshot, itemId } = /** @type {import("@wuu/plugin-sdk").ConversationToolActivitySlotContextV1} */ (context);
        const [open, setOpen] = React.useState(false);
        // Missing measurement data is not zero. Tool timing comes from a
        // measurement provider; this example exposes the reported result only.
        if (snapshot.resultText === undefined) return null;
        return h("div", { "data-conversation-metrics-tool": itemId, style: { margin: "0.5em 0" } },
          h("button", { type: "button", "aria-expanded": open, onClick: () => setOpen(value => !value),
            style: { font: "inherit", color: "var(--wuu-color-text)", background: "var(--wuu-color-surface)",
              border: "1px solid var(--wuu-color-border-subtle)", borderRadius: "var(--wuu-radius-control)", padding: "0.25em 0.5em" } }, "Inspect reported result"),
          open ? h("pre", { style: { whiteSpace: "pre-wrap", overflowWrap: "anywhere" } }, snapshot.resultText) : null);
      },
    });
  }
}
