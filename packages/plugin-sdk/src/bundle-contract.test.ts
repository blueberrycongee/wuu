import {
  BUNDLE_CONTRACT_VERSION,
  canonicalize,
  generation,
  validateBundleManifest,
  type CanonicalValue,
} from "./bundle-contract.js";

const decode = (bytes: Uint8Array): string => new TextDecoder().decode(bytes);

async function sha256Hex(bytes: Uint8Array): Promise<string> {
  const buffer = new ArrayBuffer(bytes.byteLength);
  new Uint8Array(buffer).set(bytes);
  const digest = await crypto.subtle.digest("SHA-256", buffer);
  return Array.from(new Uint8Array(digest), (b) => b.toString(16).padStart(2, "0")).join("");
}

const fixtures: Array<{
  name: string;
  manifest: CanonicalValue;
  content: Record<string, string>;
  golden: string;
}> = [
  {
    name: "full",
    manifest: {
      schema_version: 2,
      id: "com.example.image",
      version: "1.2.3",
      name: "Image Gen",
      description: "Generates images.",
      agent: { command: "dist/agent", args: ["--serve", "--port", "9000"], env: { WUU_MODE: "desktop" } },
      desktop: { entry: "dist/desktop.mjs" },
    },
    content: {
      "dist/agent": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      "dist/desktop.mjs": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
    },
    golden: "8dbc76c3e01ddc5b80ec1b381a3230dc6119bdeec7b49d770e506df9817cbc0e",
  },
  {
    name: "agent-only",
    manifest: {
      schema_version: 2,
      id: "com.example.headless",
      version: "0.2.0",
      agent: { command: "bin/agent" },
    },
    content: { "bin/agent": "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc" },
    golden: "8aba89d1c5e972a9818627b4f2d7549ec7ec23ee80710e9ea5bb177f8e17c64c",
  },
  {
    name: "desktop-only",
    manifest: {
      schema_version: 2,
      id: "com.example.theme",
      version: "0.3.0",
      desktop: { entry: "index.mjs" },
    },
    content: { "index.mjs": "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd" },
    golden: "5600e1d7abdf8e8c806ab2e8215b015db43b8e13adeb1d1e2c21a54dc98e17f8",
  },
  {
    name: "escaping",
    manifest: {
      schema_version: 2,
      id: "com.example.escape",
      version: "0.1.0",
      description: 'a<b>&"c"\\d\ne\tf 图 🎨',
      agent: { command: "bin/agent" },
    },
    content: { "bin/agent": "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee" },
    golden: "346410388be78541f0ee60d292d5e4a7a7e9a9b8c714e2efb6b0825989516db0",
  },
];

for (const fixture of fixtures) {
  const got = await generation(
    { contract_version: BUNDLE_CONTRACT_VERSION, manifest: fixture.manifest, content: fixture.content },
    sha256Hex,
  );
  if (got !== fixture.golden) {
    throw new Error(`generation mismatch for ${fixture.name}: got ${got}, want ${fixture.golden}`);
  }
}

if (validateBundleManifest(fixtures[0].manifest).length !== 0) {
  throw new Error("valid full manifest was rejected");
}
if (!validateBundleManifest({ ...(fixtures[0].manifest as Record<string, unknown>), schema_version: 1 }).some((e) => e.includes("schema_version"))) {
  throw new Error("v1 schema_version was not rejected");
}
if (!validateBundleManifest({ schema_version: 2, id: "a.b", version: "1.0.0" }).some((e) => e.includes("at least one"))) {
  throw new Error("manifest without a surface was not rejected");
}
if (!validateBundleManifest({ schema_version: 2, id: "a.b", version: "1.0.0", agent: {} }).some((e) => e.includes("agent.command"))) {
  throw new Error("agent without command was not rejected");
}
if (!validateBundleManifest({ schema_version: 2, id: "a.b", version: "1.0.0", desktop: { entry: "../index.mjs" } }).some((e) => e.includes("package-relative"))) {
  throw new Error("escaping desktop entry was not rejected");
}

const stable = decode(
  canonicalize({
    b: { x: "1", a: "" },
    a: ["z", 'a<b>&"c"'],
  }),
);
const expected = '{"a":["z","a<b>&\\"c\\""],"b":{"x":"1"}}';
if (stable !== expected) {
  throw new Error(`canonical bytes mismatch: got ${stable}, want ${expected}`);
}

// Keep the legacy SDK test runner exit contract: this file must throw on
// failure and otherwise finish silently.
console.log("bundle-contract tests passed");
