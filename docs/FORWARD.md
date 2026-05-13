# Forward Design

This document describes the port forwarding model for IsoTTY.

The goal is to let services running inside the isolated environment be reachable on the local machine while an attach session is active.

## Scope

This document covers:

* project-local forward configuration
* the relationship between `attach` and port forwarding
* the proposed CLI for managing forward definitions
* lifecycle and failure behavior

This document does not yet cover:

* background forwarding without `attach`
* non-TCP transports
* forwarding across multiple environments for one project

## Design Goal

IsoTTY should make common development servers inside the isolated environment easy to access locally.

Examples:

* a web app running on remote port `8080`
* a Vite dev server on remote port `5173`
* a preview server on remote port `3000`

The user should be able to open `localhost:<port>` on the local machine while working inside the isolated environment.

## Core Model

Port forwarding should be project-local configuration, not global machine configuration.

The current recommendation is:

```text
./.isotty/forward.yaml
```

This file belongs to the project and describes which local ports should be mapped to which remote ports.

## Lifecycle

For the first implementation, forwards should be active only while `isotty attach` is active.

That means:

1. `isotty attach` loads the current project's forward definitions.
2. IsoTTY starts all configured forwards.
3. IsoTTY opens the interactive shell.
4. When the attach session exits, IsoTTY stops all forwards.

This gives a clean mental model:

* no hidden background forwarding by default
* no need to clean up stale listeners later
* forwarding exists only when the user is actively attached

## Configuration Shape

The current recommendation is a map keyed by forward name.

Example:

```yaml
forwards:
  web:
    local_port: 8080
    remote_port: 8080
  vite:
    local_port: 5173
    remote_port: 5173
```

Why map form is preferred:

* names are unique by construction
* `add` and `remove` are simpler
* file updates are less error-prone than list element editing

## Forward Semantics

For the MVP, each forward should mean:

* bind a local TCP port on `127.0.0.1`
* connect it to `127.0.0.1:<remote_port>` inside the isolated environment

That means the remote service is expected to listen on the remote loopback interface or all interfaces inside the environment.

The local bind target should remain loopback-only by default.

This avoids accidentally exposing forwarded development services to the local network.

## Proposed CLI

The first CLI surface should focus on managing the project-local config file.

### Add

```bash
isotty forward add <name> --local-port <local_port> --remote-port <remote_port>
```

Example:

```bash
isotty forward add web --local-port 8080 --remote-port 8080
```

Expected behavior:

* create `./.isotty/forward.yaml` if it does not exist
* add or replace the named forward definition
* fail if the ports are invalid

### List

```bash
isotty forward list
```

Expected behavior:

* read `./.isotty/forward.yaml`
* print configured forward definitions for the current project

### Remove

```bash
isotty forward remove <name>
```

Expected behavior:

* remove the named forward from the current project config
* fail if the name does not exist

## Attach Integration

For the first implementation, `attach` should apply all configured forwards.

That means there is no per-forward enable or disable state yet.

If `forward.yaml` contains:

```yaml
forwards:
  web:
    local_port: 8080
    remote_port: 8080
  vite:
    local_port: 5173
    remote_port: 5173
```

Then `isotty attach` should make both:

* `localhost:8080`
* `localhost:5173`

available for the duration of the attach session.

## Transport Recommendation

For the VM backend, the simplest first transport is SSH local forwarding.

That likely means using the same SSH transport already used for attach, with one `-L` option per configured forward.

This is a good MVP because:

* it is already aligned with the VM backend
* it avoids introducing a second forwarding subsystem
* its lifecycle can match the attach session exactly

## Failure Handling

IsoTTY should fail fast when forwarding cannot be established.

Examples:

* if the local port is already in use, `attach` should fail before opening the shell
* if the remote endpoint is not reachable, `attach` should fail
* if a configured forward is invalid, `attach` should fail with a clear error

IsoTTY should not silently skip broken forward definitions.

## Security Posture

Forwarding should keep a conservative default posture:

* bind local ports to `127.0.0.1` only
* do not expose services on `0.0.0.0` by default
* activate forwards only during `attach`
* avoid keeping hidden listeners alive after the user exits

This does not make the remote service safe by itself.

It does keep the local exposure model narrow and predictable.

## Open Questions

These still need decisions:

* Should `attach` fail if any one forward fails, or should it support partial success later?
* Should the config support `remote_host` in the future, or stay fixed to `127.0.0.1`?
* Should a future `isotty forward start` command support background forwarding without `attach`?
* Should `status` show active forwards while attached?

## Current Recommendation

For the first implementation:

* config file: `./.isotty/forward.yaml`
* config shape: map keyed by forward name
* transport: SSH local forwarding
* local bind target: `127.0.0.1`
* remote target: `127.0.0.1:<remote_port>`
* lifecycle: active only while `isotty attach` is active
* CLI: `forward add`, `forward list`, `forward remove`

This is enough to support the main development workflow without adding a second long-lived subsystem.
