// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { startPhoneAppearance } from '../src/lib/appearance';

const native = vi.hoisted(() => ({ setSurface: vi.fn().mockResolvedValue(undefined) }));
vi.mock('@capacitor/core', () => ({
 Capacitor: { getPlatform: () => 'android' }, registerPlugin: () => native,
 SystemBarType: { StatusBar: 'StatusBar', NavigationBar: 'NavigationBar' },
 SystemBars: { setStyle: vi.fn() }, SystemBarsStyle: { Dark: 'DARK', Light: 'LIGHT' },
}));
vi.mock('../../../desktop/src/renderer/Theme', () => ({ applyThemePreference: vi.fn() }));
let stop: (() => void) | undefined;
afterEach(() => {
 stop?.(); stop = undefined; vi.restoreAllMocks(); vi.unstubAllGlobals(); vi.useRealTimers();
 document.body.innerHTML = ''; document.documentElement.removeAttribute('style');
 delete document.documentElement.dataset.phoneSurface; native.setSurface.mockClear();
});

it('changes the status surface as the login hero leaves view without darkening the bottom bar', async () => {
 vi.useFakeTimers();
 vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => window.setTimeout(() => callback(0), 0));
 vi.stubGlobal('cancelAnimationFrame', (id: number) => window.clearTimeout(id));
 vi.stubGlobal('matchMedia', () => ({ addEventListener() {}, removeEventListener() {} }));
 vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockImplementation(() => {
  const context = { fillStyle: '', fillRect() {}, getImageData() {
   const channels = context.fillStyle.match(/\d+/g)?.map(Number) ?? [0, 0, 0];
   return { data: new Uint8ClampedArray([...channels.slice(0, 3), 255]) };
  } };
  return context as unknown as CanvasRenderingContext2D;
 });
 document.documentElement.style.backgroundColor = 'rgb(255, 255, 255)';
 document.documentElement.dataset.phoneSurface = 'login';
 document.body.innerHTML = '<main class="account-home"><header class="account-page-header" style="background:rgb(32,16,32)"></header></main>';
 const hero = document.querySelector('header')!;
 let bottom = 300;
 vi.spyOn(hero, 'getBoundingClientRect').mockImplementation(() => ({ bottom } as DOMRect));
 stop = startPhoneAppearance();
 const initial = native.setSurface.mock.lastCall![0];
 expect(initial.statusDark).toBe(true); expect(initial.dark).toBe(false);
 expect(initial.statusColor).not.toBe(initial.color);
 bottom = 0; window.dispatchEvent(new Event('scroll'));
 await vi.runOnlyPendingTimersAsync();
 const scrolled = native.setSurface.mock.lastCall![0];
 expect(scrolled.statusColor).toBe(scrolled.color); expect(scrolled.color).toBe(initial.color);
 expect(scrolled.statusDark).toBe(false);
 bottom = 300; document.documentElement.dataset.phoneSurface = 'default';
 await Promise.resolve(); await vi.runOnlyPendingTimersAsync();
 expect(native.setSurface.mock.lastCall![0].statusDark).toBe(false);
});
