// The empty-state signature moment (desktop DESIGN.md rule #1): mascot +
// serif greeting. One decoration per screen — nothing else on the page
// carries style.

import { Image, ImageSourcePropType, StyleSheet, Text, View } from "react-native";

import { serifFamily, usePalette } from "../theme";

export function EmptyState({
  image,
  title,
  hint,
  imageSize = 132,
}: {
  image: ImageSourcePropType;
  title: string;
  hint?: string;
  imageSize?: number;
}): React.JSX.Element {
  const palette = usePalette();
  return (
    <View style={styles.wrap}>
      <Image source={image} style={{ width: imageSize, height: imageSize }} resizeMode="contain" />
      <Text style={[styles.title, { color: palette.ink }]}>{title}</Text>
      {hint ? <Text style={[styles.hint, { color: palette.inkMuted }]}>{hint}</Text> : null}
    </View>
  );
}

const styles = StyleSheet.create({
  wrap: {
    flex: 1,
    alignItems: "center",
    justifyContent: "center",
    gap: 14,
    paddingHorizontal: 40,
    paddingBottom: 48,
  },
  title: {
    fontFamily: serifFamily,
    fontSize: 22,
    fontWeight: "500",
    textAlign: "center",
    lineHeight: 30,
  },
  hint: {
    fontSize: 13,
    textAlign: "center",
    lineHeight: 20,
  },
});
