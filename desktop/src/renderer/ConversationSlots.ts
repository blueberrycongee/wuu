import { registerConversationContributions } from "./plugins/DefaultUIContributions";
import type { ApplicationPresentationAPI } from "../../../packages/plugin-sdk/src/ui";

/** Each surface declares the conversation leaves it renders in its own generation. */
export function registerConversationSlots(api: ApplicationPresentationAPI): void {
  for (const [id, cardinality] of [
    ["conversation.turn", "one"], ["conversation.cards", "many"], ["conversation.message", "one"],
    ["conversation.message.before", "many"], ["conversation.message.after", "many"],
    ["conversation.process", "one"], ["conversation.message.actions", "many"],
    ["conversation.tool-activity", "one"], ["conversation.artifact", "one"],
  ] as const) api.registerUISlot({ id, cardinality });
  registerConversationContributions(api);
}
