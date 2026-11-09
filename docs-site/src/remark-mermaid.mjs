const escapeHtml = (value) =>
  value.replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;")

export default function remarkMermaidBlocks() {
  return (tree) => walk(tree)
}

function walk(node) {
  if (!node?.children) return

  node.children = node.children.map((child) => {
    if (child.type === "code" && child.lang === "mermaid") {
      return {
        type: "html",
        value: `<div class="mermaid-diagram"><pre class="mermaid">${escapeHtml(child.value)}</pre></div>`,
      }
    }
    walk(child)
    return child
  })
}
