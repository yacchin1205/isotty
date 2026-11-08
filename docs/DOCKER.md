# Docker Design

IsoTTY can let the agent in the VM pull and push container images that
need a registry login, or that sit in a network only the host reaches,
without a registry credential in the VM: the host's Docker talks to the
registry, for the repositories the human named, and IsoTTY carries the image
between it and the VM's Docker.

## Problem

`docker login` gives Docker a credential for a whole registry: every
repository the account can read there, and usually push to. Putting it in
the VM puts all of that in the VM's trust domain. A registry behind a VPN
has a second problem: the host is on the VPN and the VM is not.

## Model

```text
agent in VM --isotty-docker pull--> 127.0.0.1:61001/docker ==ssh -R==> host gateway in `isotty attach` --> the host's `docker pull`
                 `docker load` in the VM <-- blobs the VM lacks

agent in VM --isotty-docker push--> 127.0.0.1:61001/docker ==ssh -R==> host gateway in `isotty attach` --> the host's `docker push`
                  `docker save` in the VM --> blobs the host lacks
```

* The relay is one more service of the host gateway described in
  [EPWEAVE.md](EPWEAVE.md), mounted under `/docker`.
* The host's own `docker` talks to the registry. Its login, its credential
  helpers, the certificates it trusts, its insecure-registry settings and
  the networks the host is on are what count. An image the human can pull
  with `docker pull` on the host is one the relay can pull, and a
  repository they can `docker push` to is one it can push to; nothing about
  the registry is configured in IsoTTY.
* IsoTTY's part is the way between the two Dockers, in the archive format
  `docker save` writes and `docker load` reads.
* The crossing is explicit. A plain `docker pull` or `docker push` in the
  VM goes to the registry without a credential, as it would anywhere; the
  agent runs `isotty-docker`, and the skill installed with it says when.

The registry's own protocol is deliberately not relayed. A relay that the
VM's Docker could use as a registry would have to be a registry to that
Docker and a Docker to the real one, with every registry's quirks and the
host's Docker settings reimplemented, and it would stop matching what
`docker pull` and `docker push` do on the host the day either changes.

## Operations

| operation | access | on the host |
|---|---|---|
| pull | read | `docker buildx imagetools inspect --raw <ref>`; when the image is not held, `docker pull --platform <the VM's> <ref>`, `docker save` |
| push | write | `docker load`, `docker push <ref>` |

write includes read.

An image is named `registry/path:tag` with the registry spelled out, as in
`docker.io/library/alpine:3.20`. Docker reads a name without a registry as a
Docker Hub repository under rules of its own, and a pin checked against a
name that Docker then reads differently would be no pin; with the registry
spelled out, the repository IsoTTY checks is the one Docker goes to. A tag
is required because Docker would otherwise choose `latest` by itself, and a
digest is not accepted.

A push sends the image the reference names in the VM's Docker, for the
platform of that Docker. The registry's digest of it is the one the host's
Docker reports, which the VM is told and the audit log keeps; it can differ
from the digest the VM's Docker shows for the same image, because the host's
Docker stores and packs the layers its own way.

Missing on purpose:

* Images of several platforms under one tag. One push is one platform.
* Listing or searching repositories and tags. The agent is told which
  repositories it has; it does not discover them.
* Public images. The VM pulls them itself.

## The host's Docker is a way through

The host's disk is not where images collect. A pull and a push leave the
host's Docker as they found it:

* a reference the host did not have is removed again, and the image with it
  unless something else there names it
* a reference the host had keeps naming the image it named, also when the
  registry has a newer one for the tag, the VM pushes another image under
  it, or the VM's platform differs from the host's
* the host's Docker caches nothing; what is kept of an image is kept beside
  it, described next

Except, with `keep_pushed` in `~/.isotty/docker.yaml`, for the images the
VM pushes: those stay in the host's Docker under their reference. Putting
the host's Docker back after every push is what makes a push of a large
image take minutes though only a layer changed: the archive is loaded whole,
and `docker push` compresses every layer again to learn digests the host's
Docker no longer remembers. With the image kept, the next push of it loads
and compresses what changed. A kept image goes `keep` after its last push,
and with `isotty down`; one whose reference the owner has since moved to
another image is left alone, as that image is theirs. An image the host had
under the reference before the push is left as `docker pull` of a moved tag
would leave it: without the tag.

One operation runs at a time, since each moves references and puts them
back. When putting back fails, the VM already has its answer, so the failure
is reported when the attach ends, with the reference that was left.

## What travels, and what each side keeps of it

An image travels as its blobs: its config and its layers, each named by its
SHA-256, as the archives of Docker 25 and newer name them. Each side keeps
the blobs it received for a while, and asks the other only for those it
lacks. Without that, the host's Docker keeping nothing would mean every
pull fetching the whole image from the registry again and carrying it whole
to the VM, and every push carrying every layer, base image included; a pull
cut half way would start over.

Pull: the host asks the registry which image the tag names now (`docker
buildx imagetools inspect --raw`, with the host's login and without
pulling; `docker manifest inspect` would fetch the image of every platform
behind the tag). When the host holds that image's blobs from an earlier
pull, that is the image; when it does not, or when the registry cannot be
asked that way (no buildx, a registry buildx reaches differently from the
daemon), the host's Docker pulls and saves it, and the host takes the
archive's blobs; the VM is told why. The host tells the VM which blobs the image is made of; the VM's
client fetches those it lacks, checks each against its digest, writes an
archive of its own from the blobs under the name the pull asked for, and has
its Docker load that.

Push: the VM's client reads the archive its Docker saved, asks the host
which of the blobs it does not hold, keeps the blobs on its own side and
sends those. The host checks each against its digest, writes an archive of
its own from the blobs, and has its Docker load and push that. The archive
the host writes names the image by the reference the pin was checked for
and by nothing else, so a name inside the VM's archive never reaches the
host's Docker. Once pushed, the host records the image as it does after a
pull: a pull of the pushed image finds all of it on both sides and moves
nothing.

The blobs are all that either operation leaves behind. On the host:

* they are kept per project, so a VM can only have pushed blobs it sent
  itself and only receives what was pulled for it
* a blob is removed `keep` (four hours unless `~/.isotty/docker.yaml` says
  otherwise) after its last use; an attach removes what has expired when it
  begins and every ten minutes while it is open
* `isotty down` removes the project's blobs
* they live under the OS's temporary directory (`$TMPDIR`, else `/tmp`), in
  `isotty-<uid>/docker/<project-hash>/`, so the OS clears what no attach
  came back for; the `isotty-<uid>` directory is entered by its owner only,
  and a directory of that name that someone else made is refused

In the VM, the client keeps what it received under the same rules and the
host's `keep`, which it asks the gateway for, in `isotty-docker-<uid>/`
under the VM's temporary directory, swept each time it runs. The VM's Docker is not asked what it holds: which blobs it could
spare depends on how it stores images, and an archive it is given to load
must be whole.

A blob is as large as the layer it is. Pulling fills the host's directory
with what was pulled, and a VM that may push can fill it for as long as it
keeps sending; nothing bounds it but `keep`.

## Config

Project config, synced with the rest of `.isotty/`:

```text
./.isotty/docker.yaml
```

```yaml
repos:
  registry.example.org/team/base: read
  registry.example.org/team/app: write
  docker.io/library/alpine: read
```

```bash
isotty host docker add --repo registry.example.org/team/base
isotty host docker add --repo registry.example.org/team/app --allow write
isotty host docker remove --repo registry.example.org/team/base
isotty host docker show
isotty host docker clear
```

`add` and `remove` edit the list. A repository is pinned by its exact name;
there are no patterns. Before pinning, `add` opens a connection from the
host to the registry, on the port in its name or the HTTPS port, so a
mistyped host or a VPN that is not up is refused on the host rather than
found in the VM. That is all it can tell: a repository cannot be asked for
without naming an image in it, so a repository that does not exist, or that
the host's login may not read or write, shows up at the first pull or push,
with what the host's Docker said.

The accepted list is also kept on the host, in
`~/.isotty/projects/<project-hash>.docker.yaml`, and `up` and `attach`
refuse to continue when the synced file differs from it, as described for
GitHub in [GITHUB.md](GITHUB.md#pin).

There is no credential file. The host needs a running Docker that is logged
in to the registries of the pinned repositories.

Host settings, optional and written by hand like `~/.isotty/epweave.yaml`:

```text
~/.isotty/docker.yaml
```

```yaml
keep: 4h            # how long the host keeps what passed through, after its last use
keep_pushed: true   # pushed images stay in the host's Docker for that long too
```

They are one setting for the machine, not per project: whether and how long
images may stay on this disk is the disk owner's call. Without the file,
blobs stay four hours and no image stays. `keep` is a Go duration such as
`4h` or `30m`.

## Audit

Every push is recorded in the VM event log as `docker-push`, with the
repository, the tag and the registry's digest, before the VM is told it
succeeded; if recording fails, the VM is told that the image was pushed but
the audit record was not. Pulls are not recorded.

## VM Side

`isotty up` installs, on every run:

* `/usr/local/bin/isotty-docker`:

  ```
  isotty-docker status
  isotty-docker pull <registry>/<path>:<tag>
  isotty-docker push <registry>/<path>:<tag>
  ```

  `pull` tells the gateway the platform of the VM's Docker, receives the
  blobs it lacks and feeds the image to `docker load`, so it ends up under
  its own name and `docker run`, a Dockerfile `FROM` and compose use it as
  they are. `push` has the VM's Docker save the image of that name to a
  temporary file, sends the blobs the host lacks and prints the registry's
  digest.
* the `isotty-docker` skill for each agent in `agent.yaml`

Docker itself is not part of this: the VM gets it from the project's
`docker` service (`isotty runtime service enable docker`) or any other way
the project installs it. Pinning a repository does not install Docker, and
`isotty-docker` in a VM without it says so and names that command. Both
sides read the archive layout Docker has written since version 25.

## Lifecycle

The relay lives as long as the attach session. A pull or a push while
detached fails within seconds with an error that says so. A pull that
breaks off, when the host sleeps or goes away mid-way, fails with nothing
loaded; the blobs that arrived whole are kept, and pulling again fetches the
rest. A push whose host goes away before answering fails without knowing
whether the registry has the image; pushing again settles it, and sends only
what the host no longer holds.

## Security Posture

* registry credentials never leave the host
* the VM receives images of the pinned repositories only, pushes only to
  the ones pinned with write, and cannot make the host's Docker reach any
  other
* the repository list is pinned on the host; the synced copy cannot widen it
* of what the VM sends, a reference and a platform become arguments of the
  host's `docker`, both checked against a fixed shape first
* a pulled image is content from the registry, handed to the VM blob by
  blob, each checked against its digest on arrival
* a pushed image is content from the VM: the host's Docker stores its
  layers and pushes them, as it would for an image it pulled, and the host
  does not run it; which repository and tag it goes to is the host's
  decision, never the archive's
* every push is in the audit log with the digest the registry has
* the gateway binds to `127.0.0.1` on both ends and lives only while attached
