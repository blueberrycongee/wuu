import type { ReactNode } from "react";
import type { ChannelRoomOnboarding } from "../shared/protocol";
import { AgentAvatarMark } from "./AgentAvatarMark";
import { MessageBubble, MessageBubbleRow } from "./MessageBubbleFlow";
import "./styles/agent-onboarding.css";

export function AgentOnboardingHistory({ onboarding, modelAction }: { onboarding: ChannelRoomOnboarding; modelAction?: ReactNode }): JSX.Element {
  const avatar = <AgentAvatarMark seed="draft-agent" avatarKey={onboarding.avatar_key} />;
  return <>
    <MessageBubbleRow messageID="model" outgoing={false} avatar={avatar} className="channel-message agent" contentClassName="channel-message-content">
      <MessageBubble outgoing={false} className="agent-onboarding-bubble">
        <div className="agent-onboarding-form">
          <p className="agent-onboarding-prompt">{onboarding.model_prompt}</p>
          <div className="agent-onboarding-history-model">{onboarding.provider} / {onboarding.model}</div>
          {onboarding.effort ? <div className="agent-onboarding-history-effort">{onboarding.effort}</div> : null}
          {modelAction}
        </div>
      </MessageBubble>
    </MessageBubbleRow>
    <MessageBubbleRow messageID="name" outgoing={false} avatar={avatar} className="channel-message agent" contentClassName="channel-message-content">
      <MessageBubble outgoing={false} className="channel-message-bubble">{onboarding.name_prompt}</MessageBubble>
    </MessageBubbleRow>
    {onboarding.name ? <MessageBubbleRow messageID="answer" outgoing className="channel-message own" contentClassName="channel-message-content">
      <MessageBubble outgoing className="channel-message-bubble">{onboarding.name}</MessageBubble>
    </MessageBubbleRow> : null}
  </>;
}
