---
name: isotty-child
description: Create disposable child VMs next to this VM with the isotty-child command, and work on them over SSH. Use when a task needs a test server with a public hostname and HTTPS, or more CPU or memory than this VM has.
---

# isotty-child

This VM holds no cloud credential. Child VMs are created through a gateway
that `isotty attach` provides on the host; the host calls GCP with the
human's own login, within what is pinned in `/workspace/.isotty/children.yaml`.

```
isotty-child status
isotty-child list
isotty-child create <name> --machine-type <type> [--boot-disk-size 100GB] [--expose 80] [--expose 443] [--image-family f --image-project p]
isotty-child ssh <name> [-- <command>]
isotty-child delete <name>
```

For example:

```
isotty-child create web --machine-type e2-standard-8 --expose 80 --expose 443
isotty-child ssh web -- sudo apt-get install -y nginx
isotty-child ssh web
isotty-child delete web
```

## What a child is

- A fresh VM in the same project and zone as this one, with an external IP,
  no cloud credential, and SSH open from this VM only. The key is kept
  under `~/.ssh/isotty-child/` here and nowhere else.
- The ports you `--expose` are open to the whole internet; the rest of the
  child is closed. A public hostname comes from `isotty-dns set A <name>
  <external IP>` when the project has DNS pinned; a certificate from
  certbot on the child, with port 80 exposed for HTTP-01, or with a TXT
  record you set for DNS-01.
- Nothing is synced to it. Copy what it needs with `scp -i
  ~/.ssh/isotty-child/<name>` or through `isotty-child ssh <name> -- ...`.
- It goes when this VM stops or goes down, and with it everything on it.
  Keep results you need under `/workspace` here.

## What you may create

- As many children, of the machine types, with the disk and the open ports
  that `isotty-child status` lists. Anything else is refused by the gateway
  before any credential is used; if the task needs it, ask the human to
  change the pin with `isotty host children enable`.
- The host records every create and delete, with the machine type, image,
  open ports and external IP. What runs on the child is not recorded; say
  in your report what you set up there.
- Delete a child when you are done with it; it costs money while it runs.
- Do not edit `/workspace/.isotty/children.yaml`. It is synced to the host,
  and the host refuses to attach when it differs from what the human pinned.

## Lifecycle: creation goes through the attached host

`create`, `list` and `delete` work only while the host is attached; `ssh`
works whenever the child runs. When the human detaches, the gateway
commands fail within seconds with an explicit error. Nothing is queued.
