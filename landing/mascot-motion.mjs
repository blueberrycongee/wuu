// Independently authored motion using Wuu's existing geometry.
const smooth = t => t * t * (3 - 2 * t);
const clamp = t => Math.max(0, Math.min(1, t));

export function spinAngle(p) {
  // Brief anticipation, a fast turn, then a small settling overshoot.
  if (p < 0.14) return -0.22 * smooth(p / 0.14);
  if (p < 0.78) return -0.22 + (Math.PI * 2 + 0.34) * smooth((p - 0.14) / 0.64);
  return Math.PI * 2 + 0.12 * (1 - smooth((p - 0.78) / 0.22));
}

export function projectEye(eye, yaw, pitch = 0) {
  const radius = 39, cx = 51.22, cy = 49.97;
  const x = eye.x - cx, y = eye.y - cy;
  const z = Math.sqrt(Math.max(1, radius * radius - x * x - y * y));
  const longitude = Math.atan2(x, z);
  const rx = x * Math.cos(yaw) + z * Math.sin(yaw);
  const rz = z * Math.cos(yaw) - x * Math.sin(yaw);
  const ry = y * Math.cos(pitch) + rz * Math.sin(pitch);
  const depth = rz * Math.cos(pitch) - y * Math.sin(pitch);
  // Each eye meets the silhouette separately, with its own foreshortening.
  return { x: cx + rx, y: cy + ry,
    width: Math.max(0, Math.cos(longitude + yaw) / Math.cos(longitude)),
    opacity: smooth(clamp(depth / 5)) };
}

export function springStep(value, velocity, target, dt) {
  const steps = Math.max(1, Math.ceil(dt / 0.008));
  const h = dt / steps;
  for (let i = 0; i < steps; i++) {
    velocity += ((target - value) * 210 - velocity * 23) * h;
    value += velocity * h;
  }
  return { value, velocity };
}

export function eyePose(time, action, progress, gaze) {
  // A quick intention, a readable hold, then a softer release.
  const envelope = progress < 0.22 ? smooth(progress / 0.22)
    : progress < 0.52 ? 1 : 1 - smooth((progress - 0.52) / 0.48);
  let x = gaze.x, y = gaze.y, scaleY = 1, scaleX = 1, roll = 0;
  if (action === 'peek') { x += 10 * envelope; y -= 13 * envelope; roll = -6 * envelope; }
  if (action === 'wink') roll = 7 * envelope;
  if (action === 'bright') { scaleY += 0.3 * envelope; scaleX += 0.2 * envelope; y -= 4 * envelope; }
  if (action === 'squint') { scaleY -= 0.55 * envelope; scaleX += 0.13 * envelope; roll = -4 * envelope; }
  if (action === 'scan') { x += 12 * Math.sin(progress * Math.PI * 2) * envelope; y -= 4 * envelope; roll = 4 * Math.sin(progress * Math.PI * 2) * envelope; }
  if (action === 'think') { x -= 8 * envelope; y -= 10 * envelope; roll = 6 * envelope; }
  if (action === 'shy') { x += 3 * envelope; y += 9 * envelope; scaleY -= 0.25 * envelope; roll = -5 * envelope; }
  if (action === 'dizzy') { x += 6 * Math.sin(progress * Math.PI * 4) * envelope; y += (6 * Math.cos(progress * Math.PI * 4) - 2) * envelope; roll = 10 * Math.sin(progress * Math.PI * 2) * envelope; }
  if (action === 'spin') scaleY = 1 - 0.12 * Math.sin(progress * Math.PI * 2);
  return { x, y, scaleX, scaleY, opacity: 1, roll };
}

export function blinkLid(progress) {
  if (progress <= 0 || progress >= 1) return 1;
  if (progress < 0.25) return 1 - 0.98 * smooth(progress / 0.25);
  if (progress < 0.4) return 0.02;
  return 0.02 + 0.98 * smooth((progress - 0.4) / 0.6);
}

// Whole-body motion, in fractions of the ball's rendered width. Each pose
// leaves from and returns to rest so idle acts can chain without snapping.
export function hopPose(progress) {
  // Crouch to anticipate, stretch through the air, squash on touchdown.
  if (progress <= 0 || progress >= 1) return { rise: 0, scaleX: 1, scaleY: 1, tilt: 0 };
  const crouch = progress < 0.22 ? smooth(progress / 0.22)
    : progress < 0.3 ? 1 - smooth((progress - 0.22) / 0.08) : 0;
  const arc = progress < 0.3 || progress > 0.78 ? 0 : Math.sin((progress - 0.3) / 0.48 * Math.PI);
  const land = progress < 0.78 ? 0 : progress < 0.9 ? smooth((progress - 0.78) / 0.12) : 1 - smooth((progress - 0.9) / 0.1);
  const scaleY = 1 - 0.2 * crouch + 0.08 * arc - 0.16 * land;
  return { rise: arc, scaleX: 1 + (1 - scaleY) * 0.55, scaleY, tilt: 0 };
}

export function wigglePose(progress) {
  // A few quick side-to-side rocks that die out at both ends.
  if (progress <= 0 || progress >= 1) return { rise: 0, scaleX: 1, scaleY: 1, tilt: 0 };
  return { rise: 0, scaleX: 1, scaleY: 1, tilt: 8 * Math.sin(progress * Math.PI * 5) * Math.sin(Math.PI * progress) };
}

const backOut = t => {
  // Snappy entrance easing: overshoots the target by about 10%, then settles.
  const u = clamp(t) - 1, c = 1.70158;
  return 1 + (c + 1) * u * u * u + c * u * u;
};

export function splitRest(slot) {
  // Where everyone sits while the trio is out.
  if (slot === 0) return { x: 0, y: 0.14, scale: 1, tilt: 0 };
  const direction = slot === 1 ? -1 : 1;
  return { x: direction * 0.26, y: -0.2, scale: 0.66, tilt: direction * 8 };
}

export function splitEnter(progress, slot) {
  // Companions pop out from the center; the original recoils, then settles low.
  const p = clamp(progress);
  if (slot === 0) {
    const recoil = p < 0.24 ? smooth(p / 0.24) : 1 - smooth((p - 0.24) / 0.76);
    return { x: 0, y: 0.14 * backOut(p), scaleX: 1 + 0.1 * recoil, scaleY: 1 - 0.12 * recoil, tilt: 0, opacity: 1 };
  }
  const delay = slot === 1 ? 0 : 0.18;
  const q = backOut((p - delay) / (1 - delay));
  const rest = splitRest(slot);
  return {
    x: rest.x * q,
    y: rest.y * q,
    scale: Math.max(0.001, rest.scale * q),
    tilt: rest.tilt * q,
    opacity: clamp((p - delay) / 0.1),
  };
}

export function splitExit(progress, slot) {
  // Companions dive back accelerating; the original squashes as they land.
  const p = clamp(progress);
  const q = 1 - p * p;
  if (slot === 0) {
    const land = p < 0.7 ? 0 : p < 0.85 ? smooth((p - 0.7) / 0.15) : 1 - smooth((p - 0.85) / 0.15);
    return { x: 0, y: 0.14 * q, scaleX: 1 + 0.1 * land, scaleY: 1 - 0.12 * land, tilt: 0, opacity: 1 };
  }
  const rest = splitRest(slot);
  return {
    x: rest.x * q,
    y: rest.y * q,
    scale: Math.max(0.001, rest.scale * q),
    tilt: rest.tilt * q,
    opacity: 1 - smooth(clamp((p - 0.75) / 0.25)),
  };
}

if (typeof document !== 'undefined') {
  const reduced = matchMedia('(prefers-reduced-motion: reduce)');
  const pointer = { x: innerWidth / 2, y: innerHeight / 2, active: false, movedAt: -10 };
  const states = [];
  let frame = 0, previous = 0, clock = 0;
  const ns = 'http://www.w3.org/2000/svg';
  const group = () => document.createElementNS(ns, 'g');
  function schedule() {
    if (!frame && !document.hidden && !reduced.matches && states.some(s => s.visible)) frame = requestAnimationFrame(render);
  }
  function render(now) {
    frame = 0;
    const dt = previous ? Math.min((now - previous) / 1000, 0.05) : 0;
    previous = now;
    clock += dt;
    for (const s of states) {
      if (!s.visible) continue;
      const rect = s.svg.getBoundingClientRect();
      const dx = pointer.x - (rect.left + rect.width / 2);
      const dy = pointer.y - (rect.top + rect.height / 2);
      const distance = Math.hypot(dx, dy);
      const reach = Math.max(80, Math.min(innerWidth, innerHeight) * 0.18);
      const strength = pointer.active ? Math.min(1, distance / reach) : 0;
      for (const [axis, delta, amount] of [['x', dx, 10], ['y', dy, 8]]) {
        const wander = !pointer.active ? Math.sin(clock * (axis === 'x' ? 1.1 : 0.7) + s.offset) * 2 : 0;
        const target = (distance ? delta / distance : 0) * strength * amount + wander;
        const spring = springStep(s.gaze[axis], s.velocity[axis], target, dt);
        s.gaze[axis] = spring.value; s.velocity[axis] = spring.velocity;
      }
      if (!s.action && (!s.split || s.split.phase === 'hold') && clock > s.next) {
        const choices = ['peek', 'wink', 'bright', 'squint', 'scan', 'think', 'dizzy', 'shy'].filter(action => action !== s.lastAction);
        s.action = choices[Math.floor(Math.random() * choices.length)];
        s.lastAction = s.action; s.cycle++;
        s.duration = (s.action === 'scan' || s.action === 'dizzy' ? 1.5 : 0.75) + Math.random() * 0.45;
        s.start = clock;
      }
      const duration = s.action === 'spin' ? 0.95 : s.duration;
      const progress = Math.min(1, (clock - s.start) / duration);
      const pose = eyePose(clock + s.offset, s.action, progress, s.gaze);
      // Blend back into the pointer gaze instead of snapping after an expression.
      const blend = 1 - Math.exp(-dt * (s.action === 'spin' ? 45 : 18));
      for (const key of ['x', 'y', 'scaleX', 'scaleY', 'opacity', 'roll']) s.pose[key] += (pose[key] - s.pose[key]) * blend;
      const p = s.pose;
      s.face.setAttribute('transform', `rotate(${p.roll} 51.22 49.97)`);
      if (clock >= s.nextBlink) {
        s.blinkStart = clock;
        s.blinkDuration = 0.15 + Math.random() * 0.09;
        const double = !s.secondBlink && Math.random() < 0.24;
        s.secondBlink = double;
        s.nextBlink = clock + (double ? s.blinkDuration + 0.1 : 1.2 + Math.random() * 2.4);
      }
      const lid = blinkLid((clock - s.blinkStart) / s.blinkDuration);
      const turn = s.action === 'spin' ? spinAngle(progress) : 0;
      for (const [index, eye] of s.eyes.entries()) {
        const projection = projectEye(eye, turn + p.x / 39, p.y / 39);
        const wink = s.action === 'wink' && index === s.cycle % 2 ? 1 - 0.95 * Math.sin(Math.PI * progress) ** 6 : 1;
        const height = p.scaleY * lid * wink;
        eye.node.setAttribute('transform', `translate(${projection.x} ${projection.y}) scale(${projection.width * p.scaleX} ${height}) translate(${-eye.x} ${-eye.y})`);
        eye.node.style.opacity = String(projection.opacity);
      }
      // Whole-body channel. Idle hops and wiggles when left alone, plus the
      // trio: a state of its own that the ball enters on a double-click, or
      // wanders into on its own when it has been bored for a while.
      const idle = !pointer.active || clock - pointer.movedAt > 4;
      if (!s.split && !s.body && s.playButton && idle && clock > 20 && clock > s.nextBody && Math.random() < 0.12) startSplit(s, 'auto');
      let transform = '';
      if (s.split) {
        const split = s.split;
        const elapsed = clock - split.start;
        if (split.phase === 'enter') {
          const p = elapsed / 0.55;
          if (p >= 1) { split.phase = 'hold'; split.start = clock; }
          else {
            const main = splitEnter(p, 0);
            transform = `translateY(${main.y * rect.width}px) scale(${main.scaleX} ${main.scaleY}) rotate(${main.tilt}deg)`;
            split.holders.forEach((holder, i) => {
              const q = splitEnter(p, i + 1);
              holder.style.transform = `translate(${q.x * rect.width}px, ${q.y * rect.width}px) scale(${q.scale}) rotate(${q.tilt}deg)`;
              holder.style.opacity = String(q.opacity);
            });
          }
        } else if (split.phase === 'exit') {
          const p = elapsed / 0.45;
          if (p >= 1) {
            for (const holder of split.holders) holder.remove();
            s.split = null;
            s.nextBody = clock + 12 + Math.random() * 10;
            s.next = Math.max(s.next, clock + 1);
          } else {
            const main = splitExit(p, 0);
            transform = `translateY(${main.y * rect.width}px) scale(${main.scaleX} ${main.scaleY}) rotate(${main.tilt}deg)`;
            split.holders.forEach((holder, i) => {
              const q = splitExit(p, i + 1);
              holder.style.transform = `translate(${q.x * rect.width}px, ${q.y * rect.width}px) scale(${q.scale}) rotate(${q.tilt}deg)`;
              holder.style.opacity = String(q.opacity);
            });
          }
        } else {
          // Holding as a trio. An auto-split merges once the ball has had its
          // fun or the pointer comes back; a manual one waits for a double-click.
          const returned = split.origin === 'auto' && pointer.active && clock - pointer.movedAt < 1.5 && clock - split.start > 3;
          if (returned || (split.origin === 'auto' && clock > split.holdUntil)) {
            split.phase = 'exit'; split.start = clock;
          } else {
            const rest = splitRest(0);
            let y = rest.y, scaleX = 1, scaleY = 1, tilt = 0;
            const pose = bodyPoseFor(s, idle, 0.72, 7, 9);
            if (pose) { y -= pose.rise * 0.14; scaleX = pose.scaleX; scaleY = pose.scaleY; tilt = pose.tilt; }
            transform = `translateY(${y * rect.width}px) scale(${scaleX} ${scaleY}) rotate(${tilt}deg)`;
            split.holders.forEach((holder, i) => {
              const q = splitRest(i + 1);
              let x = q.x, cy = q.y, scale = q.scale, ct = q.tilt;
              const companion = bodyPoseFor(split.companions[i], idle, 0.75, 4, 6);
              if (companion) { cy -= companion.rise * 0.16; ct += companion.tilt; }
              const sy = companion ? companion.scaleY : 1, sx = companion ? companion.scaleX : 1;
              holder.style.transform = `translate(${x * rect.width}px, ${cy * rect.width}px) scale(${scale * sx} ${scale * sy}) rotate(${ct}deg)`;
              holder.style.opacity = '1';
            });
          }
        }
      } else {
        const pose = bodyPoseFor(s, idle, 0.72, 7, 9);
        if (pose) transform = `translateY(${-pose.rise * rect.width * 0.14}px) scale(${pose.scaleX} ${pose.scaleY}) rotate(${pose.tilt}deg)`;
      }
      if (s.svg.style.transform !== transform) s.svg.style.transform = transform;
      if (s.action && progress === 1) { s.action = null; s.next = clock + 2.5 + Math.random() * 3.5; }
    }
    schedule();
  }
  // Shared driver for the small whole-body acts. Returns the current pose,
  // or null while the actor rests between acts.
  function bodyPoseFor(actor, idle, hopChance, minGap, gapSpan) {
    if (!actor.body && idle && clock > actor.nextBody) {
      actor.body = Math.random() < hopChance ? 'hop' : 'wiggle';
      actor.hopAgain = actor.body === 'hop' && Math.random() < 0.28;
      actor.bodyDuration = actor.body === 'hop' ? 0.85 : 1.15;
      actor.bodyStart = clock;
    }
    if (!actor.body) return null;
    const bp = (clock - actor.bodyStart) / actor.bodyDuration;
    if (bp >= 1) {
      if (actor.hopAgain) { actor.hopAgain = false; actor.bodyStart = clock + 0.12; return { rise: 0, scaleX: 1, scaleY: 1, tilt: 0 }; }
      actor.body = null;
      actor.nextBody = clock + minGap + Math.random() * gapSpan;
      return null;
    }
    return (actor.body === 'hop' ? hopPose : wigglePose)(Math.max(0, bp));
  }
  function startSplit(s, origin) {
    // Companion tints follow the onboarding trio's palette.
    s.split = { phase: 'enter', start: clock, origin, holdUntil: clock + 9 + Math.random() * 6,
      companions: [0, 1].map(slot => ({ body: null, bodyStart: 0, bodyDuration: 0.85, hopAgain: false, nextBody: clock + 0.8 + slot * 0.5 + Math.random() * 0.4 })),
      holders: [1, 2].map(slot => {
      const clone = s.svg.cloneNode(true);
      clone.removeAttribute('class');
      clone.style.transform = '';
      clone.setAttribute('aria-hidden', 'true');
      const clip = clone.querySelector('clipPath');
      if (clip) {
        clip.id = `${clip.id}-split-${slot}`;
        const bound = clone.querySelector('[clip-path]');
        if (bound) bound.setAttribute('clip-path', `url(#${clip.id})`);
      }
      const body = clone.querySelector('g[fill]');
      if (body) body.setAttribute('fill', slot === 1 ? '#abc4ce' : '#b9c8b2');
      const holder = document.createElement('div');
      holder.className = 'mascot-split-clone';
      holder.setAttribute('aria-hidden', 'true');
      holder.append(clone);
      (s.playButton || s.svg.parentNode).append(holder);
      return holder;
    }) };
    s.body = null;
    if (!s.action) { s.action = 'bright'; s.duration = 0.9; s.start = clock; }
  }
  const observer = new IntersectionObserver(entries => {
    for (const entry of entries) {
      const s = states.find(s => s.svg === entry.target);
      if (s) s.visible = entry.isIntersecting;
    }
    schedule();
  });
  const images = [...document.querySelectorAll('img[src$="/brand/wuu.svg"]')];
  if (images.length) fetch(images[0].src).then(response => {
    if (!response.ok) throw new Error('Mascot asset unavailable');
    return response.text();
  }).then(source => {
    images.forEach((img, index) => {
      const svg = new DOMParser().parseFromString(source, 'image/svg+xml').documentElement;
      if (svg.localName !== 'svg') return;
      for (const name of ['width', 'height', 'class']) if (img.hasAttribute(name)) svg.setAttribute(name, img.getAttribute(name));
      svg.classList.add('interactive-mascot');
      svg.setAttribute('role', 'img');
      if (img.alt) svg.setAttribute('aria-label', img.alt); else svg.setAttribute('aria-hidden', 'true');
      const body = svg.children[0], face = svg.children[1];
      if (!body || !face) return;
      const defs = document.createElementNS(ns, 'defs');
      const clip = document.createElementNS(ns, 'clipPath');
      clip.id = `mascot-boundary-${index}`;
      clip.append(body.firstElementChild.cloneNode(true)); defs.append(clip); svg.prepend(defs);
      const boundary = group(); boundary.setAttribute('clip-path', `url(#${clip.id})`);
      face.replaceWith(boundary); boundary.append(face);
      img.replaceWith(svg);
      const eyes = [...face.children].map(path => {
        const box = path.getBBox(); const node = group(); path.replaceWith(node); node.append(path);
        return { node, x: box.x + box.width / 2, y: box.y + box.height / 2 };
      });
      const state = { svg, face, eyes, visible: false, gaze: { x: 0, y: 0 }, pose: { x: 0, y: 0, scaleX: 1, scaleY: 1, opacity: 1, roll: 0 }, offset: index * 1.3, action: null, start: 0, velocity: { x: 0, y: 0 }, duration: 1, blinkDuration: 0.2, secondBlink: false, lastAction: null, blinkStart: -10, nextBlink: 1.5 + Math.random() * 3, next: 7 + index * 2, cycle: index, body: null, bodyStart: 0, bodyDuration: 0.85, nextBody: 4 + index * 1.7 + Math.random() * 4, hopAgain: false, split: null, playButton: null };
      states.push(state); observer.observe(svg);
      svg.addEventListener('pointerenter', () => {
        if (reduced.matches || state.action === 'spin') return;
        state.action = 'bright'; state.duration = 0.8; state.start = clock; schedule();
      });
      if (!svg.closest('a')) {
        const button = document.createElement('button');
        const chinese = document.documentElement.lang.startsWith('zh');
        button.type = 'button'; button.className = 'mascot-play'; button.setAttribute('aria-label', chinese ? '让 Wuu 转一圈' : 'Make Wuu spin');
        button.title = chinese ? '单击转一圈，双击分身或合体' : 'Click to spin, double-click to split or merge';
        svg.replaceWith(button); button.append(svg);
        state.playButton = button;
        button.addEventListener('click', () => {
          if (reduced.matches || state.action === 'spin' || state.split) return;
          state.action = 'spin'; state.start = clock; schedule();
        });
        button.addEventListener('dblclick', () => {
          if (reduced.matches) return;
          if (!state.split) startSplit(state, 'manual');
          else if (state.split.phase !== 'exit') { state.split.phase = 'exit'; state.split.start = clock; }
          schedule();
        });
      }
    });
  }).catch(() => { /* The static mascot remains available if enhancement fails. */ });
  document.addEventListener('pointermove', event => { pointer.x = event.clientX; pointer.y = event.clientY; pointer.active = true; pointer.movedAt = clock; schedule(); }, { passive: true });
  document.documentElement.addEventListener('pointerleave', () => { pointer.active = false; });
  function reset() {
    cancelAnimationFrame(frame); frame = 0; previous = 0;
    if (reduced.matches) for (const s of states) {
      s.face.removeAttribute('transform'); s.face.style.opacity = '1';
      s.eyes.forEach(e => { e.node.removeAttribute('transform'); e.node.style.opacity = '1'; });
      s.velocity = { x: 0, y: 0 };
      s.action = null; s.pose = { x: 0, y: 0, scaleX: 1, scaleY: 1, opacity: 1, roll: 0 };
      s.svg.style.transform = ''; s.body = null; s.hopAgain = false;
      if (s.split) { for (const holder of s.split.holders) holder.remove(); s.split = null; }
    }
    schedule();
  }
  document.addEventListener('visibilitychange', reset);
  reduced.addEventListener('change', reset);
}
