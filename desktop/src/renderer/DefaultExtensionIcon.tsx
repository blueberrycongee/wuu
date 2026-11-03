import { createArtworkIcon } from "./ArtworkIcon";
import artwork from "./assets/plugin-default.webp";

/** Neutral identity for extensions without their own artwork. */
export const DefaultExtensionIcon = createArtworkIcon(artwork, "plugin-blocks");
