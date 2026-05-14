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

The only normal empty result is "no matching events were found".
