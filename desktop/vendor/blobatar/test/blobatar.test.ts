import { describe, expect, test } from "bun:test";
import { _layout, blobatar } from "../src/blobatar";
import { mad } from "../src/expression";
import { blobatarUri } from "../src/uri";
import { palette } from "../src/color";

const SEEDS = Array.from({ length: 300 }, (_, i) => `user-${i}`);

describe("output", () => {
  test("is well-formed SVG with no numeric leakage", () => {
    for (const s of SEEDS) {
      const svg = blobatar(s);
      expect(svg).toStartWith('<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100"');
      expect(svg).toEndWith("</svg>");
      expect(svg).not.toContain("NaN");
      expect(svg).not.toContain("undefined");
      expect(svg).not.toContain("Infinity");
    }
  });

  test("parses as XML", () => {
    // Bun ships no DOM parser, so lean on a structural check: tags balance.
    for (const s of SEEDS.slice(0, 50)) {
      const svg = blobatar(s);
      const open = (svg.match(/<(?!\/)[a-z]/g) ?? []).length;
      const close = (svg.match(/<\/[a-z]/g) ?? []).length + (svg.match(/\/>/g) ?? []).length;
      expect(open).toBe(close);
    }
  });

  test("emits no ids, so many blobatars on one page cannot collide", () => {
    for (const s of SEEDS.slice(0, 50)) {
      expect(blobatar(s)).not.toContain("id=");
      expect(blobatar(s)).not.toContain("url(#");
    }
  });
});

describe("options", () => {
  test("zero-strength perspective is byte-identical to the flat layout", () => {
    expect(blobatar("wuu", { perspective: { yaw: 30, pitch: 20, strength: 0 } })).toBe(
      blobatar("wuu"),
    );
  });

  test("sphere projection changes both the position and contour of the eyes", () => {
    const traits = { shape: 0.2, "body.ratio": 0.5 };
    const flat = _layout("wuu", { traits });
    const turnedLeft = _layout("wuu", {
      traits,
      perspective: { yaw: -20, pitch: 8, strength: 1 },
    });

    expect(turnedLeft.eyes[0]!.cx).toBeLessThan(flat.eyes[0]!.cx);
    expect(turnedLeft.eyes.map((e) => [e.rx, e.ry])).not.toEqual(
      flat.eyes.map((e) => [e.rx, e.ry]),
    );
    expect(
      blobatar("wuu", { traits, perspective: { yaw: -20, pitch: 8, strength: 1 } }),
    ).not.toContain("rotate(");
  });

  test("a baked pose still moves the projected eyes", () => {
    const posed = _layout("wuu", {
      traits: { shape: 0.2, "body.ratio": 0.5 },
      perspective: { yaw: -20, pitch: 8, strength: 1 },
      expression: mad,
    });
    expect(posed.eyes[0]!.cx).not.toBe(
      _layout("wuu", {
        traits: { shape: 0.2, "body.ratio": 0.5 },
        perspective: { yaw: -20, pitch: 8, strength: 1 },
      }).eyes[0]!.cx,
    );
  });

  test("size adds explicit dimensions", () => {
    expect(blobatar("a", { size: 64 })).toContain('width="64" height="64"');
    expect(blobatar("a")).not.toContain("width=");
  });

  test("background toggles the backdrop plate", () => {
    const on = blobatar("a", { background: true }).match(/<path/g)!.length;
    const off = blobatar("a", { background: false }).match(/<path/g)!.length;
    expect(on).toBe(off + 1);
  });

  test("the default is no backdrop at all", () => {
    // The body *is* the blobatar, so nothing is drawn behind it unless asked.
    expect(blobatar("a").match(/<path/g)!.length).toBe(
      blobatar("a", { background: false }).match(/<path/g)!.length,
    );
  });

  test("hue and tone lock color while leaving shape seed-driven", () => {
    // Feature presence varies by seed, so the *set* of colors used differs.
    // What must hold is that no color outside the locked palette appears.
    const allowed = new Set(Object.values(palette(200, true, 0.5)));
    for (const s of SEEDS.slice(0, 50)) {
      for (const hex of blobatar(s, { hue: 200, tone: 0.5 }).match(/#[0-9a-f]{6}/g) ?? []) {
        expect(allowed).toContain(hex);
      }
    }
    expect(blobatar("alain", { hue: 200, tone: 0.5 })).not.toBe(
      blobatar("bob", { hue: 200, tone: 0.5 }),
    );
  });

  test("palette overrides are applied verbatim", () => {
    expect(blobatar("a", { palette: { head: "#ff0000" } })).toContain("#ff0000");
  });

  test("title is escaped", () => {
    expect(blobatar("a", { title: "<script>&" })).toContain("<title>&lt;script&gt;&amp;</title>");
  });
});

describe("data uri", () => {
  test("escapes every character that would break an attribute or URL", () => {
    for (const s of SEEDS.slice(0, 50)) {
      const uri = blobatarUri(s);
      expect(uri).not.toContain('"');
      expect(uri).not.toContain("#");
      expect(uri).not.toContain("<");
    }
  });
});
