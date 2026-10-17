# Next message settings preview

Run from `desktop/`:

```sh
npx vite --config dev/next-message/vite.config.ts
```

Open <http://127.0.0.1:5227/dev/next-message/>. This renders the production
Next message plugin with the shared UI kit and settings styles. Model choices
and saved values are synthetic and reset on reload; no model requests are made.
The input illustration explains the feature and does not accept keyboard input.

Query parameters: `theme=dark`, `font=20`, `locale=en`, `empty`, and `long`.
The top-right control switches themes. Resize the window to inspect stacked
fields, and scroll to inspect the footer at large font sizes. Select a high
reasoning level to inspect the latency hint; select the model alias to inspect
its fixed reasoning level. Save displays the production success feedback
against the disposable runtime stub.
