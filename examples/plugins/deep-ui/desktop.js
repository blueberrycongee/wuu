export async function activate(api) {
  const React = api.react;

  api.registerStyle({
    id: "deep-ui-example",
    css: `
      .deep-ui-frame {
        position: relative;
        min-width: 0;
        min-height: 0;
      }
      .deep-ui-frame::before {
        content: "";
        position: absolute;
        inset: 0;
        pointer-events: none;
        border: 1px solid color-mix(in srgb, var(--wuu-accent) 24%, transparent);
        border-radius: 12px;
        z-index: 1;
      }
      .deep-ui-timeline {
        padding-inline: 6px;
      }
      .deep-ui-catalog {
        max-width: 1180px;
        margin-inline: auto;
      }
    `,
  });

  api.registerUIContribution({
    id: "frame-conversation.timeline",
    target: "wuu.default-conversation/conversation.turn",
    mode: "wrap",
    render({ children }) {
      return React.createElement("div", { className: "deep-ui-timeline" }, children);
    },
  });
}
