package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

func runPluginExperimentEval(args []string) error {
	fs, jsonOutput := pluginFlagSet("plugin experiment eval")
	concurrency := fs.Int("concurrency", pluginpkg.DefaultExperimentConcurrency, "maximum simultaneous evaluator commands")
	timeout := fs.Duration("timeout", pluginpkg.DefaultExperimentTimeout, "per-command timeout (maximum 30m)")
	workspace := fs.String("workspace", "", "restrict candidates to their captured workspace")
	workdir := fs.String("workdir", "", "explicit writable command directory; default is a fresh scratch directory per run")
	outputLimit := fs.Int64("output-limit", pluginpkg.DefaultExperimentOutputBytes, "maximum saved bytes per stdout/stderr stream")
	if err := fs.Parse(args); err != nil {
		return pluginCLIError(err)
	}
	positional := fs.Args()
	separator := -1
	for index, arg := range positional {
		if arg == "--" {
			separator = index
			break
		}
	}
	if separator < 0 || separator == len(positional)-1 {
		return pluginCLIError(errors.New("plugin experiment eval requires PLUGIN_ID CANDIDATE_ID... -- COMMAND [ARG...]; argv and output are recorded, so do not include secrets"))
	}
	if separator < 2 {
		return pluginCLIError(errors.New("plugin experiment eval requires a plugin id and at least one candidate id"))
	}
	if *timeout <= 0 || *concurrency < 1 || *outputLimit < 1 {
		return pluginCLIError(errors.New("timeout, concurrency, and output-limit must be positive"))
	}
	home, err := statepath.Home("")
	if err != nil {
		return fmt.Errorf("resolve Wuu home: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	report, err := pluginpkg.EvaluateExperiments(ctx, home, pluginpkg.ExperimentEvaluationRequest{
		ID: positional[0], CandidateIDs: positional[1:separator], Command: positional[separator+1:],
		Workspace: *workspace, Workdir: *workdir, Concurrency: *concurrency,
		Timeout: *timeout, MaxOutputBytes: *outputLimit,
	})
	if err != nil {
		return pluginCLIError(err)
	}
	if *jsonOutput {
		return printPluginJSON(report)
	}
	fmt.Printf("Evaluation report: %s\n", report.ReportPath)
	for _, run := range report.Runs {
		fmt.Printf("%s %s: %s (%dms)\n", run.RunID, run.CandidateID, run.Status, run.DurationMS)
	}
	fmt.Println(report.Isolation)
	fmt.Println(report.Interpretation)
	return nil
}
