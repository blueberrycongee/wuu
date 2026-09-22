import { Blocks, type IconProps } from "./WuuIcons";

/** Shared plugin mark, kept as a semantic wrapper around the product icon set. */
export function PluginBlocksIcon(props: IconProps): JSX.Element {
  return (
    <Blocks
      aria-hidden="true"
      data-icon="plugin-blocks"
      {...props}
    />
  );
}
