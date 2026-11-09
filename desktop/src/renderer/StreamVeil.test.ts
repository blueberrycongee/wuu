import { describe, expect, it } from "vitest";
import { StreamVeil } from "./StreamVeil";

describe("StreamVeil", () => {
  it("seeds history and fades only subsequent suffixes, including growth inside a word", () => {
    const veil = new StreamVeil("你 wor");
    expect(veil.advance("你 wor", 0)).toEqual([]);
    expect(veil.advance("你 world", 10)).toEqual([{ start: 5, end: 7, opacity: 0 }]);
    expect(veil.advance("你 world", 210)[0].opacity).toBeGreaterThan(0);
    expect(veil.advance("你 world", 410)).toEqual([]);
    expect(veil.advance("你 world", 1000)).toEqual([]);
  });
  it("preserves the unchanged prefix on replacement without bisecting emoji", () => {
    const veil = new StreamVeil("hi 😀 old");
    const spans = veil.advance("hi 😁 new", 0);
    expect(spans[0].start).toBe(3);
    expect("hi 😁 new".slice(spans[0].start)).toBe("😁 new");
    expect(veil.advance("hi", 10)).toEqual([]);
  });
  it("keeps a newly extended grapheme in one paint range", () => {
    const veil = new StreamVeil("hi 👩");
    expect(veil.advance("hi 👩‍💻", 0)[0].start).toBe(3);
    const accent = new StreamVeil("cafe");
    expect(accent.advance("café", 0)[0].start).toBe(3);
  });
  it("releases committed text while keeping its active fades and absolute offsets", () => {
    const veil = new StreamVeil("old ");
    veil.advance("old tail", 0);
    const before = veil.sample(100)[0];
    veil.discardPrefix(8);
    const next = veil.advance("new", 100);
    expect(next[0]).toEqual(before);
    expect(next[1]).toEqual({ start: 8, end: 11, opacity: 0 });
    expect(veil.sample(1000)).toEqual([]);
  });
  it("never reverses opacity as concurrent chunks finish", () => {
    const veil = new StreamVeil();
    let text = "";
    const seen = new Map<number, number>();
    for (let now = 0; now <= 1000; now += 10) {
      if (now % 70 === 0 && now < 300) text += "x";
      for (const span of veil.advance(text, now)) {
        expect(span.opacity).toBeGreaterThanOrEqual(seen.get(span.start) ?? 0);
        seen.set(span.start, span.opacity);
      }
    }
    expect(veil.advance(text, 1000)).toEqual([]);
  });
});
