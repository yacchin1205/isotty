# Forward Design

IsoTTY forward exposes services running inside the isolated environment on the local machine, and the host's services to the agent in the environment.

## Config

Project-local forward definitions live in:

```text
./.isotty/forward.yaml
```

Format:

```yaml
forwards:
  web:
    local_port: 8080
    remote_port: 8080
```

## CLI

```bash
isotty host forward add <name> --local-port <port> --remote-port <port>
isotty host forward list
isotty host forward remove <name>
```

## The Host Daemon

The connection that carries the forwards and the host gateway is held by
one daemon per host user, `isotty daemon`, not by a terminal: it belongs to
the VM being up, as the sync does, which mutagen's daemon keeps apart from
any terminal. A terminal that held it would take the agent's relays away
when it closed, and a [wake](WAKE.md), which starts the VM with nobody
there, would need a terminal of its own.

* The daemon serves every project of the host user, as mutagen's daemon
  serves every sync session: one process to keep running, one place to
  look.
* For a project whose VM runs, it holds one ssh connection with the
  project's forwards and the gateway's reverse forward, writes the sync
  report the VM reads, and connects again when the connection drops. For
  one whose VM is stopped, by `isotty stop` or by itself, it waits, and
  starts the VM for a due wake.
* `up` starts the daemon when it is not running, as mutagen's commands
  start mutagen's, and hands it the project; the daemon holds the project
  until its `down`, through any number of stops and starts of the VM.
* On a Mac the daemon is a launchd agent, started at login and again
  whenever it ends, so a wake is served without a command run first.
* `isotty daemon status` lists the projects, how each is held and what went
  wrong last, which no terminal is there to show; the daemon logs to
  `~/.isotty/daemon.log`. `up` and `attach` replace a daemon that
  runs another binary than theirs, a new isotty put in the place of the old
  among them, since nothing else would notice; `isotty daemon restart` does
  so at will, and `isotty daemon stop` ends it.

## Lifecycle

Forwards and the gateway are up while the VM runs, attached or not. The
relays the human agreed to, and wakes, are agreements to the agent using
them unattended; a human who wants the host to stop serving the VM stops
the VM, which is the plain way to say it.

A change to the forwards or to the host's agreements takes effect at the
next `up`, as the VM side of an agreement always did: `up` is the one place
that brings the VM to the configuration. At its end `up` has the daemon
read the project again, and the daemon replaces the project's forwards and
gateway on the same connection.

## Transport

The VM backend uses SSH local forwarding through `gcloud compute ssh`.

Each forward maps:

```text
127.0.0.1:<local_port> -> 127.0.0.1:<remote_port>
```

## Security Posture

The default posture is:

* local bind target is `127.0.0.1`
* forwards and the gateway are up while the VM runs; `isotty stop` takes them down
* invalid forward definitions fail loudly
* a forward that cannot bind is reported by `isotty daemon status` and leaves
  the project's other forwards and the gateway up

The daemon also opens one reverse forward for the host gateway, which serves
host-side services to the VM on port 61001: [ep_weave](EPWEAVE.md) under
`/epweave`, the [GitHub](GITHUB.md), [GitLab](GITLAB.md) and
[Bitbucket](BITBUCKET.md) relays under `/github`, `/gitlab` and
`/bitbucket`, the [Redmine](REDMINE.md) relay under `/redmine`, the
[Google token issuer](GOOGLE.md) under `/google`, the
[AWS credentials issuer](AWS.md) under `/aws`, the relay to the
[SSH hosts](SSH.md) under `/ssh`, the [wake](WAKE.md) requests of the
agents under `/wake` and the browser-open request of [opeco](OPECO.md) at
`/open`. Each relay serves only what its pin allows; one whose synced file
no longer matches its pin is not served, and `isotty daemon status` says so.
