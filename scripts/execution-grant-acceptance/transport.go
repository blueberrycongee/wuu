// Transport-only acceptance bootstrap. Providers never import this or any Wuu
// internal package. Every connection uses the production signed-provider ingress;
// there is no trusted-local Serve call, principal factory or authorization bypass.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/blueberrycongee/wuu/internal/sdk"
)

type output struct{ mu sync.Mutex }

func (o *output) emit(value any) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return json.NewEncoder(os.Stdout).Encode(value)
}

type writer struct {
	out    *output
	client string
}

func (w writer) Write(data []byte) (int, error) {
	err := w.out.emit(map[string]any{"client": w.client, "frame": json.RawMessage(data)})
	return len(data), err
}

type connection struct {
	input *io.PipeWriter
	done  chan struct{}
}

func run() error {
	cwd := flag.String("workdir", "", "isolated fixture workspace")
	config := flag.String("config", "", "isolated fixture config")
	audience := flag.String("audience", "", "configured synthetic token audience")
	flag.Parse()
	runtime, err := sdk.New(sdk.Options{WorkDir: *cwd, HomeDir: os.Getenv("HOME"), ConfigPath: *config, UserSpaceID: "local", ProductDomain: "personal"})
	if err != nil {
		return err
	}
	defer runtime.Close()
	out := &output{}
	clients := map[string]connection{}
	defer func() {
		for _, client := range clients {
			client.input.Close()
		}
		for _, client := range clients {
			<-client.done
		}
	}()
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 65536), 32*1024*1024)
	for scanner.Scan() {
		var frame struct {
			Client string
			Action string
			Frame  json.RawMessage
		}
		if err := json.Unmarshal(scanner.Bytes(), &frame); err != nil {
			return err
		}
		switch frame.Action {
		case "open":
			if _, found := clients[frame.Client]; found {
				return fmt.Errorf("duplicate client")
			}
			reader, input := io.Pipe()
			client := connection{input, make(chan struct{})}
			clients[frame.Client] = client
			go func(id string, client connection) {
				defer close(client.done)
				defer reader.Close()
				if err := runtime.ServeProviderAuthenticated(context.Background(), reader, writer{out, id}, *audience); err != nil {
					fmt.Fprintln(os.Stderr, "fixture connection:", err)
				}
			}(frame.Client, client)
		case "close":
			client, found := clients[frame.Client]
			if !found {
				return fmt.Errorf("unknown client")
			}
			client.input.Close()
			<-client.done
			delete(clients, frame.Client)
			if err := out.emit(map[string]any{"client": frame.Client, "action": "closed"}); err != nil {
				return err
			}
		default:
			client, found := clients[frame.Client]
			if !found {
				return fmt.Errorf("unknown client")
			}
			if _, err := client.input.Write(append(frame.Frame, '\n')); err != nil {
				return err
			}
		}
	}
	return scanner.Err()
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
