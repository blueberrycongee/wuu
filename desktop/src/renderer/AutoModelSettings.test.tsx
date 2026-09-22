import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
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
  models: [{ id: "model" }],
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
let cleanup: (() => void) | undefined;
afterEach(() => {
  cleanup?.();
  cleanup = undefined;
});

function render(
  onSave: (value: RuntimeAdvancedSettingsUpdate) => Promise<void>,
  providers: ProviderSummary[] = [provider],
) {
  const container = document.createElement("div");
  document.body.append(container);
  const root = createRoot(container);
  act(() =>
    root.render(
      <I18nProvider>
        <WuuUIRoot>
          <AutoModelSettings
            value={policy}
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

it("saves disabled Auto without changing model references", async () => {
  const save = vi.fn().mockResolvedValue(undefined);
  const container = render(save);
  act(() =>
    container.querySelector<HTMLButtonElement>('[role="switch"]')!.click(),
  );
  await act(async () =>
    container
      .querySelector<HTMLButtonElement>(".settings-button-primary")!
      .click(),
  );
  expect(save).toHaveBeenCalledWith({
    auto_model: { ...policy, enabled: false, default: false },
  });
  expect(policy.enabled).toBe(true);
});

it("keeps the draft editable after a save failure", async () => {
  const save = vi.fn().mockRejectedValue(new Error("unavailable"));
  const container = render(save);
  await act(async () =>
    container
      .querySelector<HTMLButtonElement>(".settings-button-primary")!
      .click(),
  );
  expect(container.querySelector('[role="alert"]')).not.toBeNull();
  expect(
    container.querySelector<HTMLButtonElement>(".settings-button-primary")!
      .disabled,
  ).toBe(false);
  expect(
    container.querySelector('[role="switch"]')!.getAttribute("aria-checked"),
  ).toBe("true");
});

it("does not save enabled Auto with missing provider references", () => {
  const container = render(vi.fn(), []);
  expect(
    container.querySelector<HTMLButtonElement>(".settings-button-primary")!
      .disabled,
  ).toBe(true);
});
