package isotty

import (
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/yazawa/isotty/internal/isotty/host/wake"
	runtimecfg "github.com/yazawa/isotty/internal/isotty/runtime"
)

func (a *App) runSchedule(args []string) error {
	if len(args) == 0 {
		return errors.New("schedule requires a subcommand: enable, disable or show")
	}
	fs := flag.NewFlagSet("schedule "+args[0], flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("schedule %s does not accept arguments", args[0])
	}
	homeDir, err := isottyHome()
	if err != nil {
		return err
	}
	switch args[0] {
	case "show":
		return a.showSchedule(homeDir)
	}
	projectPath, err := filepath.Abs(".")
	if err != nil {
		return err
	}
	projectHash := hashProjectPath(projectPath)
	switch args[0] {
	case "enable":
		if err := wake.Enable(homeDir, projectHash, projectPath); err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "The agents of %s may now have their VM started by a wake; the isotty daemon serves it.\n", projectPath)
		return nil
	case "disable":
		if err := wake.Disable(homeDir, projectHash); err != nil {
			return err
		}
		fmt.Fprintf(a.stdout, "The agents of %s no longer have their VM started by a wake; its wakes are forgotten.\n", projectPath)
		return nil
	}
	return fmt.Errorf("unknown schedule subcommand %q", args[0])
}

func (a *App) showSchedule(homeDir string) error {
	consents, err := wake.Consents(homeDir)
	if err != nil {
		return err
	}
	if len(consents) == 0 {
		fmt.Fprintln(a.stdout, "No project takes wakes.")
	}
	for _, consent := range consents {
		fmt.Fprintln(a.stdout, consent.ProjectPath)
		requests, err := wake.Load(homeDir, consent.ProjectHash)
		if err != nil {
			return err
		}
		for _, r := range requests {
			fmt.Fprintf(a.stdout, "  %s  %s  %s %s  %s\n", r.ID, r.At.Local().Format(time.RFC3339), r.Agent, r.Session, r.Prompt)
		}
	}
	return nil
}

// wakeRetry is how long a project whose wake failed waits before the next
// try, so that a VM that cannot start is not started every poll.
const wakeRetry = 10 * time.Minute

// wakeBusy is isotty-wake deliver's exit status for a conversation that
// still has work.
const wakeBusy = 75

// wakeRecordError is a wake that was delivered but could not be recorded:
// the human is not to be told it failed.
type wakeRecordError struct{ err error }

func (e wakeRecordError) Error() string {
	return "the wake was delivered but not recorded: " + e.err.Error()
}

// deliverWake gives the prompt to the conversation in the VM and records
// what it did. A conversation that still has work, or waits for an
// approval, is not typed into: a recurring wake skips that time and a
// one-time one waits a while. A delivered wake is done: a one-time one
// forgotten, a recurring one moved to its next time.
func (a *App) deliverWake(target runtimecfg.RemoteTarget, projectHash, homeDir string, request wake.Request) error {
	command := strings.Join([]string{wake.ClientPath, "deliver", runtimecfg.ShellQuote(request.Agent), runtimecfg.ShellQuote(request.Session),
		base64.StdEncoding.EncodeToString([]byte(request.Prompt))}, " ")
	out, err := target.Capture(command)
	busy := err != nil && target.ExitCode(err) == wakeBusy
	result := strings.TrimSpace(out)
	switch {
	case busy && request.Cron != "":
		result = "skipped: the conversation still has work"
	case busy:
		result = fmt.Sprintf("postponed %d minutes: the conversation still has work", int(wakeRetry.Minutes()))
	case err != nil:
		result = err.Error()
	}
	if err != nil && !busy {
		// What is told of the failure is the caller's.
		event, eventErr := runtimecfg.NewWakeVMEvent(projectHash, "wake", request.Agent, request.Session, request.At, request.Prompt, result)
		if eventErr == nil {
			eventErr = runtimecfg.RecordVMEvent(target, event, a.debug)
		}
		return errors.Join(fmt.Errorf("deliver wake %s: %w", request.ID, err), eventErr)
	}
	var moved error
	if busy && request.Cron == "" {
		moved = wake.Postpone(homeDir, projectHash, request.ID, time.Now().Add(wakeRetry))
	} else {
		moved = wake.Done(homeDir, projectHash, request, time.Now())
	}
	event, eventErr := runtimecfg.NewWakeVMEvent(projectHash, "wake", request.Agent, request.Session, request.At, request.Prompt, result)
	if eventErr == nil {
		eventErr = runtimecfg.RecordVMEvent(target, event, a.debug)
	}
	if eventErr != nil {
		return errors.Join(moved, wakeRecordError{eventErr})
	}
	return moved
}
