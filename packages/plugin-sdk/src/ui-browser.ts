export interface BrowserTargetV1 { readonly workdir: string; readonly tabID: string; readonly threadID?: string }
export interface BrowserSurfaceV1 {
  readonly workdir: string; readonly tabID: string; readonly url: string; readonly title: string;
  readonly canGoBack: boolean; readonly canGoForward: boolean; readonly loading: boolean; readonly error?: string;
}
export interface BrowserBoundsV1 { readonly x: number; readonly y: number; readonly width: number; readonly height: number }
export interface BrowserAdoptedV1 { readonly workdir: string; readonly openerTabID: string; readonly tabID: string; readonly url: string }
export interface BrowserUserInputV1 extends BrowserTargetV1 { readonly inputGeneration?: number }
export interface BrowserActivityIdentityV1 { readonly id: string; readonly thread_id: string; readonly execution_id?: string }
export interface BrowserActivityV1 extends BrowserActivityIdentityV1 {
  readonly kind: "browser" | "cua"; readonly workdir: string; readonly target?: string;
  readonly state: "starting" | "active" | "background_controlled" | "foreground_controlled" | "user_controlled" | "waiting_confirmation" | "stopped" | "error";
}
export interface BrowserDockTargetV1 { readonly thread_id: string; readonly workdir: string; readonly tabID: string }
export interface BrowserNavigationRequestV1 { readonly url: string; readonly reuseKey: string; readonly requestID: number }
export interface BrowserOperationOptionsV1 { readonly signal?: AbortSignal }
/** A presentation lease never creates a second native tab registry. Dispose parks its page. */
export interface BrowserSurfaceHandleV1 {
  readonly target: BrowserTargetV1;
  read(options?: BrowserOperationOptionsV1): Promise<BrowserSurfaceV1 | null>;
  command(input: { readonly command: "navigate" | "back" | "forward" | "reload" | "stop"; readonly url?: string }, options?: BrowserOperationOptionsV1): Promise<BrowserSurfaceV1 | null>;
  onSurface(listener: (snapshot: BrowserSurfaceV1) => void): () => void;
  onTabAdopted(listener: (event: BrowserAdoptedV1) => void): () => void;
  onUserInput(listener: (event: BrowserUserInputV1) => void): () => void;
  reportBounds(rect: BrowserBoundsV1 | null, force?: boolean): void;
  suppressOverlay(suppressed: boolean): void;
  takeOver(activity: BrowserActivityIdentityV1, inputGeneration?: number, options?: BrowserOperationOptionsV1): Promise<void>;
  dispose(): void;
}
export interface ApplicationBrowserV1 {
  readonly available: boolean;
  acquireSurface(target: BrowserTargetV1): BrowserSurfaceHandleV1;
  onDock(listener: (target: BrowserDockTargetV1) => void): () => void;
  onInvalidate(listener: (event: { readonly workdir: string }) => void): () => void;
  openURL(input: { readonly url: string; readonly modifiers?: { readonly metaKey?: boolean; readonly ctrlKey?: boolean; readonly altKey?: boolean; readonly button?: number } }, options?: BrowserOperationOptionsV1): Promise<void>;
  openExternal(url: string, options?: BrowserOperationOptionsV1): Promise<void>;
  consumeNavigation(requestID: number): void;
}
