// Deep-link URL parsing. The mobile app registers `wuu://` as a scheme
// (see app.json) and the host's push payload carries a `data.url` of the
// same shape. Centralizing the parser here keeps the route mapping in one
// place — adding a new destination is a single match arm.
//
// Supported shapes:
//   wuu://thread/<id>        → open the chat thread with that id
//   wuu://chat/<id>          → alias of `thread`, kept for the host's
//                              older payload format
//   wuu://home               → land on the chats list
//
// Any other shape (or a URL we cannot parse) returns null so the caller
// can fall back to the default route without logging noise.

export type DeepLink =
  | { kind: "thread"; threadId: string }
  | { kind: "home" };

export interface DeepLinkGate {
  receive(link: DeepLink | null): void;
  completeStartup(hasCredentials: boolean): void;
  markPaired(): void;
}

/** Holds a cold-start link until credential loading finishes. A link cannot
 *  open a thread without a client, and the normal startup route must not
 *  overwrite a valid link immediately after it opens. */
export function createDeepLinkGate(
  dispatch: (link: DeepLink) => void,
): DeepLinkGate {
  let credentialState: "loading" | "ready" | "missing" = "loading";
  let pending: DeepLink | null = null;

  return {
    receive(link) {
      if (!link) return;
      if (credentialState === "ready") {
        dispatch(link);
      } else if (credentialState === "loading") {
        pending = link;
      }
    },
    completeStartup(hasCredentials) {
      credentialState = hasCredentials ? "ready" : "missing";
      const link = pending;
      pending = null;
      if (hasCredentials && link) dispatch(link);
    },
    markPaired() {
      credentialState = "ready";
    },
  };
}

const SCHEME = "wuu";

export function parseDeepLink(input: string | null | undefined): DeepLink | null {
  if (!input) return null;
  let url: URL;
  try {
    url = new URL(input);
  } catch {
    return null;
  }
  if (url.protocol !== `${SCHEME}:`) return null;
  // wuu://thread/abc → host="thread", pathname="/abc"
  // wuu:///thread/abc → host="", pathname="/thread/abc"
  const host = url.host || url.hostname;
  const segments = url.pathname.split("/").filter((s) => s.length > 0);
  const route = host || segments.shift() || "";
  switch (route) {
    case "thread":
    case "chat": {
      const id = segments[0] ?? (host ? url.pathname.replace(/^\/+/, "") : "");
      if (!id) return null;
      return { kind: "thread", threadId: id };
    }
    case "home":
      return { kind: "home" };
    default:
      return null;
  }
}
