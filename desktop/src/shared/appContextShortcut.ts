export function normalizeAppContextShortcut(value: string): string {
  const parts = value.trim().split("+");
  const key = parts.pop()?.toUpperCase() ?? "";
  const aliases: Record<string, string> = { commandorcontrol: "Command", cmdorctrl: "Command", command: "Command", cmd: "Command", ctrl: "Control", control: "Control", alt: "Alt", option: "Alt", shift: "Shift", super: "Command" };
  const modifiers = parts.map(part => aliases[part.trim().toLowerCase()]);
  if (value.length > 100 || modifiers.some(modifier => !modifier)
      || new Set(modifiers).size !== modifiers.length
      || !modifiers.some(modifier => modifier !== "Shift")
      || !/^(?:[A-Z0-9]|SPACE|F(?:[1-9]|1[0-9]|2[0-4]))$/.test(key)) {
    throw new Error("Use Command, Control, or Option with a letter, number, Space, or function key.");
  }
  return [...["Command", "Control", "Alt", "Shift"].filter(modifier => modifiers.includes(modifier)), key === "SPACE" ? "Space" : key].join("+");
}
