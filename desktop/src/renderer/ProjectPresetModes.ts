import type { ProjectPresetMode } from "../shared/protocol";

export const PROJECT_PRESET_MODES: readonly ProjectPresetMode[] = ["low", "medium", "high", "ultra"];
export const DEFAULT_PROJECT_PRESET: ProjectPresetMode = "low";

export const PROJECT_PRESET_ROLES = [
  { id: "coordinator", label: "settings.projectCoordinatorModel" },
  { id: "technical_lead", label: "settings.projectTechnicalLeadModel" },
  { id: "executor", label: "settings.projectExecutorModel" },
] as const;
