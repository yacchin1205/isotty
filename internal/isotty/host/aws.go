package host

import (
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/yazawa/isotty/internal/isotty/host/aws"
	"github.com/yazawa/isotty/internal/isotty/host/pin"
)

// LoadAWSProjectConfig returns the synced project config once it has been
// checked against the pin this host recorded at `isotty host aws enable`,
// or nil when the project has not enabled AWS.
func LoadAWSProjectConfig(projectPath, projectHash, homeDir string) (*aws.ProjectConfig, error) {
	synced, enabled, err := aws.LoadProjectConfig(projectPath)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, nil
	}
	cfg, err := aws.VerifyPinned(homeDir, projectHash, synced)
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}

func runAWS(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("host aws requires a subcommand: enable, disable, or show")
	}
	switch args[0] {
	case "enable":
		return runAWSEnable(projectPath, projectHash, homeDir, args[1:], stdout, stderr)
	case "disable":
		return runAWSDisable(projectPath, projectHash, homeDir, args[1:], stdout, stderr)
	case "show":
		return runAWSShow(projectPath, projectHash, homeDir, args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown host aws subcommand %q", args[0])
	}
}

// checkAWS assumes the role as the agreement asks, so that a role the host
// cannot use is found at enable and not at attach.
var checkAWS = func(cfg aws.ProjectConfig, host aws.HostConfig, projectHash string) error {
	return aws.NewIssuer(cfg, host, projectHash).Check()
}

// runAWSEnable writes the whole agreement; nothing is pinned until the role
// has been assumed.
func runAWSEnable(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("host aws enable", flag.ContinueOnError)
	fs.SetOutput(stderr)
	roleARN := fs.String("role-arn", "", "the IAM role the VM acts as")
	profile := fs.String("profile", "", "the host's AWS profile that assumes the role; the aws CLI's default when omitted")
	fs.Usage = func() {
		fmt.Fprint(stderr, `Usage: isotty host aws enable --role-arn <arn> [--profile <name>]

Lets the VM use AWS as the role, through one-hour credentials the host
assumes with its aws CLI while attached. Running it again replaces the
role and the profile.

`+awsGuide)
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 || *roleARN == "" {
		return errors.New("host aws enable requires --role-arn <arn> and no arguments")
	}
	cfg := aws.ProjectConfig{RoleARN: *roleARN}
	host := aws.HostConfig{Profile: *profile}
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := host.Validate(); err != nil {
		return err
	}
	if err := checkAWS(cfg, host, projectHash); err != nil {
		fmt.Fprint(stderr, awsGuide)
		return fmt.Errorf("%s could not be assumed, so nothing was pinned: %w", cfg.RoleARN, err)
	}
	if err := aws.Save(projectPath, projectHash, homeDir, cfg, host); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Enabled AWS for this project as %s in account %s\n", cfg.RoleARN, cfg.Account())
	fmt.Fprintf(stdout, "profile: %s\n", describeProfile(host))
	fmt.Fprintln(stdout, "Run `isotty up` to install the client in the VM.")
	return nil
}

const awsGuide = `Make the role ready, in the account the agent should work in:

  1. Find the identity your aws CLI uses on this host:
       aws sts get-caller-identity [--profile <name>]
     For an assumed role, SSO among them, the principal below is the role,
     arn:aws:iam::<account>:role/<path><name>, not the session.
  2. Create the role, or pick one, with a trust policy that lets that
     identity assume it:
       {
         "Version": "2012-10-17",
         "Statement": [{
           "Effect": "Allow",
           "Principal": {"AWS": "<the identity's ARN>"},
           "Action": ["sts:AssumeRole"]
         }]
       }
     and a maximum session duration of at least one hour, the default.
  3. What the agent may do is the policies you attach to the role; attach
     those as you would for anyone.

`

func describeProfile(host aws.HostConfig) string {
	if host.Profile == "" {
		return "(the aws CLI's default in the shell that runs `isotty attach`)"
	}
	return host.Profile
}

func runAWSDisable(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("host aws disable", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("host aws disable does not accept arguments")
	}
	if err := aws.Remove(projectPath, projectHash, homeDir); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "Disabled AWS for this project. The role itself is left as it is in AWS.")
	return nil
}

func runAWSShow(projectPath, projectHash, homeDir string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("host aws show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("host aws show does not accept arguments")
	}
	cfg, err := LoadAWSProjectConfig(projectPath, projectHash, homeDir)
	if err != nil {
		return err
	}
	if cfg == nil {
		fmt.Fprintln(stdout, "AWS is not enabled for this project.")
		return nil
	}
	host, err := aws.LoadHostConfig(homeDir, projectHash)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "role_arn: %s\n", cfg.RoleARN)
	fmt.Fprintf(stdout, "account: %s\n", cfg.Account())
	fmt.Fprintf(stdout, "profile: %s\n", describeProfile(host))
	fmt.Fprintf(stdout, "session: %s\n", aws.SessionName(projectHash))
	fmt.Fprintf(stdout, "pinned: %s\n", pin.Path(homeDir, projectHash, aws.PinKind))
	return nil
}
