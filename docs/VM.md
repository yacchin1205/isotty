# VM Design

The first IsoTTY backend is a disposable GCP VM created with `gcloud`.

## User Story

```bash
isotty up
isotty down
```

## Contract

* `up` creates or reuses one VM per project
* `attach` opens an interactive shell with `gcloud compute ssh`
* `id` prints a stable VM target id for remote attach
* `attach --target ...` attaches without local project state
* `down` deletes the VM and removes local state

## Identity

Each project maps to one stable VM name:

```text
isotty-<project-hash>
```

The hash is derived from the absolute project path.

## GCP Context

Project resolution order:

1. `ISOTTY_GCP_PROJECT`
2. active `gcloud` project

Zone resolution order:

1. `ISOTTY_GCP_ZONE`
2. active `gcloud` compute/zone

If either value is missing, IsoTTY should fail.

## VM Shape

Current defaults:

* Ubuntu LTS
* machine type: `e2-standard-4`
* boot disk size: `50GB`
* workspace path: `/workspace`

Project-local VM shape can be configured with:

```text
./.isotty/vm.yaml
```

Example:

```yaml
provider: gcp

gcp:
  machine_type: e2-standard-8
  boot_disk_size: 200GB
  image_family: ubuntu-2404-lts-amd64
  image_project: ubuntu-os-cloud
```

Or update it from the CLI:

```bash
isotty vm gcp show
isotty vm gcp set --machine-type e2-standard-8 --boot-disk-size 200GB
```

## Bootstrap

After VM creation, IsoTTY waits for SSH and bootstraps over SSH.

Bootstrap sets up:

* `/workspace`
* correct ownership for the login user
* project-defined packages from `./.isotty/apt.txt`
* optional Node.js runtime from `./.isotty/node.txt` via NodeSource apt packages
* optional agent CLIs from `./.isotty/agent.yaml`
* optional services from `./.isotty/service.yaml`
* optional post-install script from `/workspace/.isotty/post-install.sh`, executed with `sudo`
* audit support required by IsoTTY

## Local State

IsoTTY keeps local VM state under:

```text
~/.isotty
```

It is written early enough that `isotty down` can still clean up after a failed `up`.

The stored state includes:

* project path
* project hash
* GCP project
* zone
* instance name
* sync mode

## Security Posture

The VM backend assumes:

* no local home directory mount
* no SSH agent forwarding by default
* no automatic credential copying

This is a practical isolation layer, not a high-assurance sandbox.
