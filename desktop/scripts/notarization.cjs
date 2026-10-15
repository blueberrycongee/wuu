const { execFileSync } = require("node:child_process");
const { createHash } = require("node:crypto");
const { existsSync, mkdirSync, readFileSync, writeFileSync, renameSync } = require("node:fs");
const { dirname, basename } = require("node:path");
const { setTimeout } = require("node:timers/promises");

function isTransientNetworkError(error) {
  const diagnostic = `${error.message}\n${error.stderr || ""}\n${error.stdout || ""}`;
  return error.code === "ETIMEDOUT" || /NSURLErrorDomain|ECONNRESET|ENETUNREACH|EAI_AGAIN|timed out|network|offline|connection|\b50[234]\b|\b429\b/i.test(diagnostic);
}

async function stapleArtifact(artifact, { run = execFileSync, sleep = setTimeout } = {}) {
  const maxAttempts = 3;
  for (let attempt = 1; attempt <= maxAttempts; attempt++) {
    try {
      const output = run("xcrun", ["stapler", "staple", artifact], { encoding: "utf8", timeout: 60_000 });
      if (output) console.log(output.trimEnd());
      return;
    } catch (error) {
      if (!isTransientNetworkError(error) || attempt === maxAttempts) {
        if (error.stdout) process.stdout.write(error.stdout);
        if (error.stderr) process.stderr.write(error.stderr);
        throw error;
      }
      // Retrieve the existing accepted ticket again; never resubmit the archive.
      console.warn(`Ticket download interrupted; retry ${attempt}/${maxAttempts - 1} for ${artifact}`);
      await sleep(30_000);
    }
  }
}

async function notarizeArchive(archive, receiptPath, {
  run = execFileSync, sleep = setTimeout, maxPolls = 60, maxNetworkErrors = 5,
} = {}) {
  const sha256 = createHash("sha256").update(readFileSync(archive)).digest("hex");
  const auth = ["--key", process.env.APPLE_API_KEY, "--key-id", process.env.APPLE_API_KEY_ID,
    "--issuer", process.env.APPLE_API_ISSUER, "--output-format", "json"];
  let receipt;
  const save = () => {
    mkdirSync(dirname(receiptPath), { recursive: true });
    writeFileSync(`${receiptPath}.tmp`, JSON.stringify(receipt, null, 2) + "\n");
    renameSync(`${receiptPath}.tmp`, receiptPath);
  };
  if (existsSync(receiptPath)) {
    receipt = JSON.parse(readFileSync(receiptPath, "utf8"));
    if (receipt.sha256 !== sha256) throw new Error("Notarization receipt hash does not match archive");
  } else {
    // Never retry an ambiguous upload: Apple may already have received it.
    let submission;
    try {
      submission = JSON.parse(run("xcrun", ["notarytool", "submit", archive, "--no-wait", ...auth], {
        encoding: "utf8", timeout: 10 * 60_000,
      }));
    } catch {
      throw new Error("Notarization upload failed or its outcome is unknown; inspect Apple history before retrying");
    }
    receipt = { id: submission.id, sha256, archive: basename(archive), status: "Submitted" };
  }
  if (!/^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/i.test(receipt.id)) {
    throw new Error("Missing or invalid notarization submission ID");
  }
  save();
  console.log(`Notarization submission ${receipt.id}; receipt: ${receiptPath}`);
  let networkErrors = 0;
  const deadline = Date.now() + 30 * 60_000;
  for (let poll = 0; poll < maxPolls && Date.now() < deadline; poll++) {
    let output;
    try {
      output = run("xcrun", ["notarytool", "info", receipt.id, ...auth], { encoding: "utf8", timeout: 60_000 });
    } catch (error) {
      if (!isTransientNetworkError(error)) throw new Error(`Notarization query failed for ${receipt.id}; check credentials and service response`);
      if (++networkErrors >= maxNetworkErrors) throw new Error(`Notarization network retry limit reached for ${receipt.id}`);
      console.warn(`Notarization query interrupted; retry ${networkErrors}/${maxNetworkErrors} for ${receipt.id}`);
      await sleep(30_000);
      continue;
    }
    networkErrors = 0;
    const result = JSON.parse(output);
    if (result.id !== receipt.id) throw new Error("Notarization response submission ID mismatch");
    receipt.status = result.status;
    save();
    console.log(`Notarization ${receipt.id}: ${result.status}`);
    if (result.status === "Accepted") return receipt;
    if (result.status !== "In Progress") throw new Error(`Notarization ${receipt.id}: ${result.status}; inspect its Apple submission log`);
    await sleep(30_000);
  }
  throw new Error(`Notarization waiting limit reached for ${receipt.id}; Apple may still be processing`);
}

module.exports = { notarizeArchive, stapleArtifact };
