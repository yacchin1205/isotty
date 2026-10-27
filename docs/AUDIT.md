# Audit Design

IsoTTY audit is for answering three questions:

* what executed in the isolated environment
* what it tried to connect to
* when it happened

## Source Of Truth

The source of truth is the Linux Audit Subsystem with `auditd`.

IsoTTY should treat `auditd` records as the primary audit log.

## Event Scope

The first implementation should collect:

* `execve`
* `connect`

These are enough to observe risky development activity such as package installs, setup scripts, and agent execution.

## CLI

The first command shape is:

```bash
isotty audit logs
isotty audit logs -f
```

`logs` is an audit namespace entry point, not a promise that only one log source exists.

## Formatting

Formatting should happen on the local side.

The VM should collect audit records and expose them through standard tooling.
IsoTTY should fetch those records and render them for humans.

If IsoTTY cannot fully interpret a record, it should not silently drop it.
It should either:

* render the partial record explicitly
* or fail clearly

## Auxiliary Events

IsoTTY operations such as `attach` are useful context, but they are not the source of truth.

Those should be recorded separately as auxiliary VM-side events, for example in an `events.jsonl` file.

That auxiliary log may annotate the audit timeline, but it must not replace `auditd`.

## Rules

IsoTTY should install dedicated audit rules with clear keys:

* `isotty-exec`
* `isotty-connect`

This keeps collection narrow enough to query while preserving the original audit records.

## Failure Handling

Audit collection must fail loudly.

IsoTTY should not silently degrade to "no audit data" when:

* `auditd` is missing
* audit rules are missing
* record parsing fails
* required privileges are missing

The only normal empty result is "no matching events were found". To tell
that apart from a stopped `auditd` or a removed rule, `isotty audit logs`
checks that `auditd` is active and both rules are loaded before it queries,
and fails when they are not.

## Archive

The VM is deleted by `down`, and the log with it, so `down` first takes an
archive, and `isotty audit archive` takes one at any time:

```text
.isotty/audit/<UTC time>.tar.gz.enc
```

It holds, under one directory named by the time, `exec.txt` and
`connect.txt` (`ausearch -i` of each rule over the whole log), `events.jsonl`
and, under `raw/`, the auditd log files themselves. The log is taken as it
is: auditd need not be running, and the archive says nothing about gaps.
`down` fails when the archive cannot be taken, as it does for agent logins;
`down --force` deletes the VM without it.

The archive is sealed on the host with the key that seals agent logins,
`~/.isotty/agents.key`, as everything IsoTTY takes out of the VM is: the
command lines in `exec.txt` carry whatever the agent typed, tokens
included, and the project directory is backed up and shared. The stream is
AES-256-GCM in chunks, so an archive of any size is sealed and opened
without holding it in memory, and a truncated or altered file is refused.
`.isotty/audit/` is left out of the sync.

Sealing is about where the file rests, not about who may read it. The next
`up` opens each archive and unpacks its rendered files into the VM:

```text
/var/log/isotty/archive/<UTC time>/{exec.txt,connect.txt,events.jsonl}
```

readable by everyone there, next to the live log, with an `isotty-audit`
skill that tells the agents the records are theirs to read. `raw/` stays in
the archive. On the host, `isotty audit logs --archive <file>` renders an
archive as `audit logs` renders a running VM.

The archive is a copy of what the VM held at that moment, not evidence
against the VM: its user has `sudo`, so the log could have been changed
before `down`. Keeping a trail the VM cannot touch would mean streaming it
to the host during `attach`, which this does not do.
