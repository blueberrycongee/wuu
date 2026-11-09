export function initializeNavigation(document, hover, timers = globalThis) {
  const shell = document.querySelector('.site-header-shell');
  if (!shell) return;
  const menus = [...shell.querySelectorAll('.nav-menu')];
  const panels = new Map();
  // Stable surfaces let CSS reverse an interrupted transition at its current
  // opacity. Native details hiding and cloned exit panels caused visible resets.
  menus.forEach((menu, index) => {
    const panel = menu.querySelector('.nav-panel');
    const summary = menu.querySelector('summary');
    panel.id = `nav-panel-${index}`;
    summary.setAttribute('aria-controls', panel.id);
    panels.set(menu, panel);
    // Keep the panel outside the native collapsing content, after its trigger.
    menu.after(panel);
  });
  shell.classList.add('nav-enhanced');
  const header = shell.querySelector('.site-header');
  let closeTimer;
  const cancelClose = () => timers.clearTimeout(closeTimer);
  const setOpen = (menu, value) => {
    if (menu.open !== value) menu.open = value;
    panels.get(menu).classList.toggle('is-active', value);
    panels.get(menu).inert = !value;
    menu.querySelector('summary').setAttribute('aria-expanded', String(value));
  };
  const syncSurface = () => {
    const active = menus.find(menu => menu.open);
    shell.classList.toggle('is-navigation-open', Boolean(active));
    if (active) shell.style.setProperty('--nav-panel-height', `${panels.get(active).offsetHeight}px`);
  };
  const closeAll = () => {
    cancelClose();
    menus.forEach(menu => setOpen(menu, false));
    syncSurface();
  };
  const open = menu => {
    cancelClose();
    menus.forEach(other => setOpen(other, other === menu));
    syncSurface();
  };
  const contains = (menu, target) => menu.contains(target) || panels.get(menu).contains(target);
  for (const menu of menus) {
    setOpen(menu, menu.open);
    for (const surface of [menu, panels.get(menu)]) {
      surface.addEventListener('pointerenter', () => {
        cancelClose();
        if (hover.matches) open(menu);
      });
      surface.addEventListener('pointerleave', event => {
        // Button gaps belong to the same navigation surface.
        if (header.contains(event.relatedTarget)) { cancelClose(); return; }
        if (hover.matches && !contains(menu, document.activeElement)) {
          cancelClose();
          closeTimer = timers.setTimeout(closeAll, 160);
        }
      });
      surface.addEventListener('focusin', cancelClose);
      surface.addEventListener('focusout', event => {
        if (!contains(menu, event.relatedTarget)) { setOpen(menu, false); syncSurface(); }
      });
    }
    menu.querySelector('summary').addEventListener('click', event => {
      event.preventDefault();
      if (menu.open) closeAll();
      else open(menu);
    });
    menu.addEventListener('toggle', () => {
      if (menu.open) open(menu);
      else { setOpen(menu, false); syncSurface(); }
    });
  }
  header.addEventListener('pointerleave', () => {
    cancelClose();
    closeTimer = timers.setTimeout(closeAll, 160);
  });
  if (typeof ResizeObserver !== 'undefined') {
    const observer = new ResizeObserver(syncSurface);
    panels.forEach(panel => observer.observe(panel));
  }
  document.addEventListener('pointerdown', event => {
    if (!menus.some(menu => contains(menu, event.target))) closeAll();
  });
  document.addEventListener('keydown', event => {
    const active = menus.find(menu => menu.open);
    if (event.key === 'Escape' && active) {
      closeAll();
      active.querySelector('summary').focus();
    }
  });
}

if (typeof document !== 'undefined') {
  initializeNavigation(document, matchMedia('(hover: hover) and (pointer: fine)'));
}
