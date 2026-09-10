import { Capacitor, registerPlugin, SystemBars, SystemBarsStyle } from '@capacitor/core';
import { applyThemePreference } from '../../../../desktop/src/renderer/Theme';

const surface = registerPlugin<{ setSurface(options: { color: string; dark: boolean }): Promise<void> }>('WuuAppearance');

/** Keep login and native chrome on the same theme as the connected renderer. */
export function startPhoneAppearance(): () => void {
  const stored = localStorage.getItem('wuu.web.theme');
  applyThemePreference(stored === 'light' || stored === 'dark' ? stored : 'system');
  const root = document.documentElement;
  let lastSurface = "";
  const update = () => {
    const dark = root.dataset.theme === 'dark';
    const scheme = dark ? 'dark' : 'light';
    if (root.style.colorScheme !== scheme) root.style.colorScheme = scheme;
    // Resolve custom themes to an opaque sRGB color accepted by Android.
    const canvas = document.createElement('canvas');
    const context = canvas.getContext('2d');
    if (!context) return;
    context.fillStyle = getComputedStyle(root).backgroundColor;
    context.fillRect(0, 0, 1, 1);
    const rgb = context.getImageData(0, 0, 1, 1).data;
    const color = '#' + Array.from(rgb.slice(0, 3), value => value.toString(16).padStart(2, '0')).join('');
    const nextSurface = `${color}:${dark}`;
    if (lastSurface === nextSurface) return;
    lastSurface = nextSurface;
    document.querySelector('meta[name="theme-color"]')?.setAttribute('content', color);
    if (Capacitor.getPlatform() === 'android') {
      void surface.setSurface({ color, dark }).catch(console.error);
    } else if (Capacitor.getPlatform() === 'ios') {
      void SystemBars.setStyle({ style: dark ? SystemBarsStyle.Dark : SystemBarsStyle.Light }).catch(console.error);
    }
  };
  let frame = 0;
  const schedule = () => { cancelAnimationFrame(frame); frame = requestAnimationFrame(update); };
  const observer = new MutationObserver(schedule);
  observer.observe(root, { attributes: true, attributeFilter: ['data-theme', 'style'] });
  const wake = () => { if (document.visibilityState !== 'hidden') { lastSurface = ''; schedule(); } };
  const system = window.matchMedia('(prefers-color-scheme: dark)');
  system.addEventListener('change', wake);
  document.addEventListener('visibilitychange', wake);
  update();
  return () => { observer.disconnect(); cancelAnimationFrame(frame); document.removeEventListener('visibilitychange', wake); system.removeEventListener('change', wake); };
}
