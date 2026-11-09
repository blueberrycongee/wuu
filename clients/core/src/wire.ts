// The two message vocabularies of wuu remote control, mirrored from
// internal/remote/wire (the reference):
//
//   - Relay leg: JSON text frames between a device and the relay. Routing
//     metadata and opaque payloads only.
//   - End-to-end leg: JSON messages between phone and host inside relay frame
//     payloads. Apart from the two handshake messages, everything travels
//     sealed by the secure channel.
//
// A relay frame payload is a byte slice with a one-byte kind prefix.

import { concatBytes } from "./bytes";

export const PROTO_VERSION = 1;

// Relay message types (RelayMsg.type).
export const TYPE_HELLO = "hello";
export const TYPE_CHALLENGE = "challenge";
export const TYPE_AUTH = "auth";
export const TYPE_AUTH_OK = "auth_ok";
export const TYPE_AUTH_ERR = "auth_err";
export const TYPE_PAIR_OPEN = "pair_open";
export const TYPE_PAIR_CLOSE = "pair_close";
export const TYPE_PAIR_OFFER = "pair_offer";
export const TYPE_PAIR_ANSWER = "pair_answer";
export const TYPE_PAIR_ERR = "pair_err";
export const TYPE_DEVICE_ADD = "device_add";
export const TYPE_DEVICE_RM = "device_remove";
export const TYPE_DEVICE_LIST = "device_list";
export const TYPE_DEVICES = "devices";
export const TYPE_FRAME = "frame";
export const TYPE_DELIVER_ERR = "deliver_err";
export const TYPE_PRESENCE = "presence";
export const TYPE_PUSH = "push";
export const TYPE_ERR = "err";
export const TYPE_OK = "ok";

// Device roles.
export const ROLE_HOST = "host";
export const ROLE_PHONE = "phone";

// Push hints (content-free by design).
export const PUSH_NEEDS_INPUT = "needs_input";
export const PUSH_AGENT_DONE = "agent_done";

export interface RelayDevice {
  pub: string;
  name?: string;
  added_at?: string;
  online?: boolean;
}

/** Single envelope for every relay-leg message; type selects the fields. */
export interface RelayMsg {
  type: string;

  // hello / auth
  proto?: number;
  role?: string;
  pub?: string;
  nonce?: string;
  sig?: string;

  // pairing
  pairing_id?: string;

  // device management
  name?: string;
  devices?: RelayDevice[];

  // data frames
  to?: string;
  from?: string;
  payload?: string;

  // presence
  online?: boolean;

  // push
  hint?: string;

  // errors
  code?: string;
  msg?: string;
}

// Payload kind prefixes for relay frame payloads.
export const KIND_HANDSHAKE = 0x01;
export const KIND_SEALED = 0x02;

export function wrapPayload(kind: number, body: Uint8Array): Uint8Array {
  return concatBytes(new Uint8Array([kind]), body);
}

export function splitPayload(payload: Uint8Array): { kind: number; body: Uint8Array } {
  if (payload.length < 1) throw new Error("empty frame payload");
  return { kind: payload[0], body: payload.subarray(1) };
}

// --- End-to-end messages -----------------------------------------------------

export const E2E_HS1 = "hs1";
export const E2E_HS2 = "hs2";
export const E2E_ATTACH = "attach";
export const E2E_ATTACHED = "attached";
export const E2E_RPC = "rpc";
export const E2E_ACK = "ack";
export const E2E_STATE = "state";
export const E2E_PING = "ping";
export const E2E_PONG = "pong";
export const E2E_BYE = "bye";

export const CLIENT_PROFILE_MOBILE_CHAT = "mobile_chat";

export interface HostInfo {
  name?: string;
  version?: string;
  workdir?: string;
  provider?: string;
  model?: string;
}

export interface RunningTurn {
  thread_id: string;
  turn_id?: string;
  started_at?: string;
}

/** Envelope for every end-to-end message; t selects the fields. See the
 *  reliability model notes in internal/remote/wire. */
export interface E2EMsg {
  t: string;

  // hs1/hs2
  device_pub?: string;
  eph?: string;
  nonce?: string;
  sig?: string;

  // attach / attached
  prev?: string;
  recv?: number;
  client_profile?: string;
  accept_line_compression?: "gzip";
  session?: string;
  resumed?: boolean;
  replay_from?: number;

  // rpc: one line of app-server JSON, embedded as a raw JSON value
  seq?: number;
  line?: unknown;
  /** Independently gzipped JSON line, raw base64url, inside the sealed envelope. */
  line_gzip?: string;

  // state
  ver?: number;
  host?: HostInfo;
  running?: RunningTurn[];

  // bye
  reason?: string;
}

export function encodeE2E(m: E2EMsg): string {
  return JSON.stringify(m);
}

export function decodeE2E(data: string): E2EMsg {
  const m = JSON.parse(data) as E2EMsg;
  if (!m || typeof m.t !== "string" || m.t === "") {
    throw new Error("e2e message missing type");
  }
  return m;
}
