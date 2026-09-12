import { act } from "react";
import { _layout } from "blobatar";
import { createRoot } from "react-dom/client";
import { renderToStaticMarkup } from "react-dom/server";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { AgentAvatarMark } from "./AgentAvatarMark";

const blobatarProps = vi.hoisted(() => vi.fn());

vi.mock("blobatar/react", () => ({
  Blobatar: (props: unknown) => {
    blobatarProps(props);
    return null;
  },
}));

describe("AgentAvatarMark", () => {
  beforeEach(() => blobatarProps.mockClear());

  it.each(["idle", "thinking", "sending"] as const)(
    "renders the %s blob without a backdrop",
    (status) => {
      renderToStaticMarkup(
        <AgentAvatarMark seed="agent-1" avatarKey="abstract-1" status={status} />,
      );

      expect(blobatarProps).toHaveBeenCalledOnce();
      expect(blobatarProps.mock.calls[0][0]).toEqual(
        expect.objectContaining({ background: false }),
      );
    },
  );

  it("keeps idle avatars still and lets only working avatars animate", () => {
    renderToStaticMarkup(
      <AgentAvatarMark seed="agent-1" avatarKey="abstract-1" status="idle" />,
    );
    expect(blobatarProps.mock.calls[0][0]).toEqual(
      expect.objectContaining({ animate: "hover" }),
    );

    blobatarProps.mockClear();
    renderToStaticMarkup(
      <AgentAvatarMark seed="agent-1" avatarKey="abstract-1" status="thinking" />,
    );
    expect(blobatarProps.mock.calls[0][0]).toEqual(
      expect.objectContaining({ animate: "always" }),
    );

    blobatarProps.mockClear();
    renderToStaticMarkup(
      <AgentAvatarMark seed="agent-1" avatarKey="abstract-1" status="sending" />,
    );
    expect(blobatarProps.mock.calls[0][0]).toEqual(
      expect.objectContaining({ animate: "always" }),
    );
  });

  it("updates one identity through a reply without changing the other avatar or its identity", async () => {
    const container = document.createElement("div");
    const root = createRoot(container);
    const faceFor = (key: string) => blobatarProps.mock.calls.map(([props]) => props).reverse().find(props => props.name === `agent-avatar:${key}`);
    const render = async (status: "thinking" | "responding" | "idle"): Promise<void> => {
      await act(async () => root.render(<>
        <AgentAvatarMark seed="active-agent" avatarKey="abstract-1" status={status} />
        <AgentAvatarMark seed="other-agent" avatarKey="abstract-2" status="idle" />
      </>));
    };
    try {
      await render("thinking");
      const first = container.querySelector('[data-agent-avatar-id="active-agent"]')!;
      const other = container.querySelector('[data-agent-avatar-id="other-agent"]')!;
      const thinkingFace = faceFor("abstract-1");
      const otherFace = faceFor("abstract-2");

      await render("responding");
      const respondingFace = faceFor("abstract-1");
      expect(respondingFace.name).toBe(thinkingFace.name);
      expect(respondingFace.expression).not.toEqual(thinkingFace.expression);
      expect(faceFor("abstract-2").expression).toEqual(otherFace.expression);

      await render("idle");
      expect(container.querySelector('[data-agent-avatar-id="active-agent"]')).toBe(first);
      expect(faceFor("abstract-1").animate).toBe("hover");
    } finally {
      await act(async () => root.unmount());
    }
  });

  it("turns once on entering work, retains the turn through a reply and cancels for user attention", async () => {
    vi.useFakeTimers();
    const container = document.createElement("div");
    const root = createRoot(container);
    const render = async (status: "idle" | "thinking" | "responding" | "failed") => {
      await act(async () => root.render(<>
        <AgentAvatarMark seed="live" avatarKey="abstract-1" status={status} />
        <AgentAvatarMark seed="sidebar" avatarKey="abstract-1" status={status} motion="subtle" />
      </>));
    };
    try {
      await render("idle");
      await render("thinking");
      const live = container.querySelector('[data-agent-avatar-id="live"]')!;
      const ribbon = live.querySelector(".agent-avatar-ribbon");
      expect(ribbon).not.toBeNull();
      expect(container.querySelector('[data-agent-avatar-id="sidebar"] .agent-avatar-ribbon')).toBeNull();
      await act(async () => vi.advanceTimersByTime(500));
      await render("responding");
      expect(live.querySelector(".agent-avatar-ribbon")).toBe(ribbon);
      await act(async () => vi.advanceTimersByTime(700));
      expect(live.querySelector(".agent-avatar-ribbon")).toBeNull();
      await render("thinking");
      expect(live.querySelector(".agent-avatar-ribbon")).toBeNull();
      await render("idle");
      await render("thinking");
      expect(live.querySelector(".agent-avatar-ribbon")).not.toBeNull();
      await render("failed");
      expect(live.querySelector(".agent-avatar-ribbon")).toBeNull();
      expect(live.getAttribute("data-agent-avatar-state")).toBe("failed");
    } finally {
      await act(async () => root.unmount());
      vi.useRealTimers();
    }
  });

  it("keeps an uploaded identity intact across work, error and recovery", async () => {
    const container = document.createElement("div");
    const root = createRoot(container);
    const render = async (status: "thinking" | "failed" | "idle"): Promise<void> => {
      await act(async () => root.render(<AgentAvatarMark seed="photo-agent" avatarKey="abstract-1" avatarImage="data:image/png;base64,avatar" status={status} />));
    };
    try {
      await render("thinking");
      const photo = container.querySelector("img")!;
      expect(photo.getAttribute("src")).toBe("data:image/png;base64,avatar");
      await render("failed");
      expect(container.querySelector("img")).toBe(photo);
      await render("idle");
      expect(container.querySelector("img")).toBe(photo);
      expect(blobatarProps).not.toHaveBeenCalled();
    } finally {
      await act(async () => root.unmount());
    }
  });

  it("keeps queued, waiting and interrupted sessions distinct from active inference", () => {
    const faces: unknown[] = [];
    for (const status of ["queued", "waiting", "interrupted"] as const) {
      renderToStaticMarkup(<AgentAvatarMark seed="agent" avatarKey="abstract-1" status={status} />);
      const face = blobatarProps.mock.calls.at(-1)![0];
      expect(face.animate).toBe("hover");
      faces.push(face.expression);
    }
    expect(new Set(faces).size).toBe(3);
  });

  it("keeps eyes separated and inside the body across reply and recovery expressions", () => {
    for (const status of ["idle", "thinking", "responding", "sending", "queued", "waiting", "failed", "interrupted"] as const) {
      renderToStaticMarkup(<AgentAvatarMark seed="agent" avatarKey="abstract-1" status={status} />);
      const face = blobatarProps.mock.calls.at(-1)![0];
      const authored = _layout(face.name, { traits: face.traits });
      const posed = face.expression.bake(authored, face.expression.p).l;
      expect(posed.eyes[0].cx + posed.eyes[0].rx).toBeLessThan(posed.eyes[1].cx - posed.eyes[1].rx);
      for (const eye of posed.eyes) {
        expect(eye.rx, status).toBeGreaterThan(0);
        expect(eye.ry, status).toBeGreaterThan(0);
        expect(Math.abs(eye.cy - authored.body.cy) + eye.ry, status).toBeLessThan(authored.body.ry);
      }
    }
  });
});
