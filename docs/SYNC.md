# Sync Design

This document describes the synchronization model for IsoTTY.

The goal is to define how a local project is synchronized into an isolated environment without exposing the local machine directly to that environment.

## Scope

This document covers:

* the role of synchronization in IsoTTY
* why direct bind mounts are not the default model
* synchronization modes and their intended use
* the expected Mutagen-based implementation model
* security boundaries for local and remote content

This document does not yet cover:

* exact CLI implementation details
* every Mutagen flag
* advanced performance tuning

## Design Goal

IsoTTY should let the user work on a local project while risky execution happens in an isolated environment.

The sync layer is a core part of that promise.

It is not just a convenience feature. It is part of the security boundary.

## Why Sync Exists

IsoTTY does not want the isolated environment to edit the local project directory directly.

That is why the default model is:

```text
local project -> synchronized workspace -> isolated environment
```

Not:

```text
local project -> direct mount -> isolated environment
```

The difference matters because synchronization gives IsoTTY control over:

* which files are visible remotely
* which changes are allowed to flow back
* how conflicts are handled
* how dangerous writes are contained

## Why Direct Bind Mounts Are Not the Default

For a Docker backend, the simplest implementation would be a direct host bind mount.

IsoTTY should avoid making that the default because it weakens the isolation story.

With a direct bind mount:

* the isolated process can modify local files immediately
* local secret files are easier to expose accidentally
* deletions and bulk rewrites hit the local project directly
* there is no synchronization policy boundary
* there is no safe conflict model

This is convenient, but it is not aligned with IsoTTY's safety model.

If IsoTTY supports Docker, the safer design is:

* the container has its own workspace
* local content is synchronized into that workspace
* the local project is not mounted directly by default

## Core Model

IsoTTY should treat synchronization as a session between:

* a local project root
* a remote workspace root such as `/workspace`

The remote workspace belongs to the isolated environment, whether that environment is:

* a VM
* a container

The sync layer should be backend-agnostic at the policy level, even if the transport differs.

## Default Implementation Direction

The current recommendation is to use Mutagen as the synchronization engine.

Why:

* it already supports safe and conservative sync modes
* it works across SSH-accessible remote environments
* it can also work with containers
* it avoids turning the local project into a live mount

IsoTTY should treat Mutagen as the implementation mechanism for sync, but the user-facing contract should be expressed in IsoTTY terms.

## Mutagen Execution Model

Mutagen uses a per-user background daemon on the local machine.

That means the control plane for synchronization lives locally, not inside the isolated environment.

For IsoTTY, this implies:

* Mutagen is started and managed from the local machine
* sync sessions are created locally
* the isolated environment is an endpoint, not the controller
* session metadata and state exist locally
* IsoTTY should use a dedicated `MUTAGEN_DATA_DIRECTORY`

This model is acceptable for IsoTTY because the local daemon is orchestrating file transfer, not granting the remote environment direct access to the local filesystem.

Using a dedicated `MUTAGEN_DATA_DIRECTORY` keeps IsoTTY-managed sessions separate from any other Mutagen usage on the local machine.

Current recommendation:

```text
~/.isotty/mutagen
```

## Session Ownership

IsoTTY should own the lifecycle of the sync session it creates.

That means:

* `isotty up` creates or resumes the session
* `isotty down` terminates the session
* session names and labels should be stable per project
* the user should not need to create Mutagen sessions manually

A practical session naming pattern is:

```text
isotty-<project-hash>
```

Suggested labels:

* `app=isotty`
* `project_hash=<hash>`
* `sync_mode=<mode>`

## Sync Modes

IsoTTY should expose a small, opinionated subset of Mutagen's model.

### Default: `one-way-safe`

This should be the default mode.

Behavior:

* local changes are allowed to flow to the isolated environment
* remote deletions are overwritten by local content
* remote modifications that would lose unsynchronized remote data are not overwritten automatically
* extra non-conflicting remote content can remain

This mode is appropriate for:

* unknown setup scripts
* package installation
* risky code execution
* any session where the remote side should not be trusted to write back freely

### Optional: `two-way-safe`

This should be opt-in.

Behavior:

* changes can flow in both directions
* conflicts that would cause loss of unsynchronized data are not auto-resolved

This mode is appropriate for:

* normal development work
* editing locally while building or running tools remotely

IsoTTY should not expose more aggressive modes in the first version unless there is a strong reason.

## Default Exclusions

The default exclusion list is part of the security model, not just a convenience feature.

IsoTTY should apply exclusions automatically whenever it creates a sync session.

The user should not need to remember to pass these manually.

Baseline exclusions:

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

These defaults should be overridable later, but they should always exist by default.

## Remote Workspace Boundary

Synchronization should target a dedicated workspace path in the isolated environment.

The current recommendation is:

```text
/workspace
```

IsoTTY should assume:

* sync only touches the selected project subtree
* the remote endpoint path is dedicated to IsoTTY work
* synchronization is not responsible for arbitrary remote filesystem management outside the workspace

This keeps the write surface small and predictable.

## Backend-Specific Transport

The sync policy should be shared across backends, but the transport can differ.

Examples:

* VM backend: SSH-accessible endpoint
* Docker backend: Docker-aware endpoint or SSH-accessible container host

The important design rule is that transport should not change the safety model.

In particular:

* the backend should not bypass sync by directly mounting the local project by default
* the backend should still synchronize into an isolated workspace

## Local State

IsoTTY needs to remember enough information to manage the sync session later.

At minimum:

* project path
* project hash
* sync mode
* sync session name
* backend type
* remote workspace path
* IsoTTY-managed Mutagen data directory path

The current recommendation is to keep all local IsoTTY-managed sync state under:

```text
~/.isotty
```

If necessary, it may also record a Mutagen session identifier, but a stable IsoTTY-managed name is likely enough for the MVP.

## Failure Handling

Synchronization failures should preserve debuggability.

Examples:

* If environment creation succeeds but sync creation fails, IsoTTY should keep enough state for the user to inspect the environment.
* If a sync session encounters conflicts, IsoTTY should report that clearly rather than trying to force resolution.
* If `isotty down` cannot find the remote environment, it should still terminate the local sync session if possible.

IsoTTY should prefer explicit error states over silent fallbacks.

## Security Posture

The sync design should enforce these principles:

* do not directly expose the local project to remote execution by default
* do not sync common secret files by default
* do not automatically resolve conflicts that would lose unsynchronized data
* do not make the backend transport choice erase the safety boundary

This does not make the remote environment safe in an absolute sense.

It does make the file movement model more controlled and auditable.

## Open Questions

These still need decisions:

* Should IsoTTY create exactly one sync session per project, or support multiple named environments for one project later?
* How should user-defined extra exclusions be configured?
* Should `isotty attach` warn when sync conflicts are present?

## Current Recommendation

For the first implementation:

* synchronization engine: Mutagen
* controller location: local machine
* Mutagen state: dedicated `MUTAGEN_DATA_DIRECTORY` for IsoTTY
* local state root: `~/.isotty`
* default mode: `one-way-safe`
* opt-in development mode: `two-way-safe`
* remote workspace: `/workspace`
* local project exposure: no direct bind mount by default
* session identity: stable project-based name and labels

This is enough to preserve IsoTTY's core safety model across both VM and future Docker backends.
