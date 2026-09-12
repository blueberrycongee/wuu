import type {
  ConversationProcessItemV1 as SDKConversationProcessItemV1,
  ConversationProcessSnapshotV1 as SDKConversationProcessSnapshotV1,
  ViewPlacementContribution as SDKViewPlacementContribution,
} from "../../../packages/plugin-sdk/src/index";
import type {
  ConversationProcessItemV1 as DesktopConversationProcessItemV1,
  ConversationProcessSnapshotV1 as DesktopConversationProcessSnapshotV1,
  ViewPlacementContribution as DesktopViewPlacementContribution,
} from "./workbench";

type Extends<Left, Right> = Left extends Right ? true : false;
type Assert<T extends true> = T;

type SDKSnapshotMatchesDesktop = Assert<Extends<SDKConversationProcessSnapshotV1, DesktopConversationProcessSnapshotV1>>;
type DesktopSnapshotMatchesSDK = Assert<Extends<DesktopConversationProcessSnapshotV1, SDKConversationProcessSnapshotV1>>;
type SDKItemMatchesDesktop = Assert<Extends<SDKConversationProcessItemV1, DesktopConversationProcessItemV1>>;
type DesktopItemMatchesSDK = Assert<Extends<DesktopConversationProcessItemV1, SDKConversationProcessItemV1>>;
type SDKPlacementMatchesDesktop = Assert<Extends<SDKViewPlacementContribution, DesktopViewPlacementContribution>>;
type DesktopPlacementMatchesSDK = Assert<Extends<DesktopViewPlacementContribution, SDKViewPlacementContribution>>;
