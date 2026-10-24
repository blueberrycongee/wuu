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
