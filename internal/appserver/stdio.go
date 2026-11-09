package appserver

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Desktop attachments are embedded as base64 in one JSONL request. A single
// supported 20 MB PDF is about 27 MB after base64 encoding, and image batches
// can be larger still. Keep the local transport comfortably above those
// payloads; provider-specific limits are enforced after the request arrives.
const appServerMaxStdioRequestBytes = 64 * 1024 * 1024

func runStdioScanner(ctx context.Context, s *Server, in io.Reader) error {
	ctx = context.WithValue(ctx, stdioResumeDispatchKey{}, true)
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), appServerMaxStdioRequestBytes)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		readAt := s.rpcTiming.now()
		if err := s.handleLine(ctx, []byte(line), readAt); err != nil {
			if errors.Is(err, errShutdown) {
				return nil
			}
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read app-server input: %w", err)
	}
	return nil
}
