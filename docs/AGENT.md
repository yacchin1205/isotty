# Agent Design

IsoTTY can install coding agent CLIs during `up`.

## Config

Project-local agent configuration lives in:

```text
./.isotty/agent.yaml
```

Example:

```yaml
agents:
  codex: {}
  claude: {}
```

Node.js version is configured separately:

```text
./.isotty/node.txt
```

Example:

```text
22
```

`node.txt` must contain only a Node.js major version.

`node.txt` exists because Node.js is a general runtime, not just an implementation detail of one agent.

## First Agents

The first supported agents are:

* `codex`
* `claude`

## Install Timing

Agent installation happens during `isotty up`.

IsoTTY prepares the required Node.js runtime first, then installs the configured agent CLIs in the VM.
Node.js is installed through the NodeSource apt repository.

## Credentials

IsoTTY never copies credentials from the host into the VM. Authentication
happens inside the isolated environment, once per project.

A login made in the VM would die with the VM, so `isotty down` saves each
configured agent's home state and `isotty up` puts it back when it creates
a new VM:

| agent | saved paths |
|---|---|
| `claude` | `~/.claude`, `~/.claude.json` |
| `codex` | `~/.codex` |

Nothing is selected or filtered: the agent's state at `down` is the state
the next VM starts with, including logins, settings, and the agent's own
record of trusting `/workspace`. The settings IsoTTY itself writes into those
files are merged again on every `up`, after the restore.

The state is stored per project on the host as
`~/.isotty/projects/<project-hash>.<agent>.tar.gz.enc`, encrypted with
AES-256-GCM under `~/.isotty/agents.key` (mode 600, created on first use).
Without the key the saved state is unreadable. Projects never share state.

`down` fails when the state cannot be saved, so a login is not lost
silently; `down --force` removes the VM anyway. Removing an agent with
`isotty runtime agent remove` deletes its saved state.
