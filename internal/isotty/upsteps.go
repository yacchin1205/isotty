package isotty

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/yazawa/isotty/internal/isotty/host/bitbucket"
	"github.com/yazawa/isotty/internal/isotty/host/browser"
	"github.com/yazawa/isotty/internal/isotty/host/child"
	"github.com/yazawa/isotty/internal/isotty/host/dns"
	"github.com/yazawa/isotty/internal/isotty/host/docker"
	"github.com/yazawa/isotty/internal/isotty/host/epweave"
	"github.com/yazawa/isotty/internal/isotty/host/gas"
	"github.com/yazawa/isotty/internal/isotty/host/github"
	"github.com/yazawa/isotty/internal/isotty/host/gitlab"
	"github.com/yazawa/isotty/internal/isotty/host/google"
	"github.com/yazawa/isotty/internal/isotty/host/redmine"
	"github.com/yazawa/isotty/internal/isotty/opeco"
	runtimecfg "github.com/yazawa/isotty/internal/isotty/runtime"
	vmcfg "github.com/yazawa/isotty/internal/isotty/vm"
)

// The steps of up. spec/ holds their specification in Lean: each step's
// effects (base, each, cond, rerun below) are generated into
// spec/Spec/Steps.lean, where Lean checks that no step undoes or fools
// another, in this order, for every configuration. What a step's check and
// command do is taken to be what its effects say.

// upWait bounds the wait for what an earlier, interrupted up left running.
const upWait = 10 * time.Minute

// upEnv is what the steps of one up share.
type upEnv struct {
	a           *App
	cfg         Config
	state       *State
	conn        vmcfg.GCPConnection
	status      string
	ignoreLeft  bool
	homeArchive []byte
	hasHome     bool
	archives    []string
	// auths are the saved homes of the agents, with the paths they hold.
	auths       []savedAuth
	postInstall bool
}

type savedAuth struct {
	agent   string
	archive []byte
	paths   []string
}

// cond is when a step runs, in Go and in Lean.
type cond struct {
	lean  string
	holds func(e *upEnv) bool
}

var always = cond{"", func(*upEnv) bool { return true }}

type stepKind int

const (
	// A step of spec/Spec/Model.lean's `step`.
	kindStep stepKind = iota
	// A step of `perName`: one thing per name the configuration lists.
	kindPerName
	// A step written by hand in spec/Spec/Model.lean, named by special.
	kindSpecial
	// Not a step of the specification: it reads, waits or fails, and makes
	// nothing.
	kindPrepare
)

type upStep struct {
	label string
	// tag names the step's processes in the VM; one word.
	tag  string
	kind stepKind
	// The step's effects, as Lean terms: base and, for each agent `a`, each.
	base, each []string
	// perName: the kind, the constructor and the configuration's field.
	perName [3]string
	special string
	cond    cond
	// rerun: run on every up. Otherwise the step runs unless inPlace says
	// that what it makes is there.
	rerun   bool
	inPlace func(e *upEnv) (bool, error)
	run     func(e *upEnv) error
	// ownPhases: run shows its own progress.
	ownPhases bool
}

// inVM observes a step in the VM with a check that holds when it is done.
func inVM(check func(e *upEnv) (string, error)) func(e *upEnv) (bool, error) {
	return func(e *upEnv) (bool, error) {
		command, err := check(e)
		if err != nil {
			return false, err
		}
		return e.holds(command)
	}
}

func (e *upEnv) vm(tag, command string) error {
	return vmcfg.RunGCPRemoteCommand(e.conn, runtimecfg.TagUpStep(tag, command), e.a.debug)
}

func (e *upEnv) vmInput(tag, command string, input []byte) error {
	return vmcfg.RunGCPRemoteCommandWithInput(e.conn, runtimecfg.TagUpStep(tag, command), bytes.NewReader(input))
}

// holds tells whether a check holds in the VM.
func (e *upEnv) holds(check string) (bool, error) {
	out, err := vmcfg.CaptureGCPRemoteCommand(e.conn, fmt.Sprintf("if %s; then echo yes; else echo no; fi", check))
	if err != nil {
		return false, err
	}
	switch strings.TrimSpace(out) {
	case "yes":
		return true, nil
	case "no":
		return false, nil
	}
	return false, fmt.Errorf("unexpected answer to a check in the VM: %q", out)
}

func agentsCond() cond {
	return cond{"(!·.agents.isEmpty)", func(e *upEnv) bool { return len(e.cfg.Runtime.Agents) > 0 }}
}

// hostClientStep installs the client of a host relay the project pins.
func hostClientStep(name, label string, pinned func(Config) bool, extra []string, each []string,
	command func(cfg Config) string) upStep {
	return upStep{
		label: label, tag: name, kind: kindStep,
		base:  append([]string{".client ." + name, ".keepalive"}, extra...),
		each:  each,
		cond:  cond{fmt.Sprintf("(fun cfg => decide (.%s ∈ cfg.clients))", name), func(e *upEnv) bool { return pinned(e.cfg) }},
		rerun: true,
		run:   func(e *upEnv) error { return e.vm(name, command(e.cfg)) },
	}
}

var mergeScriptRes = []string{".mergeScript", `.pkg "python3-tomli-w"`}

func upSteps() []upStep {
	agents := func(e *upEnv) []string { return e.cfg.Runtime.Agents }
	return []upStep{
		{label: "Making the VM run", tag: "vm", kind: kindStep, base: []string{".vm", ".hostState"}, cond: always,
			inPlace:   func(e *upEnv) (bool, error) { return e.status == vmcfg.GCPStatusRunning, nil },
			run:       func(e *upEnv) error { return e.a.makeVMRun(e) },
			ownPhases: true},
		{label: "Waiting for SSH", kind: kindPrepare, run: func(e *upEnv) error { return e.a.waitForVM(e) }},
		{label: "Saving local state", tag: "state", kind: kindStep, base: []string{".hostState"}, cond: always, rerun: true,
			run: func(e *upEnv) error { return SaveState(*e.state) }},
		{label: "Waiting for what an earlier up left running", kind: kindPrepare,
			run: func(e *upEnv) error { return e.a.waitForEarlierUp(e) }},
		{label: "Preparing the workspace", tag: "workspace", kind: kindStep, base: []string{".workspace"}, cond: always,
			inPlace: inVM(func(e *upEnv) (string, error) { return runtimecfg.WorkspaceCheck(e.state.RemoteWorkspacePath), nil }),
			run: func(e *upEnv) error {
				return e.vm("workspace", runtimecfg.WorkspaceCommand(e.state.RemoteWorkspacePath))
			}},
		{label: "Installing packages", tag: "apt", kind: kindPerName, perName: [3]string{".pkg", ".pkg", "pkgs"}, cond: always,
			inPlace: inVM(func(e *upEnv) (string, error) {
				return orTrue(runtimecfg.AptInstallPackages(e.cfg.Runtime), runtimecfg.AptCheck), nil
			}),
			run: func(e *upEnv) error {
				return e.vm("apt", runtimecfg.AptCommand(runtimecfg.AptInstallPackages(e.cfg.Runtime)))
			}},
		{label: "Installing Node.js", tag: "node", kind: kindStep, base: []string{".nodeRepo", `.pkg "nodejs"`},
			cond:    cond{"(·.node)", func(e *upEnv) bool { return runtimecfg.NeedsNodeRuntime(e.cfg.Runtime) }},
			inPlace: inVM(func(e *upEnv) (string, error) { return runtimecfg.NodeCheck(e.cfg.Runtime), nil }),
			run:     func(e *upEnv) error { return e.vm("node", runtimecfg.NodeBootstrapCommand(e.cfg.Runtime)) }},
		{label: "Installing agents", tag: "agents", kind: kindStep, each: []string{".agentCli a"}, cond: agentsCond(),
			inPlace: inVM(func(e *upEnv) (string, error) { return runtimecfg.AgentCheck(e.cfg.Runtime) }),
			run: func(e *upEnv) error {
				command, err := runtimecfg.AgentBootstrapCommand(e.cfg.Runtime)
				if err != nil {
					return err
				}
				return e.vm("agents", command)
			}},
		{label: "Installing tools", tag: "tools", kind: kindPerName, perName: [3]string{".tool", ".tool", "tools"}, cond: always,
			inPlace: inVM(func(e *upEnv) (string, error) { return runtimecfg.ToolCheck(e.cfg.Runtime) }),
			run: func(e *upEnv) error {
				command, err := runtimecfg.ToolBootstrapCommand(e.cfg.Runtime)
				if err != nil {
					return err
				}
				return e.vm("tools", command)
			}},
		{label: "Installing services", tag: "services", kind: kindPerName, perName: [3]string{".service", ".service", "services"}, cond: always,
			inPlace: inVM(func(e *upEnv) (string, error) { return runtimecfg.ServiceCheck(e.cfg.Runtime) }),
			run: func(e *upEnv) error {
				command, err := runtimecfg.ServiceBootstrapCommand(e.cfg.Runtime)
				if err != nil {
					return err
				}
				return e.vm("services", command)
			}},
		{label: "Restoring saved agent homes", kind: kindSpecial, special: "restoreT", run: func(e *upEnv) error { return e.a.restoreAgentHomes(e) }},
		// After the restore: copied before it, ~/.isotty/home would create
		// ~/.claude and the restore would take the saved home as there.
		{label: "Copying the host's home files", kind: kindSpecial, special: "homeT", run: func(e *upEnv) error {
			if !e.hasHome {
				return nil
			}
			return e.a.runPhase("Copying %s into the VM home", func() error {
				return e.vmInput("home", runtimecfg.UnpackHomeCommand(), e.homeArchive)
			}, runtimecfg.HomeFilesDir(e.cfg.HomeDir))
		}},
		{label: "Restoring audit archives", tag: "audit-archives", kind: kindStep, base: []string{".auditArchives"}, each: []string{".skill a .audit"},
			cond:    cond{"(·.archives)", func(e *upEnv) bool { return len(e.archives) > 0 }},
			inPlace: inVM(func(e *upEnv) (string, error) { return runtimecfg.AuditArchivesCheck(e.archives, agents(e)), nil }),
			run:     func(e *upEnv) error { return e.a.restoreAuditArchives(e) }},
		{label: "Refreshing SSH config", tag: "ssh-config", kind: kindStep, base: []string{".sshConfig"}, cond: always, rerun: true,
			run: func(e *upEnv) error {
				return vmcfg.RefreshGCPSSHConfig(vmcfg.GCPConnection{
					ProjectID: e.state.GCPProjectID, Zone: e.state.Zone, InstanceName: e.state.InstanceName,
					SSHConfigPath: e.state.SSHConfigPath,
				}, e.a.debug)
			}},
		{label: "Preparing SSH wrappers", tag: "ssh-wrappers", kind: kindStep, base: []string{".sshWrappers"}, cond: always, rerun: true,
			run: func(e *upEnv) error { return ensureSSHWrappers(*e.state) }},
		{label: "Configuring audit", tag: "auditd", kind: kindStep, base: []string{".auditd"}, cond: always, rerun: true,
			run: func(e *upEnv) error { return runtimecfg.ConfigureAudit(e.a.auditTarget(e.conn), e.a.debug) }},
		hostClientStep("epweave", "Installing ep_weave client", func(c Config) bool { return c.Epweave != nil }, mergeScriptRes,
			[]string{".skill a .epweave", ".mcp a .epweave"},
			func(c Config) string { return epweave.SetupCommand(*c.Epweave, c.Runtime.Agents) }),
		hostClientStep("github", "Installing GitHub client", func(c Config) bool { return c.GitHub != nil }, []string{`.pkg "git"`},
			[]string{".skill a .github"}, func(c Config) string { return github.SetupCommand(c.Runtime.Agents) }),
		hostClientStep("gitlab", "Installing GitLab client", func(c Config) bool { return c.GitLab != nil }, []string{`.pkg "git"`},
			[]string{".skill a .gitlab"}, func(c Config) string { return gitlab.SetupCommand(c.Runtime.Agents) }),
		hostClientStep("bitbucket", "Installing Bitbucket client", func(c Config) bool { return c.Bitbucket != nil }, []string{`.pkg "git"`},
			[]string{".skill a .bitbucket"}, func(c Config) string { return bitbucket.SetupCommand(c.Runtime.Agents) }),
		hostClientStep("redmine", "Installing Redmine client", func(c Config) bool { return c.Redmine != nil }, nil,
			[]string{".skill a .redmine"}, func(c Config) string { return redmine.SetupCommand(c.Runtime.Agents) }),
		hostClientStep("docker", "Installing Docker client", func(c Config) bool { return c.Docker != nil }, nil,
			[]string{".skill a .docker"}, func(c Config) string { return docker.SetupCommand(c.Runtime.Agents) }),
		hostClientStep("google", "Installing Google client", func(c Config) bool { return c.Google != nil }, nil,
			[]string{".skill a .google"}, func(c Config) string { return google.SetupCommand(c.Runtime.Agents) }),
		hostClientStep("gas", "Installing Apps Script client", func(c Config) bool { return c.GAS != nil }, nil,
			[]string{".skill a .gas"}, func(c Config) string { return gas.SetupCommand(c.Runtime.Agents) }),
		hostClientStep("dns", "Installing DNS client", func(c Config) bool { return c.DNS != nil }, nil,
			[]string{".skill a .dns"}, func(c Config) string { return dns.SetupCommand(c.Runtime.Agents) }),
		hostClientStep("child", "Installing child VM client", func(c Config) bool { return c.Children != nil }, nil,
			[]string{".skill a .child"}, func(c Config) string { return child.SetupCommand(c.Runtime.Agents) }),
		{label: "Preparing tmux", tag: "tmux", kind: kindStep, base: []string{`.pkg "tmux"`, ".tmuxConf"}, cond: always, rerun: true,
			run: func(e *upEnv) error { return e.vm("tmux", runtimecfg.TmuxSetupCommand()) }},
		{label: "Making the agents ring the terminal bell", tag: "notify", kind: kindStep, base: mergeScriptRes,
			each: []string{".notifySetting a"}, cond: agentsCond(), rerun: true,
			run: func(e *upEnv) error { return e.vm("notify", runtimecfg.NotifyCommand(e.cfg.Runtime.Agents)) }},
		{label: "Installing browser opener", tag: "open", kind: kindStep, base: []string{".client .open"}, each: []string{".skill a .open"},
			cond: always, rerun: true,
			run: func(e *upEnv) error { return e.vm("open", browser.SetupCommand(e.cfg.Runtime.Agents)) }},
		{label: "opeco", kind: kindSpecial, special: "opecoT", run: func(e *upEnv) error { return e.a.applyOpeco(e) }},
		{label: "Installing sync status client", tag: "sync", kind: kindStep, base: append([]string{".client .sync"}, mergeScriptRes...),
			each: []string{".skill a .sync", ".hook a .sync"}, cond: always, rerun: true,
			run: func(e *upEnv) error { return e.vm("sync", runtimecfg.SyncClientCommand(e.cfg.Runtime.Agents)) }},
		// up does not wait for the sync: the attach tells the VM how it stands.
		{label: "Starting sync session", tag: "mutagen", kind: kindStep, base: []string{".mutagen"}, cond: always, rerun: true,
			run: func(e *upEnv) error {
				if err := recreateMutagenSession(*e.state, e.a.debug); err != nil {
					return err
				}
				return writeSyncReport(e.conn, syncReport{Status: "starting"})
			}},
		{label: "Running post-install script", tag: "post-install", kind: kindStep, base: []string{".postInstall"},
			cond:  cond{"(·.postInstall)", func(e *upEnv) bool { return e.postInstall }},
			rerun: true, ownPhases: true,
			run: func(e *upEnv) error { return e.a.runPostInstall(e) }},
	}
}

func orTrue(items []string, check func([]string) string) string {
	if len(items) == 0 {
		return "true"
	}
	return check(items)
}

// runUpSteps runs each step of up whose condition holds and that is not
// already in place.
func (a *App) runUpSteps(e *upEnv) error {
	for _, step := range upSteps() {
		if step.kind == kindPrepare || step.kind == kindSpecial {
			if err := step.run(e); err != nil {
				return err
			}
			continue
		}
		if !step.cond.holds(e) {
			continue
		}
		if !step.rerun {
			done, err := step.inPlace(e)
			if err != nil {
				return fmt.Errorf("%s: %w", step.label, err)
			}
			if done {
				a.phase("%s: in place", step.label)
				continue
			}
		}
		if step.ownPhases {
			if err := step.run(e); err != nil {
				return err
			}
			continue
		}
		if err := a.runPhase("%s", func() error { return step.run(e) }, step.label); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) makeVMRun(e *upEnv) error {
	cfg, state := e.cfg, e.state
	switch e.status {
	case "":
		if err := a.runPhase("Creating VM %s", func() error {
			return vmcfg.CreateGCPInstance(vmcfg.GCPInstanceSpec{
				ProjectID:    cfg.GCPProjectID,
				Zone:         cfg.Zone,
				ProjectHash:  cfg.ProjectHash,
				MachineType:  *cfg.VM.GCP.MachineType,
				BootDiskSize: *cfg.VM.GCP.BootDiskSize,
				ImageFamily:  *cfg.VM.GCP.ImageFamily,
				ImageProject: *cfg.VM.GCP.ImageProject,
			}, state.InstanceName, a.debug)
		}, state.InstanceName); err != nil {
			return err
		}
	case vmcfg.GCPStatusTerminated:
		if err := a.runPhase("Starting VM %s", func() error {
			return vmcfg.StartGCPInstance(e.conn, a.debug)
		}, state.InstanceName); err != nil {
			return err
		}
	}
	// Written before anything else, so that `down` can clean up after an up
	// that fails from here on.
	return SaveState(*state)
}

func (a *App) waitForVM(e *upEnv) error {
	if err := a.runPhase("Waiting for SSH", func() error { return vmcfg.WaitForGCPSSH(e.conn) }); err != nil {
		return err
	}
	workspacePath, err := vmcfg.GetGCPWorkspacePath(e.conn)
	if err != nil {
		return err
	}
	e.state.RemoteWorkspacePath = workspacePath
	return nil
}

// waitForEarlierUp waits for what an earlier, interrupted up left running
// in the VM: the specification assumes that an up starts after it.
func (a *App) waitForEarlierUp(e *upEnv) error {
	deadline := time.Now().Add(upWait)
	shown := ""
	for {
		out, err := vmcfg.CaptureGCPRemoteCommand(e.conn, runtimecfg.RunningUpStepsCommand())
		if err != nil {
			return fmt.Errorf("look for what an earlier up left running: %w", err)
		}
		running, err := runtimecfg.ParseRunningUpSteps(out)
		if err != nil {
			return err
		}
		if len(running) == 0 {
			return nil
		}
		list := describeRunning(running)
		if e.ignoreLeft {
			fmt.Fprintf(a.stderr, "warning: going on beside what an earlier up left running; the steps of this up may not reach their result:\n%s", list)
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("an earlier up still runs in the VM after %s:\n%s`isotty up --ignore-running` goes on without waiting", upWait, list)
		}
		if list != shown {
			fmt.Fprintf(a.stdout, "==> Waiting for what an earlier up left running:\n%s", list)
			shown = list
		}
		time.Sleep(5 * time.Second)
	}
}

func describeRunning(running []runtimecfg.RunningUpStep) string {
	var b strings.Builder
	for _, r := range running {
		fmt.Fprintf(&b, "    %s: pid %d, %s\n", r.Step, r.PID, time.Duration(r.Seconds)*time.Second)
	}
	return b.String()
}

// restoreAgentHomes restores, for each agent the host saved a home of,
// the saved paths that are absent from the VM.
func (a *App) restoreAgentHomes(e *upEnv) error {
	for _, auth := range e.auths {
		done, err := e.holds(runtimecfg.AgentHomeCheck(auth.paths))
		if err != nil {
			return err
		}
		if done {
			continue
		}
		if err := a.runPhase("Restoring %s login state", func() error {
			return e.vmInput("restore-"+auth.agent, runtimecfg.RestoreAgentHomeCommand(auth.agent), auth.archive)
		}, auth.agent); err != nil {
			return fmt.Errorf("restore %s login state: %w", auth.agent, err)
		}
	}
	return nil
}

func (a *App) restoreAuditArchives(e *upEnv) error {
	for _, archive := range e.archives {
		stream, err := runtimecfg.OpenAuditArchive(e.cfg.HomeDir, archive)
		if err != nil {
			return err
		}
		var buf bytes.Buffer
		_, err = buf.ReadFrom(stream)
		stream.Close()
		if err != nil {
			return fmt.Errorf("read %s: %w", filepath.Base(archive), err)
		}
		if err := e.vmInput("audit-archives", runtimecfg.RestoreAuditArchiveCommand(e.cfg.Runtime.Agents), buf.Bytes()); err != nil {
			return fmt.Errorf("restore audit archive %s: %w", filepath.Base(archive), err)
		}
	}
	return nil
}

func (a *App) applyOpeco(e *upEnv) error {
	if opeco.Active(e.cfg.Runtime.Agents, e.cfg.Opeco) {
		return a.runPhase("Installing opeco (QR page on port %d)", func() error {
			return e.vm("opeco", opeco.SetupCommand(e.cfg.Runtime.Agents, e.state.OpecoQRPort))
		}, e.state.OpecoQRPort)
	}
	if len(e.cfg.Runtime.Agents) == 0 {
		return nil
	}
	return a.runPhase("Removing opeco", func() error { return e.vm("opeco", opeco.RemoveCommand()) })
}

// runPostInstall runs the project's script on every up, on the synced
// workspace it reads.
func (a *App) runPostInstall(e *upEnv) error {
	if err := a.runPhase("Waiting for the sync, which the post-install script reads", func() error {
		return flushMutagenSession(*e.state)
	}); err != nil {
		return err
	}
	return e.vm("post-install", runtimecfg.PostInstallCommand(e.state.RemoteWorkspacePath))
}
