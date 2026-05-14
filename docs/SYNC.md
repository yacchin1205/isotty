# Sync Design

IsoTTY sync exists to keep risky execution away from the local project directory.

## Core Model

The model is:

```text
local project -> synchronized workspace -> isolated environment
```

Not:

```text
local project -> direct mount -> isolated environment
```

## Engine

The first synchronization engine is Mutagen.

IsoTTY manages Mutagen from the local machine and treats the isolated environment as the remote endpoint.

## Local State

IsoTTY keeps its sync state under:

```text
~/.isotty
~/.isotty/mutagen
```

IsoTTY uses its own `MUTAGEN_DATA_DIRECTORY` instead of sharing the user's default Mutagen state.

## Session Model

Each project owns one stable sync session:

```text
isotty-<project-hash>
```

IsoTTY owns the lifecycle:

* `up` creates the session
* `down` terminates it

## Modes

Default:

* `one-way-safe`

Optional:

* `two-way-safe`

`one-way-safe` is the default because it keeps remote execution from freely writing back to the local machine.

## Workspace

The remote sync target is:

```text
/workspace
```

## Default Exclusions

IsoTTY applies these by default:

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

These defaults are part of the safety model.

## Security Posture

The sync design assumes:

* no direct bind mount of the local project by default
* no silent conflict resolution that can lose unsynchronized data
* no automatic syncing of common secret material
