import { useCallback, useEffect, useRef, useState } from "react";
import type { ApplicationServiceV1 } from "../../../packages/plugin-sdk/src/ui";
import type { CodexPetSettingsUpdate, CodexPetsSnapshot, SettingsUsageResponse } from "../shared/protocol";
import { translateCurrent } from "./i18n";

export type SettingsRuntimeState = {
  settingsUsage: SettingsUsageResponse | undefined;
  settingsUsageLoading: boolean;
  settingsUsageError: string;
  codexPets: CodexPetsSnapshot | undefined;
  codexPetsLoading: boolean;
  codexPetsError: string;
  refreshCodexPets: () => Promise<CodexPetsSnapshot>;
  updateCodexPets: (settings: CodexPetSettingsUpdate) => Promise<CodexPetsSnapshot>;
};

export function useSettingsRuntimeState({ settingsOpen, application }: {
  settingsOpen: boolean; application: Pick<ApplicationServiceV1, "invoke">;
}): SettingsRuntimeState {
  const [settingsUsage, setSettingsUsage] = useState<SettingsUsageResponse>();
  const [settingsUsageLoading, setSettingsUsageLoading] = useState(false);
  const [settingsUsageError, setSettingsUsageError] = useState("");
  const [codexPets, setCodexPets] = useState<CodexPetsSnapshot>();
  const [codexPetsLoading, setCodexPetsLoading] = useState(true);
  const [codexPetsError, setCodexPetsError] = useState("");
  const active = useRef(true);
  useEffect(() => { active.current = true; return () => { active.current = false; }; }, []);

  const refreshCodexPets = useCallback(async (): Promise<CodexPetsSnapshot> => {
    setCodexPetsLoading(true); setCodexPetsError("");
    try {
      const snapshot = structuredClone(await application.invoke("settings.read-pets", {})) as CodexPetsSnapshot;
      if (active.current) setCodexPets(snapshot);
      return snapshot;
    } catch (error) {
      if (active.current) setCodexPetsError(error instanceof Error ? error.message : translateCurrent("settings.pets.readFailed"));
      throw error;
    } finally { if (active.current) setCodexPetsLoading(false); }
  }, [application]);

  const updateCodexPets = useCallback(async (settings: CodexPetSettingsUpdate): Promise<CodexPetsSnapshot> => {
    setCodexPetsLoading(true); setCodexPetsError("");
    try {
      const snapshot = structuredClone(await application.invoke("settings.update-pets", settings)) as CodexPetsSnapshot;
      if (active.current) setCodexPets(snapshot);
      return snapshot;
    } catch (error) {
      if (active.current) setCodexPetsError(error instanceof Error ? error.message : translateCurrent("settings.pets.saveFailed"));
      throw error;
    } finally { if (active.current) setCodexPetsLoading(false); }
  }, [application]);

  useEffect(() => {
    if (!settingsOpen) {
      setSettingsUsage(undefined); setSettingsUsageLoading(false); setSettingsUsageError("");
      return;
    }
    const lifetime = new AbortController();
    setSettingsUsageLoading(true); setSettingsUsageError("");
    void application.invoke("settings.read-usage", {}, { signal: lifetime.signal })
      .then(snapshot => { if (!lifetime.signal.aborted) setSettingsUsage(structuredClone(snapshot) as SettingsUsageResponse); })
      .catch(error => { if (!lifetime.signal.aborted) { setSettingsUsage(undefined); setSettingsUsageError(error instanceof Error ? error.message : translateCurrent("settings.usageLoadFailed")); } })
      .finally(() => { if (!lifetime.signal.aborted) setSettingsUsageLoading(false); });
    return () => lifetime.abort();
  }, [settingsOpen, application]);

  useEffect(() => {
    const lifetime = new AbortController();
    setCodexPetsLoading(true); setCodexPetsError("");
    void application.invoke("settings.read-pets", {}, { signal: lifetime.signal })
      .then(snapshot => { if (!lifetime.signal.aborted) setCodexPets(structuredClone(snapshot) as CodexPetsSnapshot); })
      .catch(error => { if (!lifetime.signal.aborted) setCodexPetsError(error instanceof Error ? error.message : translateCurrent("settings.pets.readFailed")); })
      .finally(() => { if (!lifetime.signal.aborted) setCodexPetsLoading(false); });
    return () => lifetime.abort();
  }, [application]);

  return { settingsUsage, settingsUsageLoading, settingsUsageError, codexPets, codexPetsLoading, codexPetsError,
    refreshCodexPets, updateCodexPets };
}
