package host

import (
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/yazawa/isotty/internal/isotty/opeco"
)

func runOpeco(projectPath, homeDir string, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("host opeco requires a subcommand: enable, disable, or show")
	}
	fs := flag.NewFlagSet("host opeco "+args[0], flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("host opeco %s does not accept arguments", args[0])
	}
	settings, err := opeco.LoadSettings(homeDir, projectPath)
	if err != nil {
		return err
	}
	switch args[0] {
	case "enable":
		// The project's file cannot overrule the host user's, so removing
		// it here would report an opeco that still does not come.
		if !settings.User.On() {
			return fmt.Errorf("opeco is disabled for every project of this host user in %s; remove `enabled: false` there first", opeco.UserConfigPath(homeDir))
		}
		if err := opeco.EnableProject(projectPath); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "Enabled opeco for this project. Run `isotty up` to install it in the VM.")
		return nil
	case "disable":
		if err := opeco.DisableProject(projectPath); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "Disabled opeco for this project. Run `isotty up` to remove it from the VM.")
		return nil
	case "show":
		if settings.Enabled() {
			fmt.Fprintln(stdout, "opeco is enabled for this project.")
			return nil
		}
		if !settings.User.On() {
			fmt.Fprintf(stdout, "opeco is disabled for every project of this host user: %s\n", opeco.UserConfigPath(homeDir))
		}
		if !settings.Project.On() {
			fmt.Fprintf(stdout, "opeco is disabled for this project: %s\n", opeco.ProjectConfigPath(projectPath))
		}
		return nil
	default:
		return fmt.Errorf("unknown host opeco subcommand %q", args[0])
	}
}
