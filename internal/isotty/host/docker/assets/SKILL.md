---
name: isotty-docker
description: Pull and push container images that need a registry login or sit in a private network, with the isotty-docker command. Use whenever `docker pull`, a Dockerfile `FROM` or a compose file needs an image of a repository listed in /workspace/.isotty/docker.yaml, whenever an image is to be pushed to one, or when a plain `docker pull` or `docker push` fails for lack of credentials or of a route to the registry.
---

# isotty-docker

This VM holds no registry credential and may not reach every registry the
human can. For the repositories the human pinned in
`/workspace/.isotty/docker.yaml`, the host's Docker talks to the registry,
with the host's login and from the host's network, and `isotty attach`
carries the image between it and the Docker of this VM.

```
isotty-docker status
isotty-docker pull <registry>/<path>:<tag>
isotty-docker push <registry>/<path>:<tag>
```

Spell the registry and the tag out, as in
`isotty-docker pull docker.io/library/alpine:3.20`; a name without a
registry, a tag left to default and a digest are refused.

## Pulling

After the pull the image is in this VM's Docker under the same name, so
`docker run`, a Dockerfile `FROM` and compose use it as they are. Pull it
before `docker build`: the build itself cannot reach the registry.

## Pushing

Build or tag the image in this VM under the full name it is to have in the
registry, then push that name:

```
docker build -t registry.example.org/team/app:1.4 .
isotty-docker push registry.example.org/team/app:1.4
```

The image pushed is the one for this VM's platform. The command ends with
`Pushed <name> as sha256:...`: that digest is the registry's, and it can
differ from the one `docker image ls --digests` shows here. Use the printed
one wherever the pushed image is referred to by digest, and include it in
your report.

A second push of an image sends only the layers that changed since a push
of the last hours, and pulling an image back that was pushed in the last
hours moves nothing.

Both commands work with the Docker of this VM. When the VM has none, they
say so; tell the human, who enables it on the host with
`isotty runtime service enable docker`.

## What you may reach

- Only the repositories `isotty-docker status` lists: `read` ones for
  pulling, `write` ones for pushing as well. Anything else is refused; ask
  the human to pin it on the host with
  `isotty host docker add --repo <registry>/<path>`, with `--allow write`
  for pushing.
- Public images need none of this: pull them with plain `docker pull`.
- Every push is recorded in the audit log the human reads.
- Do not edit `/workspace/.isotty/docker.yaml`. It is synced to the host, and
  the host refuses to attach when it differs from what the human pinned.

## Lifecycle: the image goes through the attached host

A large image takes a while the first time; the layers that arrived in a
pull of the last hours are not fetched again. When the human detaches or
their machine sleeps, a pull fails with an explicit error and loads nothing;
pulling again fetches what is still missing. A push fails too; when the host
went away in the middle of one, the error says that the registry may or may
not have the image. Retry after re-attach, and say so in your report.
