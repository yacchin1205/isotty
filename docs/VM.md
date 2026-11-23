# VM Design

The first IsoTTY backend is a disposable GCP VM created with `gcloud`.

## User Story

```bash
isotty up
isotty stop
isotty down
```

## Contract

* `up` creates or reuses one VM per project, starting it when `stop` left it stopped
* `attach` opens a tmux session named `isotty` in the workspace over `gcloud compute ssh`, attaching to it when it exists, so what runs there survives a detach or a dropped connection; it refuses a VM that is not running, since `up` is what starts one
* while `attach` connects, a terminal shows a tide pool with a crab in it, after IsoTTY's logo; it is drawn on the host during a wait that passes anyway, not as a tmux popup in the session, which would take the keys typed ahead; a pipe or a file gets none of it, nor does `--debug`
* `attach --no-host` joins that session as a grouped tmux session: the same windows, its own current window, gone when it detaches
* `attach --no-tmux`, or `tmux: false` in `~/.isotty/attach.yaml` on the host, gives a plain login shell; `up` installs tmux when the image lacks it and writes `/etc/tmux.conf`, which tells tmux to relay OSC 8 hyperlinks to the attached terminal: tmux relays them only to a terminal it knows takes them, which it tells from `TERM_PROGRAM`, and ssh does not carry that, so a link an agent prints would otherwise stop at tmux
* `id` prints a stable VM target id for remote attach
* `attach --target ...` attaches without local project state
* `stop` stops the VM and keeps its disk and the local state; the sync session ends with it
* `down` deletes the VM and removes local state, starting a stopped VM first to save the agents' logins and the audit log

## Identity

Each project maps to one stable VM name:

```text
isotty-<project-hash>
```

The hash is derived from the absolute project path.

## GCP Context

Project and zone are each resolved in this order:

1. `ISOTTY_GCP_PROJECT` / `ISOTTY_GCP_ZONE`
2. where the project was last brought up
3. active `gcloud` project / compute/zone

If either value is missing, IsoTTY should fail.

Where a project was last brought up is kept on the host in
`~/.isotty/projects/<hash>.gcp.yaml` and outlives `down`: the active
`gcloud` configuration is shared with other work and changes under it, and
a project should come back where it was without the human naming it again.
It is not in `.isotty/`, which is synced, so that the VM cannot point the
next `up` at another GCP project.

While the VM is up, `up` refuses a project or zone other than the VM's:
it would find no VM there and make a second one, leaving the first with no
state pointing at it. Moving takes a `down` first.

## VM Shape

Current defaults:

* Ubuntu LTS
* machine type: `e2-standard-4`
* boot disk size: `50GB`
* workspace path: `/workspace`

Project-local VM shape can be configured with:

```text
./.isotty/vm.yaml
```

Example:

```yaml
provider: gcp

gcp:
  machine_type: e2-standard-8
  boot_disk_size: 200GB
  image_family: ubuntu-2404-lts-amd64
  image_project: ubuntu-os-cloud
```

Or update it from the CLI:

```bash
isotty vm gcp show
isotty vm gcp set --machine-type e2-standard-8 --boot-disk-size 200GB
```

## Up

Every `up` brings the VM to the configuration, whether it created the VM or
not, so a VM whose first `up` failed, or one made before a package was added
to the configuration, still gets it. What is installed keeps its version:
checking costs far less than reinstalling on every `up`.

`~/.isotty/home` is copied over the VM home on every `up`, so personal files
such as `~/.claude/CLAUDE.md` are the same on every VM.

The post-install script runs on every `up`. Running it exactly once cannot be
promised when an `up` may stop anywhere; a script that must do something once
checks that for itself.

`up` first waits, up to 10 minutes, for what a stopped `up` left running in
the VM, since a step that finishes late can undo the new `up`'s work.
`isotty up --ignore-running` goes on without waiting.

That the steps reach the configuration is proved in [spec/](../spec/README.md).

## Local State

IsoTTY keeps local VM state under:

```text
~/.isotty
```

It is written early enough that `isotty down` can still clean up after a failed `up`.

The stored state includes:

* project path
* project hash
* GCP project
* zone
* instance name
* sync mode

## Security Posture

The VM backend assumes:

* no local home directory mount
* no SSH agent forwarding by default
* no service account attached to the VM and no access scopes, so nothing in
  the VM can obtain a Google credential from the metadata server
* no automatic credential copying from the host; logins made in the VM are
  kept per project, see [Agent design](AGENT.md)

This is a practical isolation layer, not a high-assurance sandbox.
