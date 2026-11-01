import assert from "node:assert/strict";
import test from "node:test";

import { addPeerAddresses, resolvePeerAddress } from "../src/addressing.ts";

const sessions = [
  {session_id: "aaaaaaaa-1111-4111-8111-111111111111", name: "alpha"},
  {session_id: "bbbbbbbb-2222-4222-8222-222222222222", name: "beta"},
  {session_id: "cccccccc-3333-4333-8333-333333333333", name: "shared"},
  {session_id: "cccccccc-4444-4444-8444-444444444444", name: "shared"},
  {session_id: "dddddddd-5555-4555-8555-555555555555"},
  {session_id: "eeeeeeee-6666-4666-8666-666666666666", name: "self"},
];

const peers = addPeerAddresses(sessions);
const selfID = sessions[5].session_id;

test("unique names are addresses and unnamed sessions get a stable fallback", () => {
  assert.equal(peers[0].address, "alpha");
  assert.equal(peers[1].address, "beta");
  assert.equal(peers[4].name, "Session dddddddd");
  assert.equal(peers[4].address, "Session dddddddd");
});

test("shared names receive refs that stay unique on a prefix collision", () => {
  assert.equal(peers[2].address, "shared [cccccccc-333]");
  assert.equal(peers[3].address, "shared [cccccccc-444]");
  assert.notEqual(peers[2].address, peers[3].address);
});

test("a unique name cannot be shadowed by another peer's disambiguated address", () => {
  const shadowed = addPeerAddresses([
    {session_id: "cccccccc-3333-4333-8333-333333333333", name: "shared"},
    {session_id: "cccccccc-4444-4444-8444-444444444444", name: "shared"},
    {session_id: "ffffffff-7777-4777-8777-777777777777", name: "shared [cccccccc-333]"},
  ]);
  assert.equal(shadowed[2].address, "shared [cccccccc-333]");
  assert.equal(shadowed[0].address, "shared [cccccccc-3333-43]");
  assert.equal(shadowed[1].address, "shared [cccccccc-444]");
  assert.equal(resolvePeerAddress(shadowed, shadowed[2].address, selfID).session_id, shadowed[2].session_id);
});

test("resolves an exact session id, an exact address, or a unique bare name", () => {
  assert.equal(resolvePeerAddress(peers, sessions[0].session_id, selfID).name, "alpha");
  assert.equal(resolvePeerAddress(peers, peers[3].address, selfID).name, "shared");
  assert.equal(resolvePeerAddress(peers, "beta", selfID).name, "beta");
});

test("rejects an ambiguous bare name with the disambiguated addresses", () => {
  assert.throws(
    () => resolvePeerAddress(peers, "shared", selfID),
    /peer name "shared" is ambiguous; use one of: "shared \[cccccccc-333\]", "shared \[cccccccc-444\]"/,
  );
});

test("rejects a self target by id, name, or address", () => {
  assert.throws(() => resolvePeerAddress(peers, selfID, selfID), /cannot send a peer request to itself/);
  assert.throws(() => resolvePeerAddress(peers, "self", selfID), /cannot send a peer request to itself/);
});

test("reports the available addresses when nothing matches", () => {
  assert.throws(
    () => resolvePeerAddress(peers, "missing", selfID),
    (error: Error) => {
      assert.match(error.message, /no active peer session matches "missing"/);
      for (const address of ["alpha", "beta", "Session dddddddd", "shared [cccccccc-333]", "shared [cccccccc-444]"]) {
        assert.ok(error.message.includes(JSON.stringify(address)), `missing candidate ${address}`);
      }
      return true;
    },
  );
});
