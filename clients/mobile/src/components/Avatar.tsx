// Participant avatar: an uploaded avatar_image data URL when present,
// otherwise the deterministic blobatar fallback (same FNV-1a hue assignment
// as the desktop, so identities match across ends). RN <Image> cannot decode
// SVG, so the blobatar string API output is parsed into react-native-svg
// parts in lib/avatar.ts.

import { useMemo } from "react";
import { Image, StyleSheet, View } from "react-native";
import Svg, { Circle, Path } from "react-native-svg";

import { avatarSvgParts } from "../lib/avatar";
import { usePalette } from "../theme";

export type AvatarStatus = "online" | "busy" | null;

export function Avatar({
  id,
  name,
  imageUri,
  size,
  status = null,
}: {
  id?: string;
  name?: string;
  imageUri?: string;
  size: number;
  status?: AvatarStatus;
}): React.JSX.Element {
  const palette = usePalette();
  const parts = useMemo(() => avatarSvgParts(id, name), [id, name]);

  return (
    <View style={{ width: size, height: size }}>
      <View
        style={[
          styles.circle,
          { width: size, height: size, borderRadius: size / 2 },
        ]}
      >
        {imageUri ? (
          <Image source={{ uri: imageUri }} style={styles.fill} resizeMode="cover" />
        ) : (
          <Svg width={size} height={size} viewBox="0 0 100 100">
            {parts.plate ? <Path d={parts.plate.d} fill={parts.plate.fill} /> : null}
            {parts.head.paths[0] ? <Path d={parts.head.paths[0]} fill={parts.head.fill} /> : null}
            {parts.head.circles.map((circle, index) => (
              <Circle
                key={`head-circle-${index}`}
                cx={circle.cx}
                cy={circle.cy}
                r={circle.r}
                fill={parts.head.fill}
              />
            ))}
            {parts.eyes.paths.map((d, index) => (
              <Path key={`eye-${index}`} d={d} fill={parts.eyes.fill} />
            ))}
          </Svg>
        )}
      </View>
      {status ? (
        <View
          style={[
            styles.statusDot,
            {
              backgroundColor: status === "online" ? palette.success : palette.warning,
              borderColor: palette.paper,
            },
          ]}
        />
      ) : null}
    </View>
  );
}

const styles = StyleSheet.create({
  circle: {
    overflow: "hidden",
    alignItems: "stretch",
  },
  fill: {
    flex: 1,
    width: undefined,
    height: undefined,
  },
  statusDot: {
    position: "absolute",
    top: -1,
    right: -1,
    width: 9,
    height: 9,
    borderRadius: 5,
    borderWidth: 1.5,
  },
});
