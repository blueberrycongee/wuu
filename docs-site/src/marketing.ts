/** Resolve the standalone marketing pages beneath the documentation site's base. */
export function marketingHtml(source: string, baseUrl: string): string {
  const base = baseUrl.replace(/\/$/, "")
  return source
    .replaceAll('"assets/', `"${base}/site-assets/`)
    .replace(/"styles\.css(\?[^ "]*)?"/, (_, query = "") => `"${base}/site-assets/styles.css${query}"`)
    .replace('"projection-diagrams.css"', `"${base}/site-assets/projection-diagrams.css"`)
    .replace('"site-nav.mjs"', `"${base}/site-assets/site-nav.mjs"`)
    .replace('"mascot-motion.mjs"', `"${base}/site-assets/mascot-motion.mjs"`)
    .replaceAll('href="./', `href="${base}/`)
    .replace(/href="([a-z-]+)\.html(#[^"]*)?"/g, (_, page, hash = "") => `href="${base}/${page}/${hash}"`)
}
