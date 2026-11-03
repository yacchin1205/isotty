# DNS Design

IsoTTY can let the agent in the VM create, change and delete DNS records at
the names the human pinned, through a relay in the host gateway that uses
the host's gcloud login.

## Problem

A test deployment needs a hostname: an A record for the service, a TXT
record for a validation. The agent in the VM can run `gcloud dns` only with
a credential, and the credential at hand, the human's `gcloud auth login`,
reaches every record of the zone and every project the human can touch. The
zone is shared with other uses, so a mistake is not contained, and the audit
log shows the command line, not what the records were before and after.
When the deployment is gone, the human has to remember which records it
left.

Cloud DNS can restrict a credential to a zone, not to names inside it, so
the agreement has to be enforced by IsoTTY.

## Model

```text
agent in VM --isotty-dns--> 127.0.0.1:61001/dns ==ssh -R==> host gateway in `isotty attach` --> Cloud DNS
```

* The relay is one service of the host gateway, mounted under `/dns`.
* The host calls the Cloud DNS API with the token of its active gcloud
  account (`gcloud auth print-access-token`), renewed before gcloud's hour
  runs out. No key is imported and no credential enters the VM.
* The agreement is a list of record names per managed zone. A pinned name
  covers itself and every name under it, wildcard labels included, so
  `*.app.lab.example.org` is under `lab.example.org`. A request outside
  it is refused before any credential is used, with the agreement in the
  answer. The record type is part of the ask, not of the agreement, as a
  function is in the Apps Script relay: a mistake stays under the name
  whatever the type, and the audit log carries the type.
* `list` returns only the records under the pinned names; the rest of the
  zone is not shown to the VM.
* `set` creates a record or replaces the one with the same name and type,
  as one Cloud DNS change that deletes the old record set and adds the new
  one. `delete` removes the record set.

## Config

Project config, synced with the rest of `.isotty/`:

```text
./.isotty/dns.yaml
```

```yaml
provider: clouddns
zones:
  - project: example-project
    managed_zone: test-zone
    names:
      - lab.example.org
```

```bash
isotty host dns add --project example-project --name lab.example.org
isotty host dns show
isotty host dns remove --name lab.example.org
```

`add` lists the project's managed zones with the host login and pins each
name in the zone that serves it, the one whose domain is the longest suffix
of the name. A project the login cannot reach, or a name no zone serves, is
refused on the host. When two zones serve the same domain, a public and a
private one, `--managed-zone` names the one meant. The provider is one
per project; a
project whose zones span providers is not something IsoTTY sets out to
serve. Only `clouddns` exists, so `add` sets it; another provider would get
its own `isotty host dns login`-style step for the host credential it needs.

### Pin

`.isotty/` is synced two-way with `/workspace`, so an agent in the VM could
rewrite `dns.yaml` and widen its names. `isotty host dns add` and `remove`
therefore also record the accepted agreement on the host:

```text
~/.isotty/projects/<project-hash>.dns.yaml
```

`up`, `attach` and `attach --target` compare the synced file with the pin
and refuse to continue when they differ or when no pin exists. Accepting a
change means running `isotty host dns add` or `remove` on the host again.

## VM Side

`isotty up` installs, on every run:

* `/usr/local/bin/isotty-dns`, a Python client with `status`, `list`,
  `set` and `delete`
* the sshd keepalive the other gateway services rely on
* the `isotty-dns` skill for each agent in `agent.yaml`

## Audit

Every change the VM asked for is recorded in the VM event log
(`/var/log/isotty/events.jsonl`) as `dns-set` or `dns-delete`, with the
zone, the name, the type, the record's values before and after (`<ttl>
<data>...`, empty when the record did not exist) and the result: the
change's status from Cloud DNS, the API's refusal, or `refused` for a
request outside the agreement, which is recorded too.

## Cleanup

Every record the VM created, as opposed to replaced, is noted on the host:

```text
~/.isotty/projects/<project-hash>.dns-records.yaml
```

`isotty host dns show` reads those names back from the zones and lists the
ones that still exist, so a record the human removed by hand is not
reported. `isotty down` prints the same list as a warning; whether they go
is the human's decision, and the note outlives the environment so `show`
still answers after `down`.

## Lifecycle

The relay lives exactly as long as the attach session, like the other
services. Nothing is queued: a change either reached Cloud DNS while the
host was attached, or the client failed within seconds and says so.

## Security Posture

* the host's gcloud login never leaves the host; the VM sees an HTTP
  endpoint that accepts four operations
* the VM can change only records under the pinned names, and sees only
  those
* the pin is on the host; the synced copy cannot widen it
* every change carries its before and after in the audit log, and every
  record the VM created is on a list the human can check
* Cloud DNS enforces nothing of this: the login is as wide as the human's;
  the agreement is IsoTTY's
