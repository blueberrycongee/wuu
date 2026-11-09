// The shared renderer contains an Electron-only browser panel. The web host
// disables that capability, but TypeScript still follows the lazy module graph.
// Keep the compatibility type local to this shell instead of making Electron a
// browser dependency.
declare namespace Electron {
  interface WebviewTag extends HTMLElement {
    canGoBack(): boolean;
    canGoForward(): boolean;
    getTitle(): string;
    getURL(): string;
    goBack(): void;
    goForward(): void;
    loadURL(url: string): Promise<void>;
    reload(): void;
    stop(): void;
    addEventListener(type: string, listener: (event: any) => void): void;
    removeEventListener(type: string, listener: (event: any) => void): void;
  }
}
