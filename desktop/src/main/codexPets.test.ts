import {
  existsSync,
  mkdirSync,
  mkdtempSync,
  rmSync,
  statSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import {
  defaultCodexPetsDir,
  ensureCodexPetsDir,
  loadCodexPetsSnapshot,
} from "./codexPets";

const roots: string[] = [];

afterEach(() => {
  for (const root of roots.splice(0)) {
    rmSync(root, { recursive: true, force: true });
  }
});

function createPetsRoot(): string {
  const root = mkdtempSync(join(tmpdir(), "wuu-codex-pets-"));
  roots.push(root);
  return root;
}

function writePet(
  root: string,
  id: string,
  overrides: Partial<{
    id: string;
    displayName: string;
    description: string;
    spritesheetPath: string;
  }> = {},
): void {
  const dir = join(root, id);
  mkdirSync(dir, { recursive: true });
  writeFileSync(
    join(dir, "pet.json"),
    `${JSON.stringify(
      {
        id,
        displayName: id,
        description: "",
        spritesheetPath: "spritesheet.webp",
        ...overrides,
      },
      null,
      2,
    )}\n`,
  );
  writeFileSync(join(dir, "spritesheet.webp"), "not-a-real-image");
}

describe("loadCodexPetsSnapshot", () => {
  it("defaults to Wuu's own pets directory", () => {
    const wuuHome = createPetsRoot();

    expect(defaultCodexPetsDir(wuuHome)).toBe(join(wuuHome, "pets"));
  });

  it("creates the Wuu pets directory on demand", () => {
    const root = createPetsRoot();
    const petsDir = join(root, "pets");

    expect(existsSync(petsDir)).toBe(false);
    ensureCodexPetsDir(petsDir);

    expect(statSync(petsDir).isDirectory()).toBe(true);
  });


  it("lists valid local pets and reports broken pet directories", () => {
    const root = createPetsRoot();
    writePet(root, "beta", { displayName: "Beta Pet", description: "second" });
    writePet(root, "alpha", { displayName: "Alpha Pet", description: "first" });
    mkdirSync(join(root, "broken"), { recursive: true });
    writeFileSync(join(root, "broken", "pet.json"), "{bad json");

    const snapshot = loadCodexPetsSnapshot({
      petsDir: root,
      settings: { enabled: true, selected_id: "beta" },
    });

    expect(snapshot.home).toBe(root);
    expect(snapshot.enabled).toBe(true);
    expect(snapshot.selected_id).toBe("beta");
    expect(snapshot.pets.map((pet) => pet.id)).toEqual(["alpha", "beta"]);
    expect(snapshot.pets[0]).toMatchObject({
      id: "alpha",
      display_name: "Alpha Pet",
      description: "first",
      spritesheet_path: join(root, "alpha", "spritesheet.webp"),
    });
    expect(snapshot.pets[0]?.spritesheet_url).toMatch(/^wuu-file:\/\/local\//);
    expect(snapshot.errors).toEqual([
      "broken: pet.json must be valid JSON",
    ]);
  });

  it("merges Wuu and Codex pet directories with Wuu pets taking precedence", () => {
    const wuuPetsRoot = createPetsRoot();
    const codexPetsRoot = createPetsRoot();
    writePet(wuuPetsRoot, "shared", { displayName: "Wuu Shared Pet" });
    writePet(wuuPetsRoot, "wuu-only", { displayName: "Wuu Only Pet" });
    writePet(codexPetsRoot, "shared", { displayName: "Codex Shared Pet" });
    writePet(codexPetsRoot, "codex-only", { displayName: "Codex Only Pet" });

    const snapshot = loadCodexPetsSnapshot({
      petsDirs: [wuuPetsRoot, codexPetsRoot],
      settings: { enabled: true, selected_id: "shared" },
    });

    expect(snapshot.home).toBe(wuuPetsRoot);
    expect(snapshot.selected_id).toBe("shared");
    expect(snapshot.enabled).toBe(true);
    expect(snapshot.pets.map((pet) => pet.id).sort()).toEqual([
      "codex-only",
      "shared",
      "wuu-only",
    ]);
    expect(snapshot.pets.find((pet) => pet.id === "shared")?.display_name).toBe("Wuu Shared Pet");
  });

  it("falls back to the first available pet when the stored selection is gone", () => {
    const root = createPetsRoot();
    writePet(root, "alpha", { displayName: "Alpha Pet" });
    writePet(root, "beta", { displayName: "Beta Pet" });

    const snapshot = loadCodexPetsSnapshot({
      petsDir: root,
      settings: { enabled: true, selected_id: "missing" },
    });

    expect(snapshot.enabled).toBe(true);
    expect(snapshot.selected_id).toBe("alpha");
  });

  it("disables the layer when no valid pets are installed", () => {
    const root = createPetsRoot();

    const snapshot = loadCodexPetsSnapshot({
      petsDir: root,
      settings: { enabled: true, selected_id: "missing" },
    });

    expect(snapshot.enabled).toBe(false);
    expect(snapshot.selected_id).toBe("");
    expect(snapshot.pets).toEqual([]);
  });
});
