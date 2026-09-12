import { act } from "react";
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
      const thinkingFace = blobatarProps.mock.calls.at(-2)![0];
      const otherFace = blobatarProps.mock.calls.at(-1)![0];
      expect(first.querySelector('[data-agent-avatar-feedback="active"]')).not.toBeNull();
      expect(other.querySelector("[data-agent-avatar-feedback]")).toBeNull();

      await render("responding");
      const respondingFace = blobatarProps.mock.calls.at(-2)![0];
      expect(respondingFace.name).toBe(thinkingFace.name);
      expect(respondingFace.expression).not.toEqual(thinkingFace.expression);
      expect(blobatarProps.mock.calls.at(-1)![0].expression).toEqual(otherFace.expression);

      await render("idle");
      expect(container.querySelector('[data-agent-avatar-id="active-agent"]')).toBe(first);
      expect(first.querySelector("[data-agent-avatar-feedback]")).toBeNull();
      expect(blobatarProps.mock.calls.at(-2)![0].animate).toBe("hover");
    } finally {
      await act(async () => root.unmount());
    }
  });

  it("keeps a photo intact while replacing live feedback with a recoverable error and then clearing it", async () => {
    const container = document.createElement("div");
    const root = createRoot(container);
    const render = async (status: "thinking" | "failed" | "idle"): Promise<void> => {
      await act(async () => root.render(<AgentAvatarMark seed="photo-agent" avatarKey="abstract-1" avatarImage="data:image/png;base64,avatar" status={status} />));
    };
    try {
      await render("thinking");
      const photo = container.querySelector("img")!;
      expect(photo.getAttribute("src")).toBe("data:image/png;base64,avatar");
      expect(container.querySelector('[data-agent-avatar-feedback="active"]')).not.toBeNull();
      await render("failed");
      expect(container.querySelector("img")).toBe(photo);
      expect(container.querySelector('[data-agent-avatar-feedback="active"]')).toBeNull();
      expect(container.querySelector('[data-agent-avatar-feedback="failed"]')).not.toBeNull();
      await render("idle");
      expect(container.querySelector("img")).toBe(photo);
      expect(container.querySelector("[data-agent-avatar-feedback]")).toBeNull();
      expect(blobatarProps).not.toHaveBeenCalled();
    } finally {
      await act(async () => root.unmount());
    }
  });

  it("keeps queued, waiting and interrupted sessions distinct from active inference", () => {
    const faces: unknown[] = [];
    for (const status of ["queued", "waiting", "interrupted"] as const) {
      const html = renderToStaticMarkup(<AgentAvatarMark seed="agent" avatarKey="abstract-1" status={status} />);
      expect(html).not.toContain('data-agent-avatar-feedback="active"');
      const face = blobatarProps.mock.calls.at(-1)![0];
      expect(face.animate).toBe("hover");
      faces.push(face.expression);
    }
    expect(new Set(faces).size).toBe(3);
  });
});
