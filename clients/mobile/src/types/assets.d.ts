// Metro resolves image imports to opaque asset ids.
declare module "*.png" {
  import type { ImageSourcePropType } from "react-native";
  const asset: ImageSourcePropType;
  export default asset;
}

declare module "*.jpg" {
  import type { ImageSourcePropType } from "react-native";
  const asset: ImageSourcePropType;
  export default asset;
}
