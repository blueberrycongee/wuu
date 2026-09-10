import { Capacitor, registerPlugin, SystemBars, SystemBarType, SystemBarsStyle } from '@capacitor/core';
import { applyThemePreference } from '../../../../desktop/src/renderer/Theme';

const surface = registerPlugin<{ setSurface(options: { color: string; dark: boolean; statusColor: string; statusDark: boolean }): Promise<void> }>('WuuAppearance');

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
    const hero = root.dataset.phoneSurface === 'login'
      ? document.querySelector<HTMLElement>('.account-home:not([hidden]) .account-page-header') : null;
    const heroVisible = hero && hero.getBoundingClientRect().bottom > 32;
    let statusColor = color;
    if (heroVisible) {
      context.fillStyle = getComputedStyle(hero).backgroundColor;
      context.fillRect(0, 0, 1, 1);
      const statusRGB = context.getImageData(0, 0, 1, 1).data;
      statusColor = '#' + Array.from(statusRGB.slice(0, 3), value => value.toString(16).padStart(2, '0')).join('');
    }
    const statusDark = Boolean(heroVisible) || dark;
    const nextSurface = `${color}:${dark}:${statusColor}:${statusDark}`;
    if (lastSurface === nextSurface) return;
    lastSurface = nextSurface;
    document.querySelector('meta[name="theme-color"]')?.setAttribute('content', statusColor);
    if (Capacitor.getPlatform() === 'android') {
      void surface.setSurface({ color, dark, statusColor, statusDark }).catch(console.error);
    } else if (Capacitor.getPlatform() === 'ios') {
      void SystemBars.setStyle({ bar: SystemBarType.StatusBar, style: statusDark ? SystemBarsStyle.Dark : SystemBarsStyle.Light }).catch(console.error);
      void SystemBars.setStyle({ bar: SystemBarType.NavigationBar, style: dark ? SystemBarsStyle.Dark : SystemBarsStyle.Light }).catch(console.error);
    }
  };
  let frame = 0;
  const schedule = () => { cancelAnimationFrame(frame); frame = requestAnimationFrame(update); };
  const observer = new MutationObserver(schedule);
  observer.observe(root, { attributes: true, attributeFilter: ['data-theme', 'style', 'data-phone-surface'] });
  const wake = () => { if (document.visibilityState !== 'hidden') { lastSurface = ''; schedule(); } };
  const scroll = () => { if (root.dataset.phoneSurface === 'login') schedule(); };
  window.addEventListener('scroll', scroll, true);
  window.addEventListener('resize', wake);
  const system = window.matchMedia('(prefers-color-scheme: dark)');
  system.addEventListener('change', wake);
  document.addEventListener('visibilitychange', wake);
  update();
  return () => { window.removeEventListener('scroll', scroll, true); window.removeEventListener('resize', wake); observer.disconnect(); cancelAnimationFrame(frame); document.removeEventListener('visibilitychange', wake); system.removeEventListener('change', wake); };
}
