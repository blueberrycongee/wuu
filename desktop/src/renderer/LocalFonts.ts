export async function listLocalFonts(): Promise<string[]> {
  const host = window as Window & { queryLocalFonts?: () => Promise<Array<{ family: string }>> };
  if (!host.queryLocalFonts) throw new Error("Local font access is unavailable");
  const fonts = await host.queryLocalFonts();
  return [...new Set(fonts.map((font) => font.family).filter(Boolean))].sort((a, b) => a.localeCompare(b));
}

export function isMonospaceFont(family: string): boolean {
  const context = document.createElement("canvas").getContext("2d");
  if (!context) return false;
  context.font = `16px ${JSON.stringify(family)}`;
  const widths = ["iiiiiiii", "WWWWWWWW", "00000000", "........"].map((text) => context.measureText(text).width);
  return widths[0] > 0 && widths.every((width) => Math.abs(width - widths[0]) < 0.01);
}
