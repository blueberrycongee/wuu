// Select recovery before importing the product: product module constructors and
// normal configuration must not be prerequisites for a diagnostic launch.
// Await the selected module so Electron does not become ready before the
// product registers its privileged protocols.
if (process.argv.includes("--safe-mode") || process.env.WUU_SAFE_MODE === "1") {
  await import("./desktopRecoveryEntry");
} else {
  await import("./index");
}
export {};
