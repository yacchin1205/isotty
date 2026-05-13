# IsoTTY

IsoTTY is a disposable isolated development sandbox.

It creates an isolated environment, syncs a selected local project directory into it, and lets you attach and work inside it. The goal is to run risky development tasks—such as `npm install`, unknown setup scripts, or coding agents—outside your local machine.

## Concept

Developer machines often contain sensitive credentials:

- SSH keys
- cloud credentials
- package registry tokens
- `.env` files
- Kubernetes configs
- Docker sockets
- private source trees

IsoTTY keeps those out of the execution environment by default.

```text
Local machine
  ./project
     |
     | sync
     v
Isolated environment
  /workspace
```

The VM should not be able to access the local machine directly.

## Basic Usage

```bash
cd ~/workspace/product
isotty up
isotty attach
```

Inside the VM:

```bash
cd /workspace
npm install
npm test
```

Destroy the VM:

```bash
isotty down
```

## Getting Started

Current implementation status:

* GCP VM backend
* project sync using Mutagen
* `one-way-safe` and `two-way-safe`

Prerequisites:

* Go
* `gcloud`
* `mutagen`
* authenticated `gcloud` access to a GCP project

Build:

```bash
go build -o ./bin/isotty ./cmd/isotty
```

Check dependencies:

```bash
./bin/isotty version
```

Set the GCP zone if needed:

```bash
export ISOTTY_GCP_ZONE=us-central1-f
```

Start an environment for the current directory:

```bash
./bin/isotty up
```

Check sync and environment status:

```bash
./bin/isotty status
```

Attach to the environment:

```bash
./bin/isotty attach
```

Inside the environment, the synced project is available at:

```bash
/workspace
```

Destroy the environment:

```bash
./bin/isotty down
```

IsoTTY stores local state under:

```text
~/.isotty
```

## Sync Model

IsoTTY supports two sync modes.

### Default: one-way-safe

By default, IsoTTY syncs the selected local project directory into the VM.

```text
local -> VM
```

Changes made inside the VM are not automatically written back to the local machine.

This is the safest default for running unknown setup scripts, package installs, or untrusted code.

```bash
isotty up ./product
```

### Development mode: two-way-safe

For normal development work, IsoTTY can run in conservative two-way sync mode.

```bash
isotty up ./product --sync two-way-safe
```

In this mode, changes can flow in both directions.

```text
local <-> VM
```

This is useful when you want to edit locally while running builds, tests, package installs, or coding agents inside the VM.
Conflicts that could cause unsynchronized data loss are not auto-resolved.

## Design Principles

* Use a disposable VM, not the local host.
* Sync only the selected project directory.
* Do not mount the local home directory.
* Do not forward SSH agent by default.
* Do not copy cloud credentials by default.
* Do not expose the local machine to the VM.
* Use a standard terminal workflow.

## Default Exclusions

IsoTTY should not sync these by default:

```text
.env
.env.*
.npmrc
.pypirc
*.pem
*.key
.ssh/
.aws/
.gcloud/
.azure/
.kube/
.docker/
node_modules/
.venv/
```

## MVP

The first version should support:

* `isotty up [PATH]`
* `isotty up [PATH] --sync two-way-safe`
* `isotty attach`
* `isotty down`
* GCP VM creation via `gcloud`
* Ubuntu VM bootstrap
* project sync using Mutagen
* default one-way-safe sync
* optional two-way-safe sync
* default ignore rules for common secrets

## Non-Goals

IsoTTY is not:

* a full IDE
* a remote desktop
* a team workspace platform
* a VPN
* a perfect malware sandbox

It is a practical isolation layer for risky development work.

## Name

IsoTTY means:

```text
isolated TTY
```

The name reflects the goal: keep the normal terminal workflow, but move risky execution into an isolated environment.
