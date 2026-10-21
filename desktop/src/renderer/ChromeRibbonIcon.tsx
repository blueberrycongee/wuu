import type { RibbonIconModel } from "../shared/chromeOverlay";
import { Globe, MessagesSquare, RotateCw } from "./WuuIcons";
import { PluginBlocksIcon } from "./PluginBlocksIcon";
import { PublicIcon } from "./PublicIcon";
import { WebsiteIcon } from "./WebsiteIcon";

export function ChromeRibbonIcon({ icon }: { icon: RibbonIconModel }): JSX.Element {
  if (icon.kind === "conversations") return <MessagesSquare aria-hidden="true" />;
  if (icon.kind === "catalog") return <PluginBlocksIcon aria-hidden="true" />;
  if (icon.kind === "public") return <PublicIcon name={icon.name} />;
  if (icon.kind === "image") return icon.dark ? <span className="plugin-icon-themed" aria-hidden="true">
    <img className="plugin-icon-asset plugin-icon-asset-light" src={icon.light.replace("wuu-plugin://asset/", "wuu-chrome://ui/plugin-icon/")} alt="" />
    <img className="plugin-icon-asset plugin-icon-asset-dark" src={icon.dark.replace("wuu-plugin://asset/", "wuu-chrome://ui/plugin-icon/")} alt="" />
  </span> : <img className="plugin-icon-asset" src={icon.light.replace("wuu-plugin://asset/", "wuu-chrome://ui/plugin-icon/")} alt="" />;
  return icon.loading ? <RotateCw className="sidebar-ribbon-tab-loading" aria-hidden="true" />
    : <WebsiteIcon url={icon.url} favicon={icon.favicon} fallback={<span className="sidebar-ribbon-website-icon" aria-hidden="true">{icon.url === "about:blank" && icon.startPage !== false ? <Globe /> : Array.from(icon.title)[0]?.toLocaleUpperCase() || <Globe />}</span>} />;
}
