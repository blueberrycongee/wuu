import { Capacitor, registerPlugin, type PluginListenerHandle } from '@capacitor/core';

type SyncState = { id: string; active: boolean; error?: string };
interface BackgroundSyncPlugin {
  start(options: { id: string }): Promise<SyncState>;
  stop(options: { id: string }): Promise<void>;
  addListener(event: 'stateChange', listener: (state: SyncState) => void): Promise<PluginListenerHandle>;
}
const native = registerPlugin<BackgroundSyncPlugin>('WuuBackgroundSync');
let nextLease = 0;

export class AndroidBackgroundSync {
  private readonly id = `${Date.now()}-${++nextLease}`;
  private listener?: PluginListenerHandle;
  private starting?: Promise<void>;
  private stopped = false;
  active = false;

  constructor(private readonly onChange: (error?: string) => void) {}

  start(): Promise<void> {
    if (this.stopped || this.active || Capacitor.getPlatform() !== 'android') return Promise.resolve();
    if (this.starting) return this.starting;
    this.starting = this.acquire().finally(() => { this.starting = undefined; });
    return this.starting;
  }

  private async acquire(): Promise<void> {
    try {
      this.listener ??= await native.addListener('stateChange', (state) => {
        if (state.id !== this.id || this.stopped) return;
        this.active = state.active;
        this.onChange(state.active ? undefined : state.error || 'Background sync stopped');
      });
      if (this.stopped) return;
      const state = await native.start({ id: this.id });
      if (this.stopped) return;
      if (!state.active) throw new Error(state.error || 'Background sync did not start');
      this.active = true;
      this.onChange();
    } catch (error) {
      this.active = false;
      await native.stop({ id: this.id }).catch(() => {});
      if (!this.stopped) this.onChange(error instanceof Error ? error.message : String(error));
    }
  }

  async stop(): Promise<void> {
    this.stopped = true;
    this.active = false;
    // A late native start must be released too; leases isolate replacements.
    await this.starting;
    if (Capacitor.getPlatform() === 'android') await native.stop({ id: this.id }).catch(() => {});
    await this.listener?.remove();
    this.listener = undefined;
  }
}
