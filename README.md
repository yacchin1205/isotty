# IsoTTY

<p align="center"><img src="docs/logo.png" width="280" alt="A crab waving from a tide pool"></p>

IsoTTY is a disposable isolated development sandbox.

The name is *iso*lated + *tty*, but it reads just as well as 磯 (iso), the rocky shore: a tide pool is a little sea of its own, cut off from the ocean until the next tide, and so is each VM.

It creates an isolated environment for a project, syncs files into `/workspace`, and lets you attach and work there instead of running risky tasks on your local machine.

## Concept

Developer machines tend to hold credentials, tokens, and private source trees.
IsoTTY keeps risky tasks such as package installs, setup scripts, and agent execution inside a disposable isolated environment instead of running them directly on the local machine.
The goal is to preserve a normal terminal workflow while moving execution across a safer boundary.

IsoTTY trusts the agent and distrusts mistakes: it is not a cage for a hostile agent, but a way to agree with the human on what the agent may reach, keep a mistake inside that agreement, and show afterwards what happened.

## Requirements

* `gcloud`
* `mutagen`
* authenticated `gcloud` access to a GCP project

## Install

Download a release binary from GitHub Releases and place it somewhere on your `PATH`:

* https://github.com/yacchin1205/isotty/releases

For example:

```bash
curl -LO https://github.com/yacchin1205/isotty/releases/download/v2026.5.0/isotty_2026.5.0_darwin_arm64
mv isotty_2026.5.0_darwin_arm64 /usr/local/bin/isotty
chmod +x /usr/local/bin/isotty
```

## Quick Start

Set the GCP zone if needed:

```bash
export ISOTTY_GCP_ZONE=us-central1-f
```

Create an environment for the current project:

```bash
isotty up
```

Inside the VM:

```bash
pwd
# /workspace
```

Prepare the environment without attaching:

```bash
isotty up --no-attach
```

Attach later when you are ready to work in the VM. This also enables any configured port forwards:

```bash
isotty attach
```

Destroy the environment:

```bash
isotty down
```

`down` saves the configured agents' login state from the VM into `.isotty/`, encrypted with a key kept under `~/.isotty`, so the next `isotty up` of the same project starts logged in. See [Agent design](docs/AGENT.md).

## Common Commands

```bash
isotty status
isotty audit logs
isotty audit logs -f
isotty --debug up
```

## Remote Attach

`isotty id` prints a stable VM target id for the current environment.
Use `isotty attach --target ...` to attach from outside the original project directory.

```bash
isotty id
isotty attach --target <isotty-id>
```

## Project Config

Optional apt packages:

```text
./.isotty/apt.txt
```

Example:

```text
ripgrep
jq
```

Tool bundles:

Tool bundles are curated environments you select by name instead of listing
every package yourself. Each bundle installs a set of apt packages plus any
follow-up setup (for example a Python virtualenv).

```bash
isotty runtime tools available
isotty runtime tools enable doc-tools
isotty runtime tools list
```

Stored in:

```text
./.isotty/tools.yaml
```

The first bundle is `doc-tools`, for reading PDF announcements, checking Word
templates, and organizing submitted documents. See [Tools design](docs/TOOLS.md).

Port forwards:

These forwards are applied while attached, so services running in the VM are reachable on local `localhost` ports.

```bash
isotty host forward add web --local-port 8080 --remote-port 8080
isotty host forward list
```

Stored in:

```text
./.isotty/forward.yaml
```

ep_weave notes:

The agent in the VM can read and write pads in the [ep_weave](https://github.com/NII-cloud-operation/ep_weave) running on the host, through a gateway that `isotty attach` provides.
The VM never sees ep_weave itself or its credentials, and can write only under the project's title prefix.

```bash
isotty host epweave enable --prefix myproject
isotty up
```

Credentials live on the host in `~/.isotty/epweave.yaml`; the project setting is stored in:

```text
./.isotty/epweave.yaml
```

Inside the VM:

```bash
isotty-notes status
isotty-notes create 'myproject/design' --tags '#myproject' < memo.md
```

See [ep_weave design](docs/EPWEAVE.md).

GitHub:

The agent in the VM can push, fetch and work with pull requests, issues and workflow runs of the repositories you name, through a relay that `isotty attach` provides with your GitHub token.
The VM never holds the token and cannot reach any other repository.

```bash
isotty host github add --remote origin --allow write
isotty up
```

The token lives on the host in `~/.isotty/github.yaml`; the project setting is stored in:

```text
./.isotty/github.yaml
```

Inside the VM:

```bash
isotty-github push -u origin HEAD
isotty-github pr create --title 'Add relay' --base main < body.md
```

See [GitHub design](docs/GITHUB.md).

Google:

The agent in the VM can use Google Docs, Sheets, Forms and other Google APIs as a service account you choose, for the files shared with that account and the scopes you pin.
The key stays on the host; `isotty attach` hands the VM one-hour tokens.

```bash
isotty host google enable --key ~/Downloads/my-project-1234.json --scope documents.readonly
isotty up
```

The key is copied under `~/.isotty/google/`; the project setting is stored in:

```text
./.isotty/google.yaml
```

Inside the VM, tools that read `GOOGLE_OAUTH_ACCESS_TOKEN` run through the client:

```bash
isotty-google exec -- node tools/document-cli.js get <document-id>
```

See [Google design](docs/GOOGLE.md).

Apps Script:

The agent in the VM can run functions of the Google Apps Script projects you name, as you, through a relay that `isotty attach` provides with your login.
The VM never holds the token and cannot reach any other script.

```bash
isotty host gas add --script <script-id> --client ~/Downloads/client_secret.json \
    --scope https://www.googleapis.com/auth/spreadsheets
isotty up
```

The login lives on the host in `~/.isotty/gas/`; the project setting is stored in:

```text
./.isotty/gas.yaml
```

Inside the VM:

```bash
isotty-gas run <script-id> sendRequests '["<sheet-id>", "2026-10"]'
```

See [Apps Script design](docs/GAS.md).

opeco notifications:

When an agent is configured, `isotty up` also installs [opeco](https://github.com/yacchin1205/opeco-link) in the VM and registers it as the agent's `opeco-link` MCP server, so the agent can keep you informed on your phone while your machine sleeps.
Pairing happens once while attached: the QR page opens in your browser through the attach session.

See [opeco design](docs/OPECO.md).

Optional Node.js runtime version:

```text
./.isotty/node.txt
```

Put only a Node.js major version in this file, for example `22`.
Node.js is installed from the NodeSource apt repository when `node.txt` is present.

Optional agent install config:

```text
./.isotty/agent.yaml
```

Optional post-install script:

```text
./.isotty/post-install.sh
```

If present, it runs in the VM after sync is ready.
It runs with `sudo`.

```bash
isotty runtime post-install path
```

Optional services:

```text
./.isotty/service.yaml
```

Example:

```yaml
services:
  docker: {}
```

Or update it from the CLI:

```bash
isotty runtime service enable docker
isotty runtime service list
```

Optional GCP VM shape:

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

## Files for the VM Home

Files placed under `~/.isotty/home` on the host are copied into the home directory of every VM this host creates, with the same relative paths. This is how personal instructions reach the agents, for example `~/.isotty/home/CODE_POLICY.md` and a `~/.isotty/home/.claude/CLAUDE.md` that tells Claude to read it.

The directory must hold copies: `isotty up` stops if it finds a symbolic link, naming the link. Files are copied when the VM is created, so changes reach a VM only through `down` and `up`.

## Sync Modes

Default:

```bash
isotty up
```

One-way mode:

```bash
isotty up --sync one-way-safe
```

## Local State

IsoTTY stores local state under:

```text
~/.isotty
```

## Docs

* [VM design](docs/VM.md)
* [Sync design](docs/SYNC.md)
* [Forward design](docs/FORWARD.md)
* [ep_weave design](docs/EPWEAVE.md)
* [GitHub design](docs/GITHUB.md)
* [Google design](docs/GOOGLE.md)
* [Apps Script design](docs/GAS.md)
* [opeco design](docs/OPECO.md)
* [Audit design](docs/AUDIT.md)
* [Agent design](docs/AGENT.md)
* [Tools design](docs/TOOLS.md)
* [Contributing](CONTRIBUTING.md)
