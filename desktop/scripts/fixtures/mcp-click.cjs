// Serialized into the fixture renderer: no captured Node state or retry after dispatch.
module.exports = function clickMCPFixtureControl(request) {
  if (request.dispatched) return true;
  const control = [...document.querySelectorAll(request.selector)].find(element => !request.label
    || (request.contains ? element.textContent.includes(request.label) : element.textContent.trim() === request.label));
  if (!control || control.disabled || control.getAttribute("aria-disabled") === "true") return false;
  // Focus listeners can run synchronously inside click(). Latch before the
  // event, including uncertain failure, so this explicit action is never replayed.
  request.dispatched = true;
  control.click();
  return true;
};
