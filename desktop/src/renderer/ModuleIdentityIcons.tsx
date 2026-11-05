import { createArtworkIcon } from "./ArtworkIcon";
import layout from "./assets/plugin-identities/app-layout.webp";
import settings from "./assets/plugin-identities/app-settings.webp";
import navigation from "./assets/plugin-identities/app-navigation.webp";
import conversations from "./assets/plugin-identities/app-conversations.webp";
import composer from "./assets/plugin-identities/app-composer.webp";
import preview from "./assets/plugin-identities/app-preview.webp";
import application from "./assets/plugin-identities/app-application.webp";

export const MODULE_IDENTITY_ICONS = {
  "app-layout": createArtworkIcon(layout, "app-layout"),
  "app-settings": createArtworkIcon(settings, "app-settings"),
  "app-navigation": createArtworkIcon(navigation, "app-navigation"),
  "app-conversations": createArtworkIcon(conversations, "app-conversations"),
  "app-composer": createArtworkIcon(composer, "app-composer"),
  "app-preview": createArtworkIcon(preview, "app-preview"),
  "app-application": createArtworkIcon(application, "app-application"),
} as const;
