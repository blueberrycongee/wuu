import { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";

type Directory = {
  path: string;
  parent: string;
  folders: Array<{ name: string; path: string }>;
  archives?: Array<{ name: string; path: string }>;
  truncated: boolean;
};
type Request = <T>(method: string, params?: unknown) => Promise<T>;
export function pickComputerFolder(
  request: Request,
  create = false,
  packageMode = false,
): Promise<string | null> {
  return new Promise((resolve) => {
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    const finish = (path: string | null) => {
      root.unmount();
      container.remove();
      resolve(path);
    };
    root.render(<Picker request={request} create={create} packageMode={packageMode} finish={finish} />);
  });
}
function Picker({
  request,
  create,
  packageMode,
  finish,
}: {
  request: Request;
  create: boolean;
  packageMode: boolean;
  finish: (path: string | null) => void;
}) {
  const [directory, setDirectory] = useState<Directory>();
  const [path, setPath] = useState("");
  const [name, setName] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const load = async (path?: string) => {
    setBusy(true);
    setError("");
    try {
      const d = await request<Directory>("desktop/projects/folders", { path, includeArchives: packageMode });
      setDirectory(d);
      setPath(d.path);
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  };
  useEffect(() => {
    void load();
    const close = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.preventDefault();
        finish(null);
      }
    };
    window.addEventListener("keydown", close);
    return () => window.removeEventListener("keydown", close);
  }, []);
  return (
    <div
      className="computer-folder-overlay"
      role="dialog"
      aria-modal="true"
      aria-label={packageMode ? "选择电脑上的扩展包" : "选择电脑文件夹"}
    >
      <section className="computer-folder-picker">
        <h2>{packageMode ? "选择电脑上的扩展包" : "选择电脑文件夹"}</h2>
        {error && <p role="alert">{error}</p>}
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void load(path);
          }}
        >
          <label>
            电脑路径
            <input value={path} onChange={(e) => setPath(e.target.value)} />
          </label>
          <button disabled={busy}>前往</button>
        </form>
        <button
          disabled={busy || !directory || directory.path === directory.parent}
          onClick={() => void load(directory?.parent)}
        >
          上一级
        </button>
        <div className="computer-folder-list">
          {directory?.folders.map((d) => (
            <button
              key={d.path}
              disabled={busy}
              onClick={() => void load(d.path)}
            >
              {d.name} ›
            </button>
          ))}
          {packageMode && directory?.archives?.map(file => <button key={file.path} disabled={busy} onClick={() => finish(file.path)}>{file.name}</button>)}
        </div>
        {directory?.truncated && <p>仅显示部分文件夹</p>}
        {create && (
          <form
            onSubmit={(e) => {
              e.preventDefault();
              setBusy(true);
              void request<{ path: string }>("desktop/projects/mkdir", {
                path: directory?.path,
                name,
              })
                .then((d) => load(d.path))
                .catch((e) => setError(String(e)))
                .finally(() => setBusy(false));
            }}
          >
            <label>
              新文件夹名称
              <input
                value={name}
                onChange={(e) => setName(e.target.value)}
                required
              />
            </label>
            <button disabled={busy || !directory}>创建文件夹</button>
          </form>
        )}
        <footer>
          <button onClick={() => finish(null)}>取消</button>
          <button
            disabled={busy || !directory}
            onClick={() => finish(directory!.path)}
          >
            {packageMode ? "安装此目录" : "使用此文件夹"}
          </button>
        </footer>
      </section>
    </div>
  );
}
