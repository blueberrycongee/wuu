package process

import (
	"errors"
	"fmt"
	"os"
)

func (p *Process) outputLog(stream string) (string, int64, error) {
	switch stream {
	case "":
		return p.LogPath, p.LogBaseOffset, nil
	case "stdout", "stderr":
		if !p.SeparateStreams {
			return "", 0, errors.New("separate output streams are unavailable for this process")
		}
		base := p.StdoutBaseOffset
		if stream == "stderr" {
			base = p.StderrBaseOffset
		}
		return p.LogPath + "." + stream + ".log", base, nil
	default:
		return "", 0, fmt.Errorf("unknown output stream %q", stream)
	}
}

func compactProcessStreams(p *Process) error {
	if !p.SeparateStreams {
		return nil
	}
	var result error
	for _, stream := range []string{"stdout", "stderr"} {
		path, _, _ := p.outputLog(stream)
		_, discarded, err := compactProcessLog(path, terminalProcessLogMaxBytes)
		if err != nil && !os.IsNotExist(err) {
			result = errors.Join(result, fmt.Errorf("compact %s: %w", stream, err))
		}
		if stream == "stdout" {
			p.StdoutBaseOffset += discarded
		} else {
			p.StderrBaseOffset += discarded
		}
	}
	return result
}
