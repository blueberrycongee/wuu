// Serialized into the renderer so the endpoint observes two real frame opportunities.
function waitForSessionContentFrame(index) {
  return new Promise((resolve, reject) => {
    const deadline = performance.now() + 30000;
    let readyFrames = 0;
    const check = () => {
      if (performance.now() > deadline) return reject(new Error('Target conversation never became visible'));
      const pane = document.querySelector(`.cached-conversation-pane[data-active="true"][data-thread-id="switch-thread-${index}"]`);
      const turns = pane?.getElementsByClassName('turn');
      const last = turns?.[turns.length - 1];
      let ready = false;
      // Pending navigation cannot satisfy the endpoint. Reading geometry here
      // would only force loading-animation style updates into the work counters.
      if (window.__switchProbe.start !== undefined && pane && !pane.closest('[inert]') &&
        last?.textContent.includes(`Session ${index} question`)) {
        const viewport = document.querySelector('.conversation-pane > .scroll-region');
        const rect = viewport?.getBoundingClientRect();
        const lastRect = last.getBoundingClientRect();
        ready = Boolean(rect && lastRect.bottom > rect.top && lastRect.top < rect.bottom && lastRect.height > 0);
      }
      readyFrames = ready ? readyFrames + 1 : 0;
      if (readyFrames >= 2) {
        if (window.__switchTrace) performance.mark(`switch-${index}-content`);
        return resolve(performance.now() - window.__switchProbe.start);
      }
      requestAnimationFrame(check);
    };
    requestAnimationFrame(check);
  });
}

module.exports = { waitForSessionContentFrame };
