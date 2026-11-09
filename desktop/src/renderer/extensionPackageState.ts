import type { ExtensionInventoryRecord, ExtensionPackageAction } from "../shared/protocol";

export function extensionPackageApproval(
  record: ExtensionInventoryRecord,
): NonNullable<ExtensionInventoryRecord["approval_state"]> {
  if (record.approval_state) {
    return record.approval_state;
  }
  return record.provenance.official ? "official" : "pending";
}

// The switch shows whether a plugin runs. Turning on an unapproved plugin
// is its approval, reviewed first when it asks for permissions.
export function pluginToggle(record: ExtensionInventoryRecord): {
  on: boolean;
  action: ExtensionPackageAction;
  review: boolean;
} {
  const approval = extensionPackageApproval(record);
  const trusted = approval === "official" || approval === "granted";
  if (trusted) {
    const on = record.enabled !== false;
    return { on, action: on ? "disable" : "enable", review: false };
  }
  return { on: false, action: "grant", review: (record.requested_permissions?.length ?? 0) > 0 };
}
