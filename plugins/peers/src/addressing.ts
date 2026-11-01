export type PeerSession = {
  session_id: string;
  name?: string;
  workspace_id?: string;
  state?: string;
  updated_at?: string;
};

export type AddressedPeer<T extends PeerSession = PeerSession> = T & {
  /** Display name, falling back to a short stable session id. */
  name: string;
  /** Short id suffix used only when a display name is shared. */
  ref: string;
  /** Name, or `name [ref]` when more than one peer answers to the name. */
  address: string;
};

/** Give every session an address without hiding ambiguous display names.
 * A bare name is safe only when it is unique; otherwise every peer sharing
 * that name receives a short ref derived from its stable session id. */
export function addPeerAddresses<T extends PeerSession>(sessions: readonly T[]): Array<AddressedPeer<T>> {
  const counts = new Map<string, number>();
  for (const session of sessions) {
    const name = displayName(session);
    counts.set(name, (counts.get(name) ?? 0) + 1);
  }
  const addressed = sessions.map((session) => {
    const name = displayName(session);
    const sameName = sessions.filter((peer) => displayName(peer) === name);
    const ref = uniqueRef(session.session_id, sameName);
    return {...session, name, ref, address: name};
  });
  // A unique name is a complete address, including when it happens to look
  // like another peer's `name [ref]`. Claim those first, then extend the ref
  // of every shared name until its address no longer shadows anyone.
  const used = new Set<string>();
  for (const peer of addressed) {
    if ((counts.get(peer.name) ?? 0) !== 1) continue;
    peer.address = peer.name;
    used.add(peer.address);
  }
  for (const peer of addressed) {
    if ((counts.get(peer.name) ?? 0) === 1) continue;
    const id = peer.session_id.trim();
    let ref = peer.ref;
    let address = `${peer.name} [${ref}]`;
    while (used.has(address) && ref.length < id.length) {
      ref = id.slice(0, Math.min(ref.length + 4, id.length));
      address = `${peer.name} [${ref}]`;
    }
    if (used.has(address)) throw new Error(`peer address collision for ${JSON.stringify(peer.name)}`);
    peer.ref = ref;
    peer.address = address;
    used.add(address);
  }
  return addressed;
}

/** Resolve a tool target by exact id, exact address, or a unique bare name.
 * The caller must pass every visible session, including the source, so that
 * self-sends and ambiguous names fail before any request is persisted. */
export function resolvePeerAddress<T extends AddressedPeer>(
  peers: readonly T[],
  input: string,
  selfID: string,
): T {
  const value = input.trim();
  if (!value) throw new Error("peer target is required; use to with an address from list_peers");
  const selected = peers.find((peer) => peer.session_id.trim() === value)
    ?? peers.find((peer) => peer.address === value)
    ?? uniqueName(peers, value);
  if (!selected) {
    const candidates = peers.filter((peer) => peer.session_id.trim() !== selfID.trim());
    throw new Error(candidates.length === 0
      ? "no other active user-visible peer sessions are available in this workspace"
      : `no active peer session matches ${JSON.stringify(value)}; address one of: ${formatCandidates(candidates)}`);
  }
  if (selected.session_id.trim() === selfID.trim()) throw new Error("a session cannot send a peer request to itself");
  return selected;
}

function uniqueName<T extends AddressedPeer>(peers: readonly T[], value: string): T | undefined {
  const matches = peers.filter((peer) => peer.name === value);
  if (matches.length === 0) return undefined;
  if (matches.length === 1) return matches[0];
  throw new Error(`peer name ${JSON.stringify(value)} is ambiguous; use one of: ${formatCandidates(matches)}`);
}

function displayName(session: PeerSession): string {
  return session.name?.trim() || `Session ${session.session_id.trim().slice(0, 8)}`;
}

function uniqueRef(sessionID: string, sameName: readonly PeerSession[]): string {
  const id = sessionID.trim();
  let length = Math.min(8, id.length);
  const others = sameName
    .map((peer) => peer.session_id.trim())
    .filter((other) => other !== id);
  while (length < id.length && others.some((other) => other.slice(0, length) === id.slice(0, length))) {
    length = Math.min(length + 4, id.length);
  }
  return id.slice(0, length);
}

function formatCandidates(peers: readonly AddressedPeer[]): string {
  const ordered = [...peers].sort((left, right) => left.address.localeCompare(right.address));
  const shown = ordered.slice(0, 8).map((peer) => JSON.stringify(peer.address));
  return shown.join(", ") + (ordered.length > shown.length ? `, and ${ordered.length - shown.length} more` : "");
}
