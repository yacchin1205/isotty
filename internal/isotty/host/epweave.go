package host

import (
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/yazawa/isotty/internal/isotty/host/epweave"
)

func runEpweave(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("host epweave requires a subcommand: enable, disable, or show")
	}

	switch args[0] {
	case "enable":
		return runEpweaveEnable(projectPath, projectHash, homeDir, args[1:], stdout, stderr)
	case "disable":
		return runEpweaveDisable(projectPath, projectHash, homeDir, args[1:], stdout, stderr)
	case "show":
		return runEpweaveShow(projectPath, projectHash, homeDir, args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown host epweave subcommand %q", args[0])
	}
}

func runEpweaveEnable(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("host epweave enable", flag.ContinueOnError)
	fs.SetOutput(stderr)
	prefix := fs.String("prefix", "", "title prefix the VM may write under (titles become <prefix>/<topic>)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("host epweave enable does not accept arguments")
	}
	if *prefix == "" {
		return errors.New("host epweave enable requires --prefix")
	}

	cfg := epweave.ProjectConfig{Prefix: *prefix}
	if err := epweave.SaveProjectConfig(projectPath, cfg); err != nil {
		return err
	}
	if err := epweave.SavePin(homeDir, projectHash, cfg); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Enabled ep_weave: titles under %s are writable from the VM\n", cfg.TitlePrefix())
	fmt.Fprintf(stdout, "Run `isotty up` to install the client in the VM.\n")
	return nil
}

func runEpweaveDisable(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("host epweave disable", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("host epweave disable does not accept arguments")
	}
	if err := epweave.RemoveProjectConfig(projectPath); err != nil {
		return err
	}
	if err := epweave.RemovePin(homeDir, projectHash); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "Disabled ep_weave for this project.")
	return nil
}

func runEpweaveShow(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("host epweave show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("host epweave show does not accept arguments")
	}
	cfg, err := LoadEpweaveProjectConfig(projectPath, projectHash, homeDir)
	if err != nil {
		return err
	}
	if cfg == nil {
		fmt.Fprintln(stdout, "ep_weave is not enabled for this project.")
		return nil
	}
	hostCfg, err := epweave.LoadHostConfig(homeDir)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "prefix: %s\n", cfg.Prefix)
	fmt.Fprintf(stdout, "pinned: %s\n", epweave.PinPath(homeDir, projectHash))
	fmt.Fprintf(stdout, "url: %s\n", hostCfg.URL)
	fmt.Fprintf(stdout, "client_id: %s\n", hostCfg.ClientID)
	return nil
}
