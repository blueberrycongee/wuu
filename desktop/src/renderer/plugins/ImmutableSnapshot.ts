/** Detach immutable host JSON records without serializing their string payloads. */
export function createImmutableSnapshotProjector() {
  const copies = new WeakMap<object, object>();
  function project<T>(value: T): T {
    if (!value || typeof value !== "object") return value;
    const previous = copies.get(value);
    if (previous) return previous as T;
    const copy = Array.isArray(value) ? value.map(project)
      : Object.fromEntries(Object.entries(value).map(([key, entry]) => [key, project(entry)]));
    Object.freeze(copy);
    copies.set(value, copy);
    return copy as T;
  }
  return project;
}

/** Structural equality for frozen snapshot values. Shared subtrees short-circuit on identity. */
export function sameSnapshotValue(left: unknown, right: unknown): boolean {
  if (left === right) return true;
  if (!left || !right || typeof left !== "object" || typeof right !== "object") return false;
  if (Array.isArray(left) !== Array.isArray(right)) return false;
  // Only plain data compares by content; Sets, Maps and class instances keep identity.
  if (!Array.isArray(left) && (!plainObject(left) || !plainObject(right))) return false;
  if (Array.isArray(left)) {
    const other = right as unknown[];
    return left.length === other.length && left.every((entry, index) => sameSnapshotValue(entry, other[index]));
  }
  const leftKeys = Object.keys(left);
  const other = right as Record<string, unknown>;
  if (leftKeys.length !== Object.keys(other).length) return false;
  return leftKeys.every(key => key in other && sameSnapshotValue((left as Record<string, unknown>)[key], other[key]));
}

/**
 * Reuse a previous snapshot's top-level values when their content did not change.
 * `useSyncExternalStore` selectors and memoized children then skip rerenders for
 * publications that only touched other parts of the snapshot.
 */
export function retainUnchangedSnapshotFields<T extends object>(previous: T | undefined, next: T, skip: ReadonlySet<keyof T>): T {
  if (!previous) return next;
  let changed = false;
  const retained: Record<string, unknown> = {};
  for (const [key, value] of Object.entries(next)) {
    const before = (previous as Record<string, unknown>)[key];
    if (!skip.has(key as keyof T) && value !== before && sameSnapshotValue(before, value)) {
      retained[key] = before;
      changed = true;
    } else {
      retained[key] = value;
    }
  }
  return changed ? Object.freeze(retained) as T : next;
}

function plainObject(value: object): boolean {
  const prototype = Object.getPrototypeOf(value);
  return prototype === Object.prototype || prototype === null;
}
