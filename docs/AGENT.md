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

## Notifications

An agent that finishes or waits for an answer rings the terminal bell. The
bell character is the one signal that passes ssh and tmux to whatever
terminal the human attached from, where it shows as a badge on the tab, a
bounce or a sound, as that terminal is set. Left to itself each agent
rings nothing in a VM: Claude Code's automatic choice asks the terminal
program, which ssh does not forward, and on Apple Terminal reads the
host's profile, which the VM cannot; Codex rings only while the terminal
is unfocused, which it learns from focus events that tmux does not forward
and Apple Terminal never sends. So `up` sets, for Claude,
`preferredNotifChannel` to `terminal_bell` in `~/.claude/settings.json`,
and for Codex, `notification_method` to `bel` and `notification_condition`
to `always` under `[tui]` in `~/.codex/config.toml`. A key that is already
set is left as it is, so a human who wants another channel sets it once in
the VM and the saved state carries it on.

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

The agent's state at `down` is the state the next VM starts with, including
logins, settings, and the agent's own record of trusting `/workspace`; only
the agent's temporary files and caches are left out. The settings IsoTTY
itself writes into those files are merged again on every `up`, after the
restore. Codex's `config.toml` is read and written as data, so a comment in
it does not live past an `up`.

The state is stored with the project as `.isotty/<agent>.tar.gz.enc`,
encrypted with AES-256-GCM under `~/.isotty/agents.key` (mode 600, created on
first use). It moves and dies with the project, and `ls .isotty` shows it.
Without the key it is ciphertext, so committing or syncing it exposes nothing;
the key never leaves the host. `.isotty/` is synced into the VM, so the VM
sees the archive too: it cannot read or forge it (a modified archive fails to
decrypt), but it can delete it, after which the next `up` asks for a login.

`down` fails when the state cannot be saved, so a login is not lost
silently; `down --force` removes the VM anyway. A VM that has no agent
state at all, because its `up` failed before the restore, leaves the saved
state as it is. Removing an agent with
`isotty runtime agent remove` deletes its saved state.
