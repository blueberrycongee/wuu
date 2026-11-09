// Time-of-day greeting for the empty conversation home, zh wuu variant.
// Mirrored from desktop/src/renderer/greetings.ts (wuu context only; the
// phone pairs to one workspace so there is no per-project greeting).

export function greetingFor(hour: number): string {
  if (hour >= 5 && hour < 11) return "早上好，今天想先搞定什么？";
  if (hour >= 11 && hour < 14) return "中午好，休息一下，还是接着做点什么？";
  if (hour >= 14 && hour < 18) return "下午好，有什么我能帮忙推进的？";
  if (hour >= 18 && hour < 22) return "晚上好，今天还想处理什么？";
  return "夜深了，还要继续吗？";
}
