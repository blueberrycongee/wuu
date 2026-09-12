const AGENT_AVATAR_SOURCE_MAX_BYTES = 10 * 1024 * 1024;
const AGENT_AVATAR_SIZE = 256;
const AGENT_AVATAR_TYPES = new Set(["image/png", "image/jpeg", "image/webp"]);

export async function squareAvatarImageFromFile(file: File): Promise<string> {
  if (!AGENT_AVATAR_TYPES.has(file.type) || file.size === 0 || file.size > AGENT_AVATAR_SOURCE_MAX_BYTES) throw new Error("invalid-avatar-image");
  const bitmap = await createImageBitmap(file);
  try {
    const canvas = document.createElement("canvas");
    canvas.width = AGENT_AVATAR_SIZE;
    canvas.height = AGENT_AVATAR_SIZE;
    const context = canvas.getContext("2d");
    if (!context) throw new Error("invalid-avatar-image");
    const scale = Math.max(AGENT_AVATAR_SIZE / bitmap.width, AGENT_AVATAR_SIZE / bitmap.height);
    const width = bitmap.width * scale;
    const height = bitmap.height * scale;
    context.drawImage(bitmap, (AGENT_AVATAR_SIZE - width) / 2, (AGENT_AVATAR_SIZE - height) / 2, width, height);
    return canvas.toDataURL("image/webp", 0.86);
  } finally {
    bitmap.close();
  }
}
