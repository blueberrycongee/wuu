// A preview can cover only the underlying content header. Foreground drawer
// and app-history controls always retain strict pointer ownership.
function ownsHeaderSample(target, sample, drawerBounds) {
  if (sample.owned) return true;
  return !!(target.contentHeader && !target.history && !target.drawerOwned &&
    sample.drawer && drawerBounds &&
    sample.x >= drawerBounds.left && sample.x <= drawerBounds.right &&
    sample.y >= drawerBounds.top && sample.y <= drawerBounds.bottom);
}
module.exports = { ownsHeaderSample };
