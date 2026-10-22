import { ScrollText, type IconProps } from "./WuuIcons";

/** Shared Skills mark: a playbook scroll, paired with PluginBlocksIcon for extensions. */
export function SkillsIcon(props: IconProps): JSX.Element {
  return <ScrollText aria-hidden="true" data-icon="skills-scroll" {...props} />;
}
