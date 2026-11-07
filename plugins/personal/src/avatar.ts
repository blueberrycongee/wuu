export const PERSONAL_AVATAR_SHAPES = ["round", "rounded-square", "capsule", "triangle", "diamond"] as const;
export const PERSONAL_AVATAR_ACCESSORIES = ["none", "beanie", "hard-hat", "headset", "leaf"] as const;
export const PERSONAL_AVATAR_HUES = [14, 33, 52, 96, 150, 182, 202, 222, 250, 288, 322, 350] as const;
export const DEFAULT_PERSONAL_AVATAR = "mascot-v1:round:headset:222";

export function validatePersonalAvatar(value: unknown): string {
  if (typeof value !== "string") throw new Error("avatar_key must be a mascot-v1 style");
  const [version, shape, accessory, hue, ...rest] = value.split(":");
  if (version !== "mascot-v1" || rest.length || !PERSONAL_AVATAR_SHAPES.some(item => item === shape)
    || !PERSONAL_AVATAR_ACCESSORIES.some(item => item === accessory) || !/^(0|[1-9][0-9]{0,2})$/.test(hue)
    || Number(hue) > 359) throw new Error("invalid avatar_key shape, accessory or hue");
  return value;
}
