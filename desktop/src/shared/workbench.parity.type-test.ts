import type * as SDK from "../../../packages/plugin-sdk/src/index";
import type * as Desktop from "./workbench";

type Extends<Left, Right> = Left extends Right ? true : false;
type Equal<Left, Right> = Extends<Left, Right> extends true
  ? Extends<Right, Left> extends true ? true : false
  : false;
type Expect<T extends true> = T;

type SnapshotParity = [
  Expect<Equal<SDK.DestinationDefinition, Desktop.DestinationDefinition>>,
  Expect<Equal<SDK.RibbonItemDefinition, Desktop.RibbonItemDefinition>>,
  Expect<Equal<SDK.ConversationItemSnapshotV1, Desktop.ConversationItemSnapshotV1>>,
  Expect<Equal<SDK.ComposerSnapshotV1, Desktop.ComposerSnapshotV1>>,
  Expect<Equal<SDK.HeaderSnapshotV1, Desktop.HeaderSnapshotV1>>,
  Expect<Equal<SDK.NavigationSnapshotV1, Desktop.NavigationSnapshotV1>>,
  Expect<Equal<SDK.StatusSnapshotV1, Desktop.StatusSnapshotV1>>,
  Expect<Equal<SDK.FilePreviewSnapshotV1, Desktop.FilePreviewSnapshotV1>>,
  Expect<Equal<SDK.SettingsSnapshotV1, Desktop.SettingsSnapshotV1>>,
];

type ActionParity = [
  Expect<Equal<SDK.ConversationItemActionId, Desktop.ConversationItemActionId>>,
  Expect<Equal<SDK.ComposerActionId, Desktop.ComposerActionId>>,
  Expect<Equal<SDK.HeaderActionId, Desktop.HeaderActionId>>,
  Expect<Equal<SDK.NavigationActionId, Desktop.NavigationActionId>>,
  Expect<Equal<SDK.StatusActionId, Desktop.StatusActionId>>,
  Expect<Equal<SDK.FilePreviewActionId, Desktop.FilePreviewActionId>>,
  Expect<Equal<SDK.SettingsActionId, Desktop.SettingsActionId>>,
];

export type WorkbenchPublicContractParity = readonly [SnapshotParity, ActionParity];
