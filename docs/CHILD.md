# Child VM Design

IsoTTY can let the agent in the VM create VMs next to it, within the size
the human agreed to, through a relay in the host gateway that uses the
host's gcloud login. A child is reachable over SSH from its parent only and
goes when the parent stops or goes down.

## Problem

Some work does not fit the project's VM: a test server that needs a public
hostname and HTTPS, or a job that needs more CPU or memory than the VM has.
The agent could create a VM only with a credential, and a credential in the
VM is what IsoTTY exists to avoid. A VM made by hand outside IsoTTY has no
agreement around it, no record, and nobody deletes it.

## Model

```text
agent in VM --isotty-child--> 127.0.0.1:61001/child ==ssh -R==> host gateway in `isotty attach` --> gcloud (host login)
```

* The relay is one service of the host gateway, mounted under `/child`.
  It creates instances with the host's gcloud, as `isotty up` does for the
  parent: same project and zone, no service account, no scopes.
* A child is named after its parent, `<parent>-<name>`, carries the labels
  `isotty_parent` and `isotty_child`, and `isotty list` shows it as
  `child of <parent>`.
* The agreement is the shape, not the use: how many children at once, which
  machine types, how large a boot disk, which ports open to the world. What
  runs on a child is the agent's business, as a function is the agent's in
  the Apps Script relay. A request outside the agreement is refused before
  any credential is used.

### Reach

* SSH: the relay makes an ed25519 key pair per child, puts the public key
  in the child's metadata (`enable-oslogin=false`, so the key is honoured)
  and hands the private key to the parent, which keeps it under
  `~/.ssh/isotty-child/`. The host keeps no key.
* Firewall rules named after the child, on the parent's network and bound
  to the child's network tag: TCP 22 from the parent's internal IP is
  allowed (priority 800), the exposed ports from `0.0.0.0/0` are allowed
  (850), and everything else from everywhere is denied (900). The deny rule
  is what keeps a default network's own rules, `default-allow-ssh` and
  `default-allow-internal` among them, from reaching the child; they rank
  below it. Rules of a higher rank than 900, and hierarchical firewall
  policies, are the project's and win; a rule admitting SSH from an office
  range or from IAP at priority 100 admits it to the child too.
* The world: the child has an ephemeral external IP, needed for apt and
  for a certificate. Egress is not restricted, as for the parent.
* The rules are created before the instance and deleted after it; a
  create that fails removes them again, and `stop` and `down` remove the
  rules of any child whose instance is already gone.

## Config

Project config, synced with the rest of `.isotty/`:

```text
./.isotty/children.yaml
```

```yaml
max: 2
machine_types:
  - e2-standard-8
  - n2-highmem-4
max_boot_disk_size: 200GB
expose:
  - 80
  - 443
```

```bash
isotty host children enable
isotty host children enable --machine-type e2-standard-8 --machine-type n2-highmem-4 --max 2 --boot-disk-size 200GB --expose 80 --expose 443
isotty host children show
isotty host children disable
```

`enable` writes the whole agreement; running it again replaces it. Without
flags a child may be what the project's own VM is, its machine type and
disk size from `vm.yaml` or the defaults, one at a time, with 80 and 443
open. A child
starts from the image and disk size a VM without `vm.yaml` gets, unless the
request names others. The image is part of the ask, not of the agreement:
gcloud resolves `image_project` with the host's login, so any image the
human can read is allowed, their own project's included, and a premium
image carries its licence fee on top of the machine type.

### Pin

`.isotty/` is synced two-way with `/workspace`, so an agent in the VM could
rewrite `children.yaml` and widen it. `isotty host children enable`
therefore also records the accepted agreement on the host:

```text
~/.isotty/projects/<project-hash>.children.yaml
```

`up`, `attach` and `attach --target` compare the synced file with the pin
and refuse to continue when they differ or when no pin exists.

## VM Side

`isotty up` installs, on every run:

* `/usr/local/bin/isotty-child`, a Python client with `status`, `list`,
  `create`, `ssh` and `delete`; `create` keeps the key, `ssh` uses it
* the sshd keepalive the other gateway services rely on
* the `isotty-child` skill for each agent in `agent.yaml`

Nothing is synced to a child; the agent copies what it needs over SSH.

## Lifecycle

A child exists for the parent's run. `isotty stop` and `isotty down` delete
every child of the VM, by label, before touching the parent, whatever state
the parent is in, and `isotty up` remembers nothing about them: a child that
is wanted again is created again. A parent stopped by other means keeps its
children until the next `isotty stop` or `down`. The agent can delete one at
any time with `isotty-child delete`.

The relay itself lives as long as the attach session; `isotty-child ssh`
works whenever the child runs.

## Audit

Every create and delete is recorded in the VM event log
(`/var/log/isotty/events.jsonl`) as `child-create` or `child-delete`, with
the name, the instance, the shape (machine type, disk, image), the ports
opened and the external IP, and the result; a refused request is recorded
too. `stop` and `down` record the deletions they make while the parent can
still take the record, and `down` does so before it archives the log. A
create whose record fails answers with an error and the key is not handed
over; the child exists and `isotty-child delete` removes it. What runs on a
child is not recorded: it is inside the agreement, on a VM that goes with
the parent.

## Security Posture

* the host's gcloud login never leaves the host; the child has no
  credential either
* a child's SSH is reachable from its parent only, as far as the project's
  own firewall rules rank below the child's; the relay's key exists on the
  parent and nowhere else, while a key the project's metadata carries for
  every instance is honoured too, within the same rules
* `enable-oslogin=false` in the instance metadata overrides the project's
  OS Login setting but not the organization policy
  `constraints/compute.requireOsLogin`; under it the child comes up and the
  parent's SSH is refused
* the first SSH to a child trusts its host key; it is filed under the
  instance name, so a later child at the same internal IP is told apart
* the ports the agreement allows are the only ones open to the world, and
  the only ones open at all besides SSH from the parent
* the agreement bounds the count and the shape, not the image
* children are found by label, so `stop` and `down` delete them even after
  `isotty host children disable`
