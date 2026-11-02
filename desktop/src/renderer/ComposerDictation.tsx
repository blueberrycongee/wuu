import { useEffect, useRef, useState } from "react";
import type { DesktopDictationAPI, DictationEvent } from "../shared/protocol";
import { useI18n } from "./i18n";
import { Mic, Square, X, LoaderCircle } from "./WuuIcons";
import { Tooltip } from "./Tooltip";
import "./styles/composer-dictation.css";

type Phase = "idle" | "starting" | "listening" | "finalizing";
export function useComposerDictation({ owner, disabled, prompt, locale, onText }: {
  owner: string; disabled: boolean; prompt: string; locale: string;
  onText(text: string, send: boolean): void;
}) {
  const api = window.wuu?.dictation;
  const { t } = useI18n();
  const [phase, setPhase] = useState<Phase>("idle");
  const [error, setError] = useState("");
  const current = useRef<{ id: string; base: string; text: string; send: boolean; stopping: boolean } | undefined>(undefined);
  const latest = useRef({ onText, owner, disabled });
  latest.current = { onText, owner, disabled };
  const [sessionId, setSessionId] = useState("");

  useEffect(() => {
    if (!api || disabled) return;
    const capturedOwner = owner;
    const unsubscribe = api.onEvent(event => {
      const session = current.current;
      if (!session || session.id !== event.sessionId || latest.current.owner !== capturedOwner || latest.current.disabled) return;
      if (event.type === "cancelled") {
        current.current = undefined;
        setPhase("idle");
      } else if (event.type === "state") {
        if (!session.stopping) setPhase(event.state === "listening" ? "listening" : event.state === "finalizing" ? "finalizing" : "starting");
      } else if (event.type === "result") {
        session.text = event.text;
        if (event.is_final) finish(session.send);
      } else if (event.type === "error") {
        finish(false);
        const key = event.code === "microphone_permission_denied" ? "composer.voice.microphoneDenied"
          : event.code.startsWith("speech_permission") ? "composer.voice.speechDenied"
          : event.code === "on_device_unavailable" ? "composer.voice.onDeviceUnavailable"
          : event.code === "locale_unavailable" ? "composer.voice.localeUnavailable" : "composer.voice.systemUnavailable";
        setError(t(key));
      }
      function finish(send: boolean): void {
        if (!session) return;
        current.current = undefined;
        setPhase("idle");
        const text = session.text.trim();
        if (text) latest.current.onText(session.base + (session.base && !/\s$/.test(session.base) ? "\n" : "") + text, send);
        else if (event.type !== "error") setError(t("composer.voice.noSpeech"));
      }
    });
    return () => {
      unsubscribe();
      const session = current.current;
      current.current = undefined;
      if (session) void api.cancel(session.id).catch(() => {});
      setPhase("idle");
      setError("");
    };
  }, [api, owner, disabled, t]);

  async function start(): Promise<void> {
    if (!api || disabled || current.current) return;
    const id = crypto.randomUUID();
    current.current = { id, base: prompt, text: "", send: false, stopping: false };
    setSessionId(id); setError(""); setPhase("starting");
    try { await api.start(id, locale); }
    catch {
      if (current.current?.id !== id) return;
      current.current = undefined; setPhase("idle"); setError(t("composer.voice.systemUnavailable"));
    }
  }
  async function stop(send = false): Promise<void> {
    const session = current.current;
    if (!api || !session || session.stopping || phase !== "listening") return;
    session.send = send; session.stopping = true; setPhase("finalizing");
    try { await api.stop(session.id); }
    catch {
      if (current.current !== session) return;
      session.send = false;
      setError(t("composer.voice.systemUnavailable"));
      cancel();
    }
  }
  function cancel(): void {
    const session = current.current;
    current.current = undefined; setPhase("idle");
    if (session && api) void api.cancel(session.id).catch(() => {});
  }
  return { api, phase, sessionId, error, active: phase !== "idle", start, stop, cancel };
}

export function DictationButton({ disabled, start }: { disabled: boolean; start(): Promise<void> }): JSX.Element {
  const { t } = useI18n();
  return <Tooltip asChild content={t("composer.voice.start")}><button type="button" className="composer-action-button composer-dictation-button"
    data-wuu-component="composer-dictation" aria-label={t("composer.voice.start")} disabled={disabled} onClick={() => void start()}><Mic /></button></Tooltip>;
}

export function DictationRecording({ api, sessionId, phase, stop, cancel }: {
  api: DesktopDictationAPI; sessionId: string; phase: Phase; stop(send?: boolean): Promise<void>; cancel(): void;
}): JSX.Element {
  const { t } = useI18n();
  const [levels, setLevels] = useState<number[]>(() => Array(48).fill(0));
  const [seconds, setSeconds] = useState(0);
  useEffect(() => api.onEvent((event: DictationEvent) => {
    if (event.sessionId === sessionId && event.type === "level") {
      const level = Math.max(0, Math.min(1, event.level));
      setLevels(previous => [...previous.slice(1), level]);
    }
  }), [api, sessionId]);
  useEffect(() => {
    if (phase !== "listening") return;
    const began = Date.now();
    const timer = setInterval(() => setSeconds(Math.floor((Date.now() - began) / 1000)), 250);
    return () => clearInterval(timer);
  }, [phase]);
  const listening = phase === "listening";
  const status = t(listening ? "composer.voice.listening" : phase === "finalizing" ? "composer.voice.transcribing" : "composer.voice.preparing");
  return <div className="composer-dictation-recording" data-phase={phase}>
    <Tooltip asChild content={t("composer.voice.cancel")}><button type="button" className="composer-action-button" aria-label={t("composer.voice.cancel")} onClick={cancel}><X /></button></Tooltip>
    <div className="composer-dictation-status">
      <span className="composer-dictation-dot" aria-hidden="true" />
      {!listening && <span className="composer-dictation-status-label">{status}</span>}
      {listening && <span className="composer-dictation-timer">{Math.floor(seconds / 60)}:{String(seconds % 60).padStart(2, "0")}</span>}
      <span className="sr-only" role="status" aria-live="polite">{status}</span>
    </div>
    <div className="composer-dictation-wave" aria-hidden="true">{levels.map((level, index) => <span key={index} style={{ transform: `scaleY(${0.08 + level * 0.92})`, opacity: 0.3 + level * 0.7 }} />)}</div>
    <Tooltip asChild content={t("composer.voice.stop")}><button type="button" className="composer-action-button composer-dictation-stop" aria-label={t("composer.voice.stop")} disabled={!listening} onClick={() => void stop()}>
      {listening ? <Square /> : <LoaderCircle className="is-spinning" />}
    </button></Tooltip>
  </div>;
}
