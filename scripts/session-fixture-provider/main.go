// This executable is a synthetic E2E fixture, not a production provider.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

const providerID = "fixture.session-documents"
const fixtureDir = "session-fixture-provider"
const markerName = ".wuu-synthetic-fixture"
const markerContent = "wuu-session-fixture-v1\n"

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(args []string, input io.Reader, output io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: session-fixture-provider install|serve --home <synthetic-home>")
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	home := flags.String("home", "", "synthetic fixture home")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	switch args[0] {
	case "install":
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		result, err := install(*home, executable, nil)
		if err != nil {
			return err
		}
		return json.NewEncoder(output).Encode(result)
	case "serve":
		return serve(*home, input, output)
	default:
		return fmt.Errorf("unknown fixture command %q", args[0])
	}
}
func serve(home string, input io.Reader, output io.Writer) error {
	home, err := syntheticHome(home)
	if err != nil {
		return err
	}
	dir, err := fixturePath(home, fixtureDir+"/store")
	if err != nil {
		return err
	}
	provider := persistence.NewService(providerID, dir, "sqlite")
	defer provider.Close(context.Background())
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 64<<20)
	encoder := json.NewEncoder(output)
	for scanner.Scan() {
		var request struct {
			ID     string          `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			return err
		}
		if request.Method == "execution.cancel" {
			continue
		}
		var result any
		var callErr error
		switch request.Method {
		case "initialize":
			var params struct {
				WuuHome  string `json:"wuu_home"`
				PluginID string `json:"plugin_id"`
			}
			if callErr = json.Unmarshal(request.Params, &params); callErr == nil {
				var bound string
				bound, callErr = syntheticHome(params.WuuHome)
				if callErr == nil && (bound != home || params.PluginID != providerID) {
					callErr = errors.New("fixture provider must use its installed synthetic home and identity")
				}
				result = map[string]any{"protocol_version": 3, "lifecycle_version": 2, "provided_services": provider.ProvidedServices()}
			}
		case "prepare":
			callErr = provider.Prepare(context.Background())
		case "activate":
			callErr = provider.Activate(context.Background())
		case "service.invoke":
			var params pluginhost.ServiceInvokeParams
			if callErr = json.Unmarshal(request.Params, &params); callErr == nil {
				if params.Service != persistence.ServiceName {
					callErr = &pluginhost.HostServiceError{Code: "service_not_found", Message: "fixture only provides state.documents"}
				} else {
					result, callErr = invoke(context.Background(), home, provider, params)
				}
			}
		case "service.changed":
		case "shutdown":
			callErr = provider.Close(context.Background())
		default:
			callErr = &pluginhost.HostServiceError{Code: "method_not_found", Message: "unsupported fixture method"}
		}
		response := map[string]any{"id": request.ID, "result": result}
		if callErr != nil {
			failure := &pluginhost.HostServiceError{Code: "fixture_error", Message: callErr.Error()}
			var serviceErr *pluginhost.HostServiceError
			if errors.As(callErr, &serviceErr) {
				failure = serviceErr
			}
			delete(response, "result")
			response["error"] = failure
		}
		if err := encoder.Encode(response); err != nil {
			return err
		}
		if request.Method == "shutdown" {
			return nil
		}
	}
	return scanner.Err()
}
