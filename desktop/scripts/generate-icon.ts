/**
 * Generate all app icons from assets/app-icon-source.png.
 * Run `npm run icon:generate` from desktop on macOS (Swift and iconutil).
 * Platform rasters and the iconset are kept in build/.icon-work/.
 */
import { execFileSync } from "node:child_process";
import { cpSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const DESKTOP_DIR = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const REPO_DIR = path.resolve(DESKTOP_DIR, "..");
const BUILD_DIR = path.join(DESKTOP_DIR, "build");
const WORK_DIR = path.join(BUILD_DIR, ".icon-work");

// ICO stores one PNG per size, with 0 representing 256 in its byte-sized fields.
function buildIco(sizes: readonly number[]): void {
  const images = sizes.map((size) => readFileSync(path.join(WORK_DIR, `desktop-${size}.png`)));
  const header = Buffer.alloc(6);
  header.writeUInt16LE(1, 2);
  header.writeUInt16LE(images.length, 4);
  const entries = Buffer.alloc(16 * images.length);
  let offset = 6 + entries.length;
  images.forEach((png, index) => {
    const size = sizes[index]!;
    entries.writeUInt8(size === 256 ? 0 : size, index * 16);
    entries.writeUInt8(size === 256 ? 0 : size, index * 16 + 1);
    entries.writeUInt16LE(1, index * 16 + 4);
    entries.writeUInt16LE(32, index * 16 + 6);
    entries.writeUInt32LE(png.length, index * 16 + 8);
    entries.writeUInt32LE(offset, index * 16 + 12);
    offset += png.length;
  });
  writeFileSync(path.join(BUILD_DIR, "icon.ico"), Buffer.concat([header, entries, ...images]));
}

function copyAsset(source: string, destination: string): void {
  mkdirSync(path.dirname(path.join(REPO_DIR, destination)), { recursive: true });
  cpSync(path.join(WORK_DIR, source), path.join(REPO_DIR, destination));
}

function main(): void {
  rmSync(WORK_DIR, { recursive: true, force: true });
  mkdirSync(WORK_DIR, { recursive: true });
  execFileSync("swift", [
    path.join(DESKTOP_DIR, "scripts/render-icon.swift"),
    path.join(REPO_DIR, "assets/app-icon-source.png"),
    WORK_DIR,
  ], { stdio: "inherit" });

  const iconset = path.join(WORK_DIR, "icon.iconset");
  mkdirSync(iconset);
  for (const size of [16, 32, 128, 256, 512]) {
    for (const scale of [1, 2]) {
      cpSync(path.join(WORK_DIR, `desktop-${size * scale}.png`),
        path.join(iconset, `icon_${size}x${size}${scale === 2 ? "@2x" : ""}.png`));
    }
  }
  execFileSync("iconutil", ["-c", "icns", iconset, "-o", path.join(BUILD_DIR, "icon.icns")]);
  buildIco([16, 24, 32, 48, 64, 128, 256]);

  copyAsset("desktop-1024.png", "desktop/build/icon.png");
  copyAsset("desktop-1024.png", "assets/app-icon.png");
  copyAsset("desktop-256.png", "assets/app-icon-256.png");
  copyAsset("desktop-1024.png", "landing/assets/app-icon.png");
  copyAsset("desktop-32.png", "docs-site/public/favicon.png");
  copyAsset("mobile.png", "clients/native/ios/App/Assets.xcassets/AppIcon.appiconset/icon.png");
  copyAsset("mobile.png", "clients/mobile/assets/icon.png");
  copyAsset("desktop-32.png", "clients/mobile/assets/favicon.png");
  copyAsset("adaptive.png", "clients/mobile/assets/adaptive-icon.png");
  copyAsset("mobile.png", "clients/mobile-app/ios/App/App/Assets.xcassets/AppIcon.appiconset/AppIcon-512@2x.png");
  for (const density of ["mdpi", "hdpi", "xhdpi", "xxhdpi", "xxxhdpi"]) {
    for (const name of ["ic_launcher", "ic_launcher_round", "ic_launcher_foreground"]) {
      for (const client of ["mobile-app", "native"]) {
        copyAsset(`${density}-${name}.png`, `clients/${client}/android/app/src/main/res/mipmap-${density}/${name}.png`);
      }
    }
  }
  console.log("Desktop, shared, Expo, iOS, and Android icons updated from assets/app-icon-source.png.");
}

main();
