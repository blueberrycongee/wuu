package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	wuuexec "github.com/blueberrycongee/wuu/internal/exec"
	"github.com/blueberrycongee/wuu/internal/session"
)

func runSessionUsage(args []string) error {
	fs := flag.NewFlagSet("session usage", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOutput := fs.Bool("json", false, "output as JSON")
	threadFlag := fs.String("thread", "", "thread id")
	if err := fs.Parse(args); err != nil {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, err)
	}
	id := strings.TrimSpace(*threadFlag)
	if id == "" && len(fs.Args()) > 0 {
		id = strings.TrimSpace(fs.Args()[0])
	}
	if id == "" {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, errors.New("session usage requires a thread id"))
	}
	sessDir, err := resolveSessionsDir()
	if err != nil {
		return err
	}
	report, err := session.ReadSessionUsage(sessDir, id)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return printJSON(report)
	}
	fmt.Printf("thread_id: %s\nsessions: %d\n", report.ThreadID, len(report.Sessions))
	printUsage := func(label string, usage session.UsageTotals) {
		fmt.Printf("%s: input=%d output=%d cache_write=%d cache_read=%d records=%d\n",
			label, usage.InputTokens, usage.OutputTokens, usage.CacheCreationTokens, usage.CacheReadTokens, usage.Records)
	}
	printUsage("self", report.Self)
	printUsage("delegated", report.Delegated)
	printUsage("tree", report.Tree)
	for _, model := range report.ModelBreakdowns {
		label := firstNonEmptyString(model.Provider, "(unknown)") + "/" + firstNonEmptyString(model.Model, "(unknown)")
		printUsage(label, model.UsageTotals)
	}
	fmt.Println("coverage: recorded turn usage only; not a complete provider billing total")
	if len(report.Coverage.SessionsWithoutRecords) > 0 {
		fmt.Printf("sessions without usage records: %s\n", strings.Join(report.Coverage.SessionsWithoutRecords, ", "))
	}
	if report.Coverage.UnattributedRecords > 0 {
		fmt.Printf("records without provider/model attribution: %d\n", report.Coverage.UnattributedRecords)
	}
	return nil
}
