import { defineRouteMiddleware } from "@astrojs/starlight/route-data"
import type { StarlightRouteData } from "@astrojs/starlight/route-data"

type SidebarEntry = StarlightRouteData["sidebar"][number]
type SidebarLink = Extract<SidebarEntry, { type: "link" }>

export const onRequest = defineRouteMiddleware((context) => {
  const route = context.locals.starlightRoute
  const marker = `__locale:${route.locale ?? "zh-cn"}`
  const localeSidebar = route.sidebar.find(
    (entry) => entry.type === "group" && entry.label === marker,
  )

  if (localeSidebar?.type === "group") {
    route.sidebar = localeSidebar.entries
    route.hasSidebar = localeSidebar.entries.length > 0

    // Pagination is calculated before the locale group is selected above.
    // Recompute it from the visible sidebar so labels match the active locale.
    const flatten = (entries: SidebarEntry[]): SidebarLink[] =>
      entries.flatMap((entry) => (entry.type === "group" ? flatten(entry.entries) : [entry]))
    const links = flatten(route.sidebar)
    const pathname = context.url.pathname.replace(/\/$/, "")
    const current = links.findIndex((entry) => entry.href.replace(/\/$/, "") === pathname)

    if (current >= 0) {
      for (const link of links) link.isCurrent = false
      links[current]!.isCurrent = true
    }

    route.pagination = {
      prev: current > 0 ? links[current - 1] : undefined,
      next: current >= 0 ? links[current + 1] : undefined,
    }
  }

  // Generated content lives under generated-content/docs, so the default
  // Starlight edit URL would point at a path that does not exist in the repo.
  const sourcePath = route.entry.filePath?.replace(/^generated-content\/docs\//, "")
  if (sourcePath?.endsWith(".md")) {
    route.editUrl = new URL(
      `https://github.com/blueberrycongee/wuu/edit/main/docs/${sourcePath}`,
    )
  }
})
