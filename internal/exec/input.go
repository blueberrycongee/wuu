package exec

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

type PromptInput struct {
	Args        []string
	Stdin       io.Reader
	StdinIsPipe bool
	AllowEmpty  bool
}

func ResolvePrompt(input PromptInput) (string, error) {
	args := append([]string(nil), input.Args...)
	for i := range args {
		args[i] = strings.TrimSpace(args[i])
	}

	forceStdin := len(args) == 1 && args[0] == "-"
	if forceStdin {
		text, err := readPromptStdin(input.Stdin)
		if err != nil {
			return "", err
		}
		if text == "" {
			return "", errors.New("prompt is empty")
		}
		return text, nil
	}

	prompt := strings.TrimSpace(strings.Join(args, " "))
	if prompt == "-" {
		return "", errors.New("use `-` by itself to read the prompt from stdin")
	}

	if input.StdinIsPipe {
		stdinText, err := readPromptStdin(input.Stdin)
		if err != nil {
			return "", err
		}
		if prompt != "" && stdinText != "" {
			return prompt + "\n\n<stdin>\n" + stdinText + "\n</stdin>", nil
		}
		if prompt == "" {
			if stdinText == "" {
				if input.AllowEmpty {
					return "", nil
				}
				return "", errors.New("prompt is empty")
			}
			return stdinText, nil
		}
	}

	if prompt == "" {
		if input.AllowEmpty {
			return "", nil
		}
		return "", errors.New("prompt is required (pass text or pipe stdin)")
	}
	return prompt, nil
}

func readPromptStdin(r io.Reader) (string, error) {
	if r == nil {
		return "", errors.New("stdin is not available")
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("read stdin: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}
