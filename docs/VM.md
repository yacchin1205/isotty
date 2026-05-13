# VM Design

This document describes the first VM-based implementation story for IsoTTY.

The goal is to make `isotty up`, `isotty attach`, and `isotty down` work with a disposable GCP VM created via `gcloud`.

## Scope

This document covers:

* creating a disposable VM on GCP
* bootstrapping the VM for development use
* attaching from the local machine into the VM
* preparing the VM for project sync
* destroying the VM cleanly

This document does not yet cover:

* container-based isolation
* multi-user support
* advanced network hardening
* a full local state model

## User Story

The basic story is:

```bash
cd ~/workspace/product
isotty up
isotty attach
isotty down
```

Expected behavior:

1. `isotty up` creates a fresh VM if one does not already exist.
2. The VM is bootstrapped with the minimum tools needed for shell access and sync.
3. The selected local project is prepared for sync into `/workspace`.
4. `isotty attach` opens an interactive shell inside the isolated environment.
5. `isotty down` stops and deletes the disposable environment.

## Design Goals

* Use GCP as the first backend.
* Keep the VM disposable and per-project.
* Avoid exposing the local machine to the VM.
* Avoid copying local credentials into the VM by default.
* Keep the first implementation operationally simple.

## Proposed Backend

The first backend is a single Ubuntu VM created with `gcloud compute instances create`.

Why this is a good MVP:

* `gcloud` is already sufficient for provisioning, SSH, and teardown.
* A VM gives a strong boundary and clean mental model.
* Mutagen can sync against a standard SSH target.
* The implementation can later be abstracted behind a generic "environment" interface.

## Lifecycle

### 1. `isotty up`

`isotty up [PATH]` should do the following:

1. Resolve the project path.
2. Derive a stable environment name from the project path.
3. Check whether an active IsoTTY VM already exists for that project.
4. Create the VM if it does not exist.
5. Wait until SSH is reachable.
6. Bootstrap required packages and directories.
7. Start or configure the sync session.
8. Save enough local metadata so `attach` and `down` can find the environment again.

### 2. `isotty attach`

`isotty attach` should:

1. Load the local metadata for the current project.
2. Resolve the active environment.
3. Open an interactive shell in the VM.

For the VM backend, the simplest implementation is a thin wrapper around `gcloud compute ssh`.

### 3. `isotty down`

`isotty down` should:

1. Stop the sync session if one exists.
2. Delete the VM.
3. Remove local metadata for the environment.

If the VM is already gone, cleanup should still succeed locally.

## VM Shape

Initial recommendation:

* OS: Ubuntu LTS
* Machine type: small default such as `e2-standard-4`
* Disk: modest persistent boot disk, enough for package installs and builds
* Network: standard outbound access, no inbound exposure beyond SSH
* Lifetime: disposable, deleted by `isotty down`

This is intentionally conservative. It should optimize for predictable behavior, not minimum cost.

## Naming

Each project should map to one disposable environment name.

A practical pattern is:

```text
isotty-<short-hash>
```

Where the hash is derived from the absolute local project path.

Why:

* stable across repeated runs
* avoids collisions between projects with the same basename
* does not leak the full local path into the VM name

## Labels and Metadata

The VM should be labeled so IsoTTY can identify and manage it.

Suggested labels:

* `app=isotty`
* `project_hash=<hash>`
* `backend=vm`

Local metadata should also record:

* project path
* project hash
* GCP project ID
* zone
* instance name
* sync mode

## GCP Context Resolution

For the MVP, IsoTTY should not take `project` or `zone` as CLI arguments.

Instead, it should resolve them in this order.

Project:

1. `ISOTTY_GCP_PROJECT`
2. active `gcloud` config `project`
3. error if neither is available

Zone:

1. `ISOTTY_GCP_ZONE`
2. active `gcloud` config `compute/zone`
3. error if neither is available

This keeps the CLI simple while still allowing IsoTTY-specific overrides.

## Bootstrap

The VM should be bootstrapped with only what is required for the MVP.

Required:

* a login shell environment
* `/workspace` directory
* a non-root user workflow
* packages needed for sync support if required by Mutagen

Nice to keep simple:

* avoid a complex startup agent
* prefer idempotent shell bootstrap steps

A straightforward first approach:

1. create the VM
2. wait for SSH
3. run a remote bootstrap script over SSH

The bootstrap script should:

* create `/workspace` if missing
* set ownership correctly
* install only minimal packages

## SSH and Attach

For the VM backend, `attach` should use the transport already provided by `gcloud`.

That likely means:

```bash
gcloud compute ssh <instance-name> --zone <zone>
```

This keeps the first implementation simple:

* no custom SSH config is required
* authentication stays in the user's existing `gcloud` flow
* the CLI does not need to manage raw SSH keys in the MVP

## Sync Preparation

Mutagen expects a reachable remote endpoint.

For the VM backend, the remote side should expose:

* SSH access
* a stable workspace path such as `/workspace`

The sync layer should treat the VM as the remote endpoint and apply IsoTTY default exclusions.

This document does not define the full sync implementation, but the VM design should assume sync starts after bootstrap completes.

## Local State

IsoTTY needs a small amount of local state so commands can find the active environment.

A simple first approach is a per-project state file under a local IsoTTY directory.

Current recommendation:

```text
~/.isotty
```

Example fields:

```json
{
  "project_path": "/Users/example/workspace/product",
  "project_hash": "abc12345",
  "backend": "gcp-vm",
  "instance_name": "isotty-abc12345",
  "zone": "asia-northeast1-b",
  "sync_mode": "one-way-safe"
}
```

The exact file location can be decided later. The important point is that the state is local, explicit, and disposable.

## Failure Handling

The CLI should favor cleanup and retry over complicated recovery.

Examples:

* If VM creation succeeds but bootstrap fails, `isotty up` should report the failure clearly and suggest `isotty down`.
* If sync setup fails after VM creation, local state should still point to the VM so the user can inspect it with `isotty attach`.
* If `isotty down` cannot find the VM, it should still remove stale local metadata after warning.

## Security Posture

The MVP security posture should be clear but modest:

* no local home directory mounts
* no SSH agent forwarding by default
* no automatic credential copying
* no assumption that the VM is perfectly hardened

This is a practical isolation layer, not a high-assurance sandbox.

## Open Questions

These should be decided before implementation starts:

* Should the default machine type prioritize lower cost or faster builds?
* Should `isotty up` reuse an existing VM for the same project, or fail unless `down` is run first?
* Should bootstrap use startup scripts, or plain post-create SSH commands?
* Where should local state live?

## Current Recommendation

For the first implementation:

* backend: GCP VM only
* transport: `gcloud compute ssh`
* GCP context: `ISOTTY_GCP_PROJECT` and `ISOTTY_GCP_ZONE`, then active `gcloud` config
* local state root: `~/.isotty`
* bootstrap: post-create remote shell script
* workspace path: `/workspace`
* environment identity: stable project-path hash
* lifecycle: explicit `up`, `attach`, `down`

This is enough to validate the core IsoTTY model before introducing containers or more abstract backends.
