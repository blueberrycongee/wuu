# Manga Studio

Manga Studio turns the Wuu desktop into a high-contrast comic workspace using only public plugin
contracts. It combines a declarative theme with generation-scoped CSS, layout-neutral app and
Settings scopes, and Wuu's published `data-wuu-*` styling coordinates.

## Try it

1. Install this directory from Wuu's Skills and Plugins catalog.
2. Approve and enable **Manga Studio**.
3. Select **Manga Paper** under **Settings → Appearance**.

The executable desktop entry is intentionally self-contained and uses the React instance supplied by
Wuu. It adds no per-view or per-message DOM wrappers and keeps Wuu's native message, process,
composer, header, and navigation components mounted so
Markdown, streaming, actions, keyboard behavior, and compact nested layouts continue to work. Disable
the plugin to unload every frame, presenter, style, and decorative element at once.

## Design

- warm paper canvas with heavy ink panel frames and offset print shadows;
- cyan, yellow, and magenta spot colors across controls, badges, inline code, links, and status;
- speech-bubble user messages with an ink outline and offset shadow (main and chat views);
- manga hover treatment for sidebar navigation, project, and thread rows;
- semantic styling across navigation, conversations, settings, catalogs, and workspace panels;
- public coordinates for all four workspace tools, plus Shadow DOM and xterm style bridges;
- semantic panel treatment for messages, reasoning, and Tool activity;
- reduced-motion support and preserved host fallbacks.
