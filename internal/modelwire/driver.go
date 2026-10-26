package modelwire

import (
	"errors"

	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func DriverMessagesToWire(messages []providers.ChatMessage) []pluginprotocol.DriverMessageV1 {
	result := make([]pluginprotocol.DriverMessageV1, 0, len(messages))
	for _, message := range messages {
		item := pluginprotocol.DriverMessageV1{ModelMessageV1: ChatMessageToWire(message), ToolInvocationID: message.ToolInvocationID, ClientID: message.ClientID, Seq: message.Seq, Steered: message.Steered,
			DisplayContent: message.DisplayContent, PresentationKind: message.PresentationKind, RelatedSessionID: message.RelatedSessionID, EngineTurnID: message.EngineTurnID, EngineTurnPending: message.EngineTurnPending}
		paths := &pluginprotocol.DriverWorkingCopiesV1{}
		hasWorkingCopy := false
		for _, image := range message.Images {
			paths.Images = append(paths.Images, image.LocalPath)
			hasWorkingCopy = hasWorkingCopy || image.LocalPath != ""
		}
		for _, file := range message.Files {
			paths.Files = append(paths.Files, file.LocalPath)
			hasWorkingCopy = hasWorkingCopy || file.LocalPath != ""
		}
		if hasWorkingCopy {
			item.WorkingCopies = paths
		}
		for _, part := range message.ContentParts {
			item.ContentParts = append(item.ContentParts, driverMessageContentPartToWire(part))
		}
		result = append(result, item)
	}
	return result
}

func DriverMessagesFromWire(messages []pluginprotocol.DriverMessageV1) []providers.ChatMessage {
	result := make([]providers.ChatMessage, 0, len(messages))
	for _, message := range messages {
		item := ChatMessageFromWire(message.ModelMessageV1)
		item.ToolInvocationID, item.ClientID, item.Seq, item.Steered = message.ToolInvocationID, message.ClientID, message.Seq, message.Steered
		item.DisplayContent, item.PresentationKind, item.RelatedSessionID = message.DisplayContent, message.PresentationKind, message.RelatedSessionID
		item.EngineTurnID, item.EngineTurnPending = message.EngineTurnID, message.EngineTurnPending
		restoreDriverWorkingCopies(&item, message.WorkingCopies)
		for _, part := range message.ContentParts {
			item.ContentParts = append(item.ContentParts, driverMessageContentPartFromWire(part))
		}
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
		item.DisplayContent, item.PresentationKind, item.RelatedSessionID = message.DisplayContent, message.PresentationKind, message.RelatedSessionID
		item.EngineTurnID, item.EngineTurnPending = message.EngineTurnID, message.EngineTurnPending
		restoreDriverWorkingCopies(item, message.WorkingCopies)
		for _, part := range message.ContentParts {
			item.ContentParts = append(item.ContentParts, driverMessageContentPartFromWire(part))
		}
	}
	return request, nil
}

func driverMessageContentPartToWire(in providers.MessageContentPart) (out pluginprotocol.DriverMessageContentPartV1) {
	out.Type = in.Type
	out.LocalPath = in.LocalPath
	out.Text = in.Text
	out.Title = in.Title
	if in.Source != nil {
		v := driverFileSelectionSourceToWire(*in.Source)
		out.Source = &v
	}
	out.Intent = in.Intent
	out.Comment = in.Comment
	out.ID = in.ID
	if in.Selection != nil {
		v := driverResponseSelectionToWire(*in.Selection)
		out.Selection = &v
	}
	return out
}

func driverMessageContentPartFromWire(in pluginprotocol.DriverMessageContentPartV1) (out providers.MessageContentPart) {
	out.Type = in.Type
	out.LocalPath = in.LocalPath
	out.Text = in.Text
	out.Title = in.Title
	if in.Source != nil {
		v := driverFileSelectionSourceFromWire(*in.Source)
		out.Source = &v
	}
	out.Intent = in.Intent
	out.Comment = in.Comment
	out.ID = in.ID
	if in.Selection != nil {
		v := driverResponseSelectionFromWire(*in.Selection)
		out.Selection = &v
	}
	return out
}

func driverFileSelectionSourceToWire(in providers.FileSelectionSource) (out pluginprotocol.DriverFileSelectionSourceV1) {
	out.Workspace = in.Workspace
	out.Path = in.Path
	out.StartLine = in.StartLine
	out.StartColumn = in.StartColumn
	out.EndLine = in.EndLine
	out.EndColumn = in.EndColumn
	out.Quote = in.Quote
	out.Revision = in.Revision
	if in.PDF != nil {
		v := driverPDFSelectionReferenceToWire(*in.PDF)
		out.PDF = &v
	}
	return out
}

func driverFileSelectionSourceFromWire(in pluginprotocol.DriverFileSelectionSourceV1) (out providers.FileSelectionSource) {
	out.Workspace = in.Workspace
	out.Path = in.Path
	out.StartLine = in.StartLine
	out.StartColumn = in.StartColumn
	out.EndLine = in.EndLine
	out.EndColumn = in.EndColumn
	out.Quote = in.Quote
	out.Revision = in.Revision
	if in.PDF != nil {
		v := driverPDFSelectionReferenceFromWire(*in.PDF)
		out.PDF = &v
	}
	return out
}

func driverPDFSelectionReferenceToWire(in providers.PDFSelectionReference) (out pluginprotocol.DriverPDFSelectionReferenceV1) {
	out.StartPage = in.StartPage
	out.EndPage = in.EndPage
	out.ArtifactURI = in.ArtifactURI
	out.ArtifactSHA256 = in.ArtifactSHA256
	out.ArtifactThreadID = in.ArtifactThreadID
	return out
}

func driverPDFSelectionReferenceFromWire(in pluginprotocol.DriverPDFSelectionReferenceV1) (out providers.PDFSelectionReference) {
	out.StartPage = in.StartPage
	out.EndPage = in.EndPage
	out.ArtifactURI = in.ArtifactURI
	out.ArtifactSHA256 = in.ArtifactSHA256
	out.ArtifactThreadID = in.ArtifactThreadID
	return out
}

func driverResponseSelectionToWire(in providers.ResponseSelection) (out pluginprotocol.DriverResponseSelectionV1) {
	out.ID = in.ID
	out.Text = in.Text
	out.Comment = in.Comment
	out.Source = driverResponseSelectionSourceToWire(in.Source)
	return out
}

func driverResponseSelectionFromWire(in pluginprotocol.DriverResponseSelectionV1) (out providers.ResponseSelection) {
	out.ID = in.ID
	out.Text = in.Text
	out.Comment = in.Comment
	out.Source = driverResponseSelectionSourceFromWire(in.Source)
	return out
}

func driverResponseSelectionSourceToWire(in providers.ResponseSelectionSource) (out pluginprotocol.DriverResponseSelectionSourceV1) {
	out.ThreadID = in.ThreadID
	out.TurnID = in.TurnID
	out.ItemID = in.ItemID
	out.StartOffset = in.StartOffset
	out.EndOffset = in.EndOffset
	out.RangeText = in.RangeText
	return out
}

func driverResponseSelectionSourceFromWire(in pluginprotocol.DriverResponseSelectionSourceV1) (out providers.ResponseSelectionSource) {
	out.ThreadID = in.ThreadID
	out.TurnID = in.TurnID
	out.ItemID = in.ItemID
	out.StartOffset = in.StartOffset
	out.EndOffset = in.EndOffset
	out.RangeText = in.RangeText
	return out
}

// The positional lists parallel the canonical media arrays. They do not grant
// authority to read files and never enter ChatMessageToWire/model requests.
func restoreDriverWorkingCopies(message *providers.ChatMessage, paths *pluginprotocol.DriverWorkingCopiesV1) {
	if paths == nil {
		return
	}
	for i, path := range paths.Images {
		if i < len(message.Images) {
			message.Images[i].LocalPath = path
		}
	}
	for i, path := range paths.Files {
		if i < len(message.Files) {
			message.Files[i].LocalPath = path
		}
	}
}
