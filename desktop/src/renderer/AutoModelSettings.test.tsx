import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { AutoModelSettings } from "./AutoModelSettings";
import { I18nProvider } from "./i18n";
import { WuuUIRoot } from "./ui/layers/UILayerHost";
import type {
  AutoModelConfig,
  ProviderSummary,
  RuntimeAdvancedSettingsUpdate,
} from "../shared/protocol";

const provider: ProviderSummary = {
  name: "service",
  type: "openai-compatible",
  model: "model",
  base_url: "https://example.invalid",
  api_key_configured: true,
  models: [{ id: "model", supported_efforts: ["low", "high"] }],
};
const selection = { provider: "service", model: "model" };
const policy: AutoModelConfig = {
  enabled: true,
  default: true,
  classifier: selection,
  simple: selection,
  medium: selection,
  complex: selection,
};
const originalScrollIntoView = Object.getOwnPropertyDescriptor(Element.prototype, "scrollIntoView");
const scrollIntoView = vi.fn();
beforeEach(() => {
  scrollIntoView.mockClear();
  Object.defineProperty(Element.prototype, "scrollIntoView", { configurable: true, value: scrollIntoView });
});
let cleanup: (() => void) | undefined;
afterEach(() => {
  cleanup?.();
  cleanup = undefined;
  vi.restoreAllMocks();
  if (originalScrollIntoView) Object.defineProperty(Element.prototype, "scrollIntoView", originalScrollIntoView);
  else Reflect.deleteProperty(Element.prototype, "scrollIntoView");
});

function render(
  onSave: (value: RuntimeAdvancedSettingsUpdate) => Promise<void>,
  providers: ProviderSummary[] = [provider],
  value: AutoModelConfig | null = policy,
) {
  const container = document.createElement("div");
  document.body.append(container);
  const root = createRoot(container);
  act(() =>
    root.render(
      <I18nProvider>
        <WuuUIRoot>
          <AutoModelSettings
            value={value ?? undefined}
            providers={providers}
            disabled={false}
            onSave={onSave}
          />
        </WuuUIRoot>
      </I18nProvider>,
    ),
  );
  cleanup = () => {
    act(() => root.unmount());
    container.remove();
  };
  return container;
}


async function select(container: HTMLElement, field: number, value: string) {
  act(() => container.querySelectorAll<HTMLButtonElement>('[aria-haspopup="menu"]')[field].click());
  const option = [...document.querySelectorAll<HTMLButtonElement>('[role="menuitemradio"]')]
    .find((item) => item.dataset.value === value)!;
  expect(option).toBeDefined();
  await act(async () => option.click());
}

it("hides selections when disabled and restores them when enabled", async () => {
  const save = vi.fn().mockResolvedValue(undefined);
  const container = render(save);
  await act(async () => container.querySelector<HTMLButtonElement>('[role="switch"]')!.click());
  expect(save).toHaveBeenCalledWith({
    auto_model: { ...policy, enabled: false, default: false },
  });
  expect(policy.enabled).toBe(true);
  expect(container.querySelector('[aria-haspopup="menu"]')).toBeNull();
  await act(async () => container.querySelector<HTMLButtonElement>('[role="switch"]')!.click());
  expect(container.querySelector('[aria-haspopup="menu"]')).not.toBeNull();
  expect(save).toHaveBeenLastCalledWith({ auto_model: { ...policy, default: false } });
});

it("restores the model after a failed save and allows retry", async () => {
  const save = vi.fn().mockRejectedValueOnce(new Error("unavailable")).mockResolvedValue(undefined);
  const other = { ...provider, name: "other-service" };
  const container = render(save, [provider, other]);
  const otherID = JSON.stringify([other.name, "model"]);
  await select(container, 0, otherID);
  expect(container.querySelector('[role="alert"]')).not.toBeNull();
  act(() => container.querySelector<HTMLButtonElement>('[aria-haspopup="menu"]')!.click());
  expect(document.querySelector('[role="menuitemradio"][aria-checked="true"]')?.getAttribute('data-value'))
    .toBe(JSON.stringify([provider.name, "model"]));
  await act(async () => [...document.querySelectorAll<HTMLButtonElement>('[role="menuitemradio"]')]
    .find((item) => item.dataset.value === otherID)!.click());
  expect(save).toHaveBeenCalledTimes(2);
  expect(container.querySelector('[role="alert"]')).toBeNull();
});

it("cannot enable Auto without providers", () => {
  const save = vi.fn();
  const container = render(save, [], { ...policy, enabled: false, default: false });
  const toggle = container.querySelector<HTMLButtonElement>('[role="switch"]')!;
  expect(toggle.disabled).toBe(true);
  act(() => toggle.click());
  expect(save).not.toHaveBeenCalled();
});

it("saves a provider and model atomically even when model IDs match", async () => {
  const save = vi.fn().mockResolvedValue(undefined);
  const other = { ...provider, name: "other-service" };
  const configured = { ...policy, simple: { ...selection, effort: "high", variant: "deep" } };
  const container = render(save, [provider, other], configured);
  await select(container, 2, JSON.stringify([other.name, "model"]));
  expect(save).toHaveBeenCalledWith({
    auto_model: { ...configured, simple: { provider: other.name, model: "model", effort: "", variant: "" } },
  });
});

it("saves reasoning while preserving other roles and the existing default preference", async () => {
  const save = vi.fn().mockResolvedValue(undefined);
  const configured = { ...policy, complex: { ...selection, model: "private-model", effort: "high" } };
  const container = render(save, [provider], configured);
  await select(container, 1, "high");
  expect(save).toHaveBeenCalledWith({
    auto_model: { ...configured, classifier: { ...selection, variant: "high", effort: "" } },
  });
});

it("does not save or reset reasoning when reselecting the current model", async () => {
  const save = vi.fn().mockResolvedValue(undefined);
  const configured = { ...policy, classifier: { ...selection, variant: "high" } };
  const container = render(save, [provider], configured);
  await select(container, 0, JSON.stringify([provider.name, "model"]));
  expect(save).not.toHaveBeenCalled();
  await select(container, 1, "low");
  expect(save).toHaveBeenCalledWith({
    auto_model: { ...configured, classifier: { ...selection, variant: "low", effort: "" } },
  });
});

it("locks controls during a save and bases the next write on the confirmed choice", async () => {
  let resolveSave!: () => void;
  const save = vi.fn().mockImplementationOnce(() => new Promise<void>((resolve) => { resolveSave = resolve; }))
    .mockResolvedValue(undefined);
  const other = { ...provider, name: "other-service" };
  const container = render(save, [provider, other]);
  await select(container, 0, JSON.stringify([other.name, "model"]));
  expect(save).toHaveBeenCalledTimes(1);
  expect([...container.querySelectorAll<HTMLButtonElement>('button')].every((button) => button.disabled)).toBe(true);
  act(() => container.querySelector<HTMLButtonElement>('[role="switch"]')!.click());
  expect(save).toHaveBeenCalledTimes(1);
  await act(async () => resolveSave());
  await select(container, 3, "low");
  expect(save).toHaveBeenLastCalledWith({ auto_model: {
    ...policy,
    classifier: { provider: other.name, model: "model", effort: "", variant: "" },
    simple: { ...selection, variant: "low", effort: "" },
  } });
});

it("initializes all roles with an execution model before enabling Auto", async () => {
  const save = vi.fn().mockResolvedValue(undefined);
  const container = render(save, [{ ...provider, model: "wuu/auto" }], null);
  await act(async () => container.querySelector<HTMLButtonElement>('[role="switch"]')!.click());
  expect(save).toHaveBeenCalledWith({ auto_model: { ...policy, enabled: true, default: false } });
});


it("restores visible selections when disabling fails", async () => {
  const save = vi.fn().mockRejectedValue(new Error("unavailable"));
  const container = render(save);
  await act(async () => container.querySelector<HTMLButtonElement>('[role="switch"]')!.click());
  expect(container.querySelector('[role="switch"]')?.getAttribute('aria-checked')).toBe('true');
  expect(container.querySelector('[aria-haspopup="menu"]')).not.toBeNull();
  expect(container.querySelector('[role="alert"]')).not.toBeNull();
});

it("repairs unavailable references on enable without replacing valid choices", async () => {
  const save = vi.fn().mockResolvedValue(undefined);
  const configured = {
    ...policy,
    enabled: false,
    default: false,
    classifier: { provider: "removed-service", model: "removed-model" },
    complex: { ...selection, variant: "high" },
  };
  const container = render(save, [provider], configured);
  expect(container.querySelector('[aria-haspopup="menu"]')).toBeNull();
  await act(async () => container.querySelector<HTMLButtonElement>('[role="switch"]')!.click());
  expect(save).toHaveBeenCalledWith({ auto_model: { ...configured, enabled: true, classifier: selection } });
  expect(container.querySelector('[aria-haspopup="menu"]')).not.toBeNull();
});


it("reveals the choices only when the user enables Auto", async () => {
  const container = render(vi.fn().mockResolvedValue(undefined));
  expect(scrollIntoView).not.toHaveBeenCalled();
  await select(container, 1, "high");
  expect(scrollIntoView).not.toHaveBeenCalled();
  const toggle = container.querySelector<HTMLButtonElement>('[role="switch"]')!;
  await act(async () => toggle.click());
  expect(scrollIntoView).not.toHaveBeenCalled();
  await act(async () => toggle.click());
  expect(scrollIntoView).toHaveBeenCalledTimes(1);
  expect(scrollIntoView).toHaveBeenCalledWith({ block: "nearest", inline: "nearest", behavior: "smooth" });
  expect(scrollIntoView.mock.instances[0].contains(container.querySelector('[aria-haspopup="menu"]'))).toBe(true);
});

it("reveals the choices without animation when reduced motion is requested", async () => {
  const media = window.matchMedia("(prefers-reduced-motion: reduce)");
  vi.spyOn(window, "matchMedia").mockReturnValue({ ...media, matches: true });
  const container = render(vi.fn().mockResolvedValue(undefined), [provider], { ...policy, enabled: false });
  await act(async () => container.querySelector<HTMLButtonElement>('[role="switch"]')!.click());
  expect(scrollIntoView).toHaveBeenCalledWith({ block: "nearest", inline: "nearest", behavior: "instant" });
});
