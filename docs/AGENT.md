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

IsoTTY does not copy or manage agent credentials.

The goal is to make the CLI available in the VM. Authentication remains a separate step inside the isolated environment.
