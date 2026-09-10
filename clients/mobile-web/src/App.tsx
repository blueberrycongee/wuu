import { lazy, Suspense, useEffect, useRef, useState, useSyncExternalStore } from "react";
import type { Credentials } from "@wuu/remote-core";
import { WorkbenchConnectionContext } from "../../../desktop/src/renderer/WorkbenchConnectionContext";

import { webCredStore } from "./lib/credStore";
import { RemoteDesktopBridge } from "./lib/desktopBridge";
import { pairingURI, pairingExpired, pairingMatchesHost } from "./lib/pairing";
import { ArrowLeft } from 'lucide-react';
import { ViewSwitchLoading } from '../../../desktop/src/renderer/LoadingViews';
import { isNative } from './lib/native';

const SharedWorkbench = lazy(() => import("./WebWorkspace"));

type Phase =
  | { kind: "boot" }
  | { kind: "pair"; error?: string }
  | { kind: "connecting" }
  | { kind: "expired" }
  | { kind: "ready" }
  | { kind: "error"; message: string };

export default function App({ onAccountBack }: { onAccountBack?: () => void } = {}): React.JSX.Element {
  const [phase, setPhase] = useState<Phase>({ kind: "boot" });
  const [scannedPair] = useState(() => new URLSearchParams(window.location.hash.slice(1)).get("pair"));
  const bridgeRef = useRef<RemoteDesktopBridge | null>(null);
  const connectionAttemptRef = useRef(0);

  useEffect(() => {
    const wake = (): void => {
      if (document.visibilityState === "visible") bridgeRef.current?.wake();
    };
    document.addEventListener("visibilitychange", wake);
    window.addEventListener("pageshow", wake);
    window.addEventListener("online", wake);
    window.addEventListener("focus", wake);
    const suspend = () => bridgeRef.current?.suspend();
    window.addEventListener('wuu:background', suspend);
    return () => {
      document.removeEventListener("visibilitychange", wake);
      window.removeEventListener("pageshow", wake);
      window.removeEventListener("online", wake);
      window.removeEventListener("focus", wake);
      window.removeEventListener('wuu:background', suspend);
    };
  }, []);

  const connect = async (credentials: Credentials): Promise<void> => {
    const attempt = ++connectionAttemptRef.current;
    setPhase({ kind: "connecting" });
    const previous = bridgeRef.current;
    const bridge = new RemoteDesktopBridge(credentials);
    bridgeRef.current = bridge;
    if (previous) void previous.disconnect().catch(() => {});
    try {
      await bridge.connect();
      if (attempt !== connectionAttemptRef.current) {
        await bridge.disconnect().catch(() => {});
        return;
      }
      bridge.install();
      setPhase({ kind: "ready" });
    } catch (error) {
      await bridge.disconnect().catch(() => {});
      if (attempt !== connectionAttemptRef.current) return;
      bridgeRef.current = null;
      setPhase({
        kind: "error",
        message: error instanceof Error ? error.message : "无法连接到 Wuu",
      });
    }
  };

  const resetPairing = async (): Promise<void> => {
    if (onAccountBack) { onAccountBack(); return; }
    connectionAttemptRef.current += 1;
    const bridge = bridgeRef.current;
    bridgeRef.current = null;
    setPhase({ kind: "pair" });
    if (bridge) void bridge.disconnect().catch(() => {});
    try {
      await webCredStore.clear();
    } catch (error) {
      setPhase({ kind: "pair", error: error instanceof Error ? error.message : "无法清除配对信息" });
    }
  };

  useEffect(() => {
    let active = true;
    const attempt = ++connectionAttemptRef.current;
    if (scannedPair) {
      // The fragment never reaches the HTTP server; remove it from browser
      // history before exchanging the single-use pairing offer.
      window.history.replaceState(null, "", window.location.pathname + window.location.search);
      setPhase({ kind: "connecting" });
    }
    void webCredStore.load().then(async (credentials) => {
      if (!active || attempt !== connectionAttemptRef.current) return;
      // Reopening a single-use invitation must not replace an existing device
      // identity. Keep its saved relay too; old links can contain stale addresses.
      if (credentials && (!scannedPair || pairingMatchesHost(scannedPair, credentials.host_pub))) {
        await connect(credentials);
      } else if (scannedPair) {
        try {
          const paired = await RemoteDesktopBridge.pair(scannedPair, "手机浏览器");
          if (!active || attempt !== connectionAttemptRef.current) return;
          await webCredStore.save(paired);
          if (active && attempt === connectionAttemptRef.current) await connect(paired);
        } catch (error) {
          if (active && attempt === connectionAttemptRef.current) setPhase(pairingExpired(error) ? { kind: "expired" } : { kind: "pair", error: error instanceof Error ? error.message : "配对失败，请在电脑上重新生成二维码" });
        }
      } else setPhase({ kind: "pair" });
    }).catch((error) => {
      if (active && attempt === connectionAttemptRef.current) setPhase({ kind: "error", message: error instanceof Error ? error.message : "无法读取配对信息" });
    });
    return () => {
      active = false;
      connectionAttemptRef.current += 1;
      const bridge = bridgeRef.current;
      bridgeRef.current = null;
      if (bridge) void bridge.disconnect().catch(() => {});
    };
  }, []);

  if (phase.kind === "ready") {
    return (
      <Suspense fallback={<StatusCard title="正在载入工作台…" />}>
        <ConnectedWorkbench bridge={bridgeRef.current!} onReset={() => void resetPairing()} resetLabel={onAccountBack ? "返回电脑列表" : "重新配对"} />
      </Suspense>
    );
  }

  if (phase.kind === "pair") {
    return (
      <PairCard
        onBack={onAccountBack}
        error={phase.error}
        onPair={async (uri, name) => {
          const attempt = ++connectionAttemptRef.current;
          setPhase({ kind: "connecting" });
          try {
            const credentials = await RemoteDesktopBridge.pair(pairingURI(uri), name);
            if (attempt !== connectionAttemptRef.current) return;
            await webCredStore.save(credentials);
            if (attempt !== connectionAttemptRef.current) return;
            await connect(credentials);
          } catch (error) {
            if (attempt !== connectionAttemptRef.current) return;
            setPhase(pairingExpired(error) ? { kind: "expired" } : {
              kind: "pair",
              error: error instanceof Error ? error.message : "配对失败",
            });
          }
        }}
      />
    );
  }

  if (phase.kind === "error") {
    return (
      <StatusCard title="连接失败" detail={phase.message}>
        <button type="button" onClick={() => void webCredStore.load().then((credentials) => {
          if (credentials) return connect(credentials);
          setPhase({ kind: "pair" });
        }).catch((error) => setPhase({ kind: "error", message: String(error) }))}>
          重试连接
        </button>
        <button
          type="button"
          onClick={() => void resetPairing()}
        >
          {onAccountBack ? "返回电脑列表" : "重新配对"}
        </button>
      </StatusCard>
    );
  }

  if (phase.kind === "expired") {
    return (
      <StatusCard title="配对码已失效">
        <button type="button" onClick={() => setPhase({ kind: "pair" })}>粘贴新的配对链接</button>
      </StatusCard>
    );
  }

  if (phase.kind === "connecting") {
    return (
      <StatusCard title="正在连接电脑…">
        <button type="button" onClick={() => void resetPairing()}>
          {onAccountBack ? "返回电脑列表" : "清除旧配对"}
        </button>
      </StatusCard>
    );
  }

  return <StatusCard title="正在启动…" />;
}

function PairCard({
  error,
  onPair,
  onBack,
}: {
  error?: string;
  onPair: (uri: string, name: string) => Promise<void>;
  onBack?: () => void;
}): React.JSX.Element {
  const [uri, setURI] = useState("");
  const [name, setName] = useState(isNative ? "Wuu 手机" : "手机浏览器");
  return (
    <main className="web-gate">
      <form className="web-gate-card web-pair-card" onSubmit={event => { event.preventDefault(); if (uri.trim() && name.trim()) void onPair(uri.trim(), name.trim()); }}>
        <header className="web-gate-header">
          {onBack && <button className="web-gate-back" type="button" aria-label="返回" onClick={onBack}><ArrowLeft size={22} /></button>}
          <h1>配对电脑</h1>
        </header>
        <div className="web-pair-body">
        <label>
          <span>配对链接</span>
          <textarea
            value={uri}
            onChange={(event) => setURI(event.target.value)}
            placeholder="粘贴电脑上复制的完整链接"
            rows={3}
            autoCapitalize="none"
            autoCorrect="off"
            spellCheck={false}
          />
        </label>
        <details className="web-pair-options"><summary>设备名称</summary>
          <input aria-label="设备名称" value={name} maxLength={64} onChange={(event) => setName(event.target.value)} />
        </details>
        {error ? <p className="web-gate-error">{error}</p> : null}
        <button
          type="submit"
          disabled={!uri.trim() || !name.trim()}
        >
          连接电脑
        </button>
        </div>
      </form>
    </main>
  );
}

function StatusCard({
  title,
  detail,
  children,
}: {
  title: string;
  detail?: string;
  children?: React.ReactNode;
}): React.JSX.Element {
  return (
    <main className="web-gate">
      <section className="web-gate-card web-gate-status">
        {title.startsWith('正在') && <ViewSwitchLoading compact />}
        <h1 role="status">{title}</h1>
        {detail ? <p className="web-gate-detail">{detail}</p> : null}
        {children}
      </section>
    </main>
  );
}


function ConnectedWorkbench({ bridge, onReset, resetLabel }: {
  bridge: RemoteDesktopBridge;
  onReset: () => void;
  resetLabel: string;
}): React.JSX.Element {
  const connection = useSyncExternalStore(bridge.subscribeConnection, bridge.getConnectionSnapshot);
  const ready = connection.phase === "connected";
  return (
    <>
      <div className="web-workbench">
        <WorkbenchConnectionContext.Provider value={ready}>
          <SharedWorkbench />
        </WorkbenchConnectionContext.Provider>
      </div>
      {!ready ? (
        <aside className="web-connection-status" role="status" aria-live="polite">
          <span>{connection.phase === "restoring" ? "正在恢复工作区…" : connection.phase === "error"
            ? `恢复失败：${connection.error}` : "与电脑的连接已断开，正在重连…"}</span>
          {connection.phase === "error" ? (
            <button type="button" onClick={() => void bridge.retryRestore().catch(() => {})}>重试恢复</button>
          ) : null}
          <button type="button" onClick={onReset}>{resetLabel}</button>
        </aside>
      ) : null}
    </>
  );
}
