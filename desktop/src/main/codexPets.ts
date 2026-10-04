import { existsSync, mkdirSync, readdirSync, readFileSync, statSync } from "node:fs";
import { homedir } from "node:os";
import { isAbsolute, join, normalize, relative, resolve } from "node:path";
import type {
  CodexPet,
  CodexPetSettings,
  CodexPetsSnapshot,
  CodexPetState,
} from "../shared/protocol";
import { renderableFileURL } from "./renderableFileURLs";

export const CODEX_PET_CELL_WIDTH = 192;
export const CODEX_PET_CELL_HEIGHT = 208;

// Frame timings follow the Codex Pets atlas convention: every frame of a
// row holds for the same time except the last, which lingers so a loop
// reads as a gesture rather than a strobe. Idle breathes unevenly.
function frameDurations(count: number, frameMs: number, lastMs: number): number[] {
  return Array.from({ length: count }, (_, index) => (index === count - 1 ? lastMs : frameMs));
}

export const CODEX_PET_STATES: CodexPetState[] = [
  { id: "idle", label: "Idle", row: 0, durations: [280, 110, 110, 140, 140, 320] },
  { id: "running-right", label: "Run right", row: 1, durations: frameDurations(8, 120, 220) },
  { id: "running-left", label: "Run left", row: 2, durations: frameDurations(8, 120, 220) },
  { id: "waving", label: "Waving", row: 3, durations: frameDurations(4, 140, 280) },
  { id: "jumping", label: "Jumping", row: 4, durations: frameDurations(5, 140, 280) },
  { id: "failed", label: "Failed", row: 5, durations: frameDurations(8, 140, 240) },
  { id: "waiting", label: "Waiting", row: 6, durations: frameDurations(6, 150, 260) },
  { id: "running", label: "Running", row: 7, durations: frameDurations(6, 120, 220) },
  { id: "review", label: "Review", row: 8, durations: frameDurations(6, 150, 280) },
];

type CodexPetManifest = {
  id: string;
  displayName: string;
  description: string;
  spritesheetPath: string;
};

type LoadCodexPetsSnapshotOptions = {
  petsDir?: string;
  petsDirs?: string[];
  settings?: CodexPetSettings;
};

function defaultWuuHomeDir(homeDir: string = homedir()): string {
  const override = process.env.WUU_HOME?.trim();
  if (override) {
    return resolve(override);
  }
  return join(homeDir, ".wuu");
}

export function defaultCodexPetsDir(wuuHome: string = defaultWuuHomeDir()): string {
  return join(wuuHome, "pets");
}

export function legacyCodexPetsDir(homeDir: string = homedir()): string {
  return join(homeDir, ".codex", "pets");
}

export function defaultCodexPetsDirs(): string[] {
  const primary = defaultCodexPetsDir();
  const legacy = legacyCodexPetsDir();
  return primary === legacy ? [primary] : [primary, legacy];
}

export function ensureCodexPetsDir(petsDir: string = defaultCodexPetsDir()): void {
  mkdirSync(petsDir, { recursive: true });
}

export function loadCodexPetsSnapshot({
  petsDir,
  petsDirs,
  settings = { enabled: false, selected_id: "" },
}: LoadCodexPetsSnapshotOptions = {}): CodexPetsSnapshot {
  const roots = normalizePetsDirs(petsDirs ?? (petsDir ? [petsDir] : defaultCodexPetsDirs()));
  const home = roots[0] ?? defaultCodexPetsDir();
  const petsByID = new Map<string, CodexPet>();
  const errors: string[] = [];

  for (const root of roots) {
    if (!existsSync(root)) {
      continue;
    }
    for (const entry of readdirSync(root, { withFileTypes: true })) {
      if (!entry.isDirectory()) {
        continue;
      }
      const pet = readCodexPet(join(root, entry.name), entry.name, errors);
      if (pet && !petsByID.has(pet.id)) {
        petsByID.set(pet.id, pet);
      }
    }
  }

  const pets = Array.from(petsByID.values());
  pets.sort((left, right) =>
    left.display_name.localeCompare(right.display_name) || left.id.localeCompare(right.id),
  );

  const storedSelectedID = settings.selected_id.trim();
  const selectedPet = pets.find((pet) => pet.id === storedSelectedID) ?? pets[0];
  const selectedID = selectedPet?.id ?? "";

  return {
    home,
    pets,
    errors,
    enabled: settings.enabled && selectedID !== "",
    selected_id: selectedID,
  };
}

function normalizePetsDirs(petsDirs: string[]): string[] {
  const normalized: string[] = [];
  const seen = new Set<string>();
  for (const dir of petsDirs) {
    const trimmed = dir.trim();
    if (!trimmed) {
      continue;
    }
    const resolved = resolve(trimmed);
    if (seen.has(resolved)) {
      continue;
    }
    seen.add(resolved);
    normalized.push(resolved);
  }
  return normalized;
}

function readCodexPet(petDir: string, dirName: string, errors: string[]): CodexPet | undefined {
  const manifestPath = join(petDir, "pet.json");
  if (!existsSync(manifestPath)) {
    errors.push(`${dirName}: missing pet.json`);
    return undefined;
  }

  let manifest: CodexPetManifest;
  try {
    manifest = parseCodexPetManifest(readFileSync(manifestPath, "utf8"));
  } catch (error) {
    errors.push(`${dirName}: ${error instanceof Error ? error.message : "invalid pet.json"}`);
    return undefined;
  }

  const spritesheetPath = resolvePetSpritesheetPath(petDir, manifest.spritesheetPath);
  if (!spritesheetPath || !fileExists(spritesheetPath)) {
    errors.push(`${dirName}: missing ${manifest.spritesheetPath}`);
    return undefined;
  }

  return {
    id: manifest.id,
    display_name: manifest.displayName,
    description: manifest.description,
    manifest_path: manifestPath,
    spritesheet_path: spritesheetPath,
    spritesheet_url: renderableFileURL(spritesheetPath),
  };
}

function parseCodexPetManifest(raw: string): CodexPetManifest {
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    throw new Error("pet.json must be valid JSON");
  }
  if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
    throw new Error("pet.json must be an object");
  }
  const record = parsed as Record<string, unknown>;
  const id = stringField(record, "id");
  const displayName = stringField(record, "displayName");
  const description = typeof record.description === "string" ? record.description : "";
  const spritesheetPath = stringField(record, "spritesheetPath");
  return { id, displayName, description, spritesheetPath };
}

function stringField(record: Record<string, unknown>, key: string): string {
  const value = record[key];
  if (typeof value !== "string" || value.trim() === "") {
    throw new Error(`pet.json ${key} must be a non-empty string`);
  }
  return value.trim();
}

function resolvePetSpritesheetPath(petDir: string, spritesheetPath: string): string | undefined {
  if (isAbsolute(spritesheetPath)) {
    return undefined;
  }
  const resolved = normalize(join(petDir, spritesheetPath));
  const rel = relative(petDir, resolved);
  if (rel === "" || rel.startsWith("..") || isAbsolute(rel)) {
    return undefined;
  }
  return resolved;
}

function fileExists(path: string): boolean {
  try {
    return statSync(path).isFile();
  } catch {
    return false;
  }
}
