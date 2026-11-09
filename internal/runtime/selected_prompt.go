package runtime

import (
	"context"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/prompt"
	"github.com/blueberrycongee/wuu/internal/promptservice"
)

func assembleSelectedPrompt(ctx context.Context, host *pluginhost.Host, input promptservice.Request, contributions *agent.SystemPromptAssembler) (prompt.BuildResult, error) {
	if contributions != nil {
		text, sections := contributions.Assemble("")
		offset := 0
		for _, section := range sections {
			input.Contributions = append(input.Contributions, promptservice.Contribution{Key: section.Key, Content: text[offset : offset+section.Bytes], Static: section.Static})
			offset += section.Bytes + 2
		}
	}
	var output promptservice.Result
	var err error
	if host == nil {
		// Explicit embedded runtimes assemble the native provider directly.
		output, err = promptservice.Assemble(input)
	} else {
		output, err = promptservice.NewClient(boundServiceCall(host, sessionExecutionConsumer, promptservice.ServiceName)).Assemble(ctx, input)
	}
	if err != nil {
		return prompt.BuildResult{}, err
	}
	result := prompt.BuildResult{Content: output.Content, Sections: make([]prompt.SectionInfo, 0, len(output.Sections))}
	for _, section := range output.Sections {
		result.Sections = append(result.Sections, prompt.SectionInfo{Key: section.Key, Static: section.Static, Bytes: section.Bytes, Hash: section.Hash})
	}
	return result, nil
}
