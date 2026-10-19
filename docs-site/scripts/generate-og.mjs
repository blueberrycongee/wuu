import { mkdir } from "node:fs/promises"
import path from "node:path"
import { fileURLToPath } from "node:url"

import sharp from "sharp"

const siteRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..")
const repositoryRoot = path.resolve(siteRoot, "..")
const readmeAssets = path.join(repositoryRoot, "landing/assets/readme")
const output = path.join(siteRoot, "public")

const cards = [
  {
    file: "og-zh.png",
    label: "文档",
    description: "本地项目里的 AI Agent",
    screenshot: "hero-light-zh.jpg",
  },
  {
    file: "og-en.png",
    label: "docs",
    description: "AI agent for local projects",
    screenshot: "hero-light-en.jpg",
  },
]

await mkdir(output, { recursive: true })

for (const card of cards) {
  const screenshot = await sharp(path.join(readmeAssets, card.screenshot))
    .resize(760, 510, { fit: "cover" })
    .png()
    .toBuffer()

  const rounded = await sharp(screenshot)
    .composite([
      {
        input: Buffer.from(
          '<svg width="760" height="510"><rect width="760" height="510" rx="18" fill="#fff"/></svg>',
        ),
        blend: "dest-in",
      },
    ])
    .png()
    .toBuffer()

  const background = Buffer.from(`<svg width="1200" height="630" xmlns="http://www.w3.org/2000/svg">
    <rect width="1200" height="630" fill="#f0eee6"/>
    <rect x="388" y="72" width="760" height="510" rx="18" fill="#191a18" opacity=".10"/>
    <rect x="380" y="64" width="760" height="510" rx="18" fill="none" stroke="#d5d3c8"/>
    <text x="72" y="118" fill="#191a18" font-family="Georgia, serif" font-size="72" font-weight="700" letter-spacing="-4">wuu</text>
    <text x="76" y="196" fill="#191a18" font-family="PingFang SC, Noto Sans CJK SC, sans-serif" font-size="38" font-weight="600">${card.label}</text>
    <text x="76" y="247" fill="#45483f" font-family="PingFang SC, Noto Sans CJK SC, sans-serif" font-size="23">${card.description}</text>
    <rect x="76" y="292" width="56" height="4" rx="2" fill="#526650"/>
  </svg>`)

  await sharp(background)
    .composite([
      {
        input: rounded,
        left: 380,
        top: 64,
      },
    ])
    .png()
    .toFile(path.join(output, card.file))
}
