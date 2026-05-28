# IsoTTY

IsoTTY is a disposable isolated development sandbox.

It creates an isolated environment for a project, syncs files into `/workspace`, and lets you attach and work there instead of running risky tasks on your local machine.

## Concept

Developer machines tend to hold credentials, tokens, and private source trees.
IsoTTY keeps risky tasks such as package installs, setup scripts, and agent execution inside a disposable isolated environment instead of running them directly on the local machine.
The goal is to preserve a normal terminal workflow while moving execution across a safer boundary.

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
isotty forward add web --local-port 8080 --remote-port 8080
isotty forward list
```

Stored in:

```text
./.isotty/forward.yaml
```

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
* [Audit design](docs/AUDIT.md)
* [Agent design](docs/AGENT.md)
* [Tools design](docs/TOOLS.md)
* [Contributing](CONTRIBUTING.md)
