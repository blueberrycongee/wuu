package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/blueberrycongee/wuu/internal/plugincontrol"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

func runPluginHosts(action string, args []string) error {
	fs, _ := pluginFlagSet("plugin " + action)
	if err := fs.Parse(args); err != nil {
		return pluginCLIError(err)
	}
	if fs.NArg() > 1 || (action == "reload" && fs.NArg() != 1) {
		return pluginCLIError(errors.New("plugin hosts accepts an optional plugin ID; reload requires one"))
	}
	home, err := statepath.Home("")
	if err != nil {
		return pluginCLIError(err)
	}
	hosts, err := plugincontrol.List(home)
	if err != nil {
		return pluginCLIError(err)
	}
	reports := make([]plugincontrol.Response, 0, len(hosts))
	var failures error
	reloaded := 0
	for _, host := range hosts {
		report, err := plugincontrol.Call(context.Background(), host, plugincontrol.Request{Operation: "status", PluginID: fs.Arg(0)})
		if err == nil && action == "reload" && len(report.Plugins) != 0 && report.Plugins[0].State != "inactive" {
			reloaded++
			report, err = plugincontrol.Call(context.Background(), host, plugincontrol.Request{Operation: "reload", PluginID: fs.Arg(0)})
			if err == nil {
				err = report.ActivationError(fs.Arg(0), "")
			}
		}
		if err != nil {
			report.Host = host.Host
			report.Error = err.Error()
			failures = errors.Join(failures, fmt.Errorf("host %s: %w", host.Host.ID, err))
		}
		reports = append(reports, report)
	}
	if err := json.NewEncoder(os.Stdout).Encode(reports); err != nil {
		return err
	}
	if action == "reload" && reloaded == 0 {
		return pluginCLIError(errors.Join(failures, errors.New("no compatible live host selects this plugin; no runtime was reloaded")))
	}
	return failures
}
