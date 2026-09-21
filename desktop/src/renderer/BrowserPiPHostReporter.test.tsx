import { describe, expect, it } from "vitest";
import { hostLayoutMutationMatters, readBrowserPiPHostLayout } from "./BrowserPiPHostReporter";

describe("browser preview host layout", () => {
  it("reads the conversation column and ignores hidden obstacles", () => {
    document.body.innerHTML = `
      <div data-pip-anchor-host="conversation" id="host"></div>
      <div data-pip-obstacle="composer" id="composer"></div>
      <div data-pip-obstacle="hidden" id="hidden" hidden></div>
    `;
    const host = document.getElementById("host")!;
    const composer = document.getElementById("composer")!;
    viRect(host, { x: 10, y: 20, width: 400, height: 300 });
    viRect(composer, { x: 10, y: 280, width: 400, height: 40 });
    expect(readBrowserPiPHostLayout(document)).toEqual({
      host: { x: 10, y: 20, width: 400, height: 300 },
      obstacles: [{ x: 10, y: 280, width: 400, height: 40 }],
    });
  });

  it("ignores message mutations that do not touch the column or an obstacle", () => {
    document.body.innerHTML = `<div data-pip-anchor-host="conversation" id="host"></div>`;
    const message = document.createElement("p");
    message.textContent = "token";
    const record = { type: "childList", target: document.body, addedNodes: [message], removedNodes: [] } as unknown as MutationRecord;
    expect(hostLayoutMutationMatters([record])).toBe(false);
    const host = document.getElementById("host")!;
    const added = { type: "childList", target: document.body, addedNodes: [host], removedNodes: [] } as unknown as MutationRecord;
    expect(hostLayoutMutationMatters([added])).toBe(true);
  });
});

function viRect(element: HTMLElement, rect: { x: number; y: number; width: number; height: number }): void {
  element.getBoundingClientRect = () => ({
    ...rect,
    top: rect.y,
    left: rect.x,
    right: rect.x + rect.width,
    bottom: rect.y + rect.height,
    toJSON: () => ({}),
  });
}
