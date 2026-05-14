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

## Install From Source

```bash
go build -o ./bin/isotty ./cmd/isotty
```

## Quick Start

Set the GCP zone if needed:

```bash
export ISOTTY_GCP_ZONE=us-central1-f
```

Create an environment for the current project:

```bash
./bin/isotty up
```

Attach:

```bash
./bin/isotty attach
```

Inside the VM:

```bash
cd /workspace
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
* [Contributing](CONTRIBUTING.md)
