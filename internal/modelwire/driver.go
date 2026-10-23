package modelwire

import (
	"errors"

	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func DriverMessagesToWire(messages []providers.ChatMessage) []pluginprotocol.DriverMessageV1 {
	result := make([]pluginprotocol.DriverMessageV1, 0, len(messages))
	for _, message := range messages {
		result = append(result, pluginprotocol.DriverMessageV1{ModelMessageV1: ChatMessageToWire(message), ToolInvocationID: message.ToolInvocationID, ClientID: message.ClientID, Seq: message.Seq, Steered: message.Steered})
	}
	return result
}

func DriverMessagesFromWire(messages []pluginprotocol.DriverMessageV1) []providers.ChatMessage {
	result := make([]providers.ChatMessage, 0, len(messages))
	for _, message := range messages {
		item := ChatMessageFromWire(message.ModelMessageV1)
		item.ToolInvocationID, item.ClientID, item.Seq, item.Steered = message.ToolInvocationID, message.ClientID, message.Seq, message.Steered
		result = append(result, item)
	}
	return result
}

func DriverRequestToWire(request providers.ChatRequest) pluginprotocol.DriverTurnReadResult {
	return pluginprotocol.DriverTurnReadResult{LogicalModelRequestV1: RequestToWire(request), Messages: DriverMessagesToWire(request.Messages)}
}

// DriverRequestFromWire reads the turn's current input, which can be empty or
// contain pending tools. Submission validation belongs to ApplyDriverRequest.
func DriverRequestFromWire(in pluginprotocol.DriverTurnReadResult) (providers.ChatRequest, error) {
	if in.Version != 1 {
		return providers.ChatRequest{}, errors.New("unsupported logical model request version")
	}
	request := requestFromWire(providers.ChatRequest{}, in.LogicalModelRequestV1)
	request.Messages = DriverMessagesFromWire(in.Messages)
	return request, nil
}

func ApplyDriverRequest(in pluginprotocol.DriverTurnReadResult) (providers.ChatRequest, error) {
	logical := in.LogicalModelRequestV1
	logical.Messages = make([]pluginprotocol.ModelMessageV1, 0, len(in.Messages))
	for _, message := range in.Messages {
		logical.Messages = append(logical.Messages, message.ModelMessageV1)
	}
	request, err := ApplyRequest(providers.ChatRequest{}, logical)
	if err != nil {
		return providers.ChatRequest{}, err
	}
	for index, message := range in.Messages {
		item := &request.Messages[index]
		item.ToolInvocationID, item.ClientID, item.Seq, item.Steered = message.ToolInvocationID, message.ClientID, message.Seq, message.Steered
	}
	return request, nil
}
