import { describe, expect, it } from "vitest";
import { pairingURI } from "../src/lib/pairing";

describe("pairing input", () => {
  const uri = "wuu://pair?h=host&k=key&p=offer&r=ws%3A%2F%2F192.168.1.2%3A8787%2Fv1%2Fconnect&v=1";
  it("accepts camera links and raw pairing URIs without changing their contents", () => {
    expect(pairingURI(` http://192.168.1.2:8787/#${new URLSearchParams({ pair: uri })} `)).toBe(uri);
    expect(pairingURI(uri)).toBe(uri);
  });
  it("rejects a plain Web address or unrelated scheme", () => {
    expect(() => pairingURI("http://192.168.1.2:8787/")).toThrow();
    expect(() => pairingURI(`javascript:alert(1)#${new URLSearchParams({ pair: uri })}`)).toThrow();
  });
});
