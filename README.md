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

Destroy the environment:

```bash
./bin/isotty down
```

## Common Commands

```bash
./bin/isotty status
./bin/isotty audit logs
./bin/isotty audit logs -f
./bin/isotty --debug up
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

Port forwards:

```bash
./bin/isotty forward add web --local-port 8080 --remote-port 8080
./bin/isotty forward list
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

## Sync Modes

Default:

```bash
./bin/isotty up
```

Two-way development mode:

```bash
./bin/isotty up --sync two-way-safe
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
* [Contributing](CONTRIBUTING.md)
