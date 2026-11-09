import type { DesktopDictationAPI, DictationEvent } from "../../src/shared/protocol";

// Synthetic speech events exercise the real composer without microphone access.
const listeners = new Set<(event: DictationEvent) => void>();
let active = "", timer: ReturnType<typeof setInterval> | undefined, tick = 0;
let stopped = false, autoFinish = true;
const transcript = "帮我整理一下今天的工作，列出接下来需要处理的事情。";
const emit = (event: DictationEvent) => listeners.forEach(listener => listener(event));
const finish = () => { if (active) emit({ sessionId: active, type: "result", text: transcript, is_final: true }); };
const api: DesktopDictationAPI = {
  async start(sessionId) {
    clearInterval(timer); active = sessionId; tick = 0; stopped = false;
    emit({ sessionId, type: "state", state: "listening" });
    timer = setInterval(() => {
      tick++;
      const level = Math.max(0, Math.sin(tick * .12) * .45 + Math.sin(tick * .53) * .25 + .25);
      emit({ sessionId, type: "level", level });
      emit({ sessionId, type: "result", text: transcript.slice(0, Math.min(transcript.length - 4, Math.floor(tick / 3))), is_final: false });
    }, 50);
  },
  async stop(sessionId) {
    if (sessionId !== active) return;
    clearInterval(timer); stopped = true;
    emit({ sessionId, type: "state", state: "finalizing" });
    if (autoFinish) setTimeout(() => { if (active === sessionId) finish(); }, 700);
  },
  async cancel(sessionId) { if (sessionId === active) { active = ""; clearInterval(timer); } },
  onEvent(listener) { listeners.add(listener); return () => { listeners.delete(listener); }; },
};
Object.assign(window, { wuu: { dictation: api, initialLanguagePreference: "zh-CN" }, dictationAcceptance: {
  finish, transcript, emit, get active() { return active; }, get stopped() { return stopped; },
  set autoFinish(value: boolean) { autoFinish = value; },
} });
const caption = document.createElement("div");
caption.textContent = "听写交互预览 · 使用模拟语音，可点击麦克风、停止和发送";
caption.style.cssText = "position:fixed;top:48px;left:24px;right:24px;text-align:center;color:var(--ink-muted);font:15px system-ui";
document.body.append(caption);
