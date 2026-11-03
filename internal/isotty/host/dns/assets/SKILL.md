---
name: isotty-dns
description: Create, change and delete DNS records under this project's names with the isotty-dns command. Use when a task needs a hostname to point somewhere, such as a test deployment that needs an A record or a TXT record for validation.
---

# isotty-dns

This VM holds no cloud credential. DNS records change through a gateway
that `isotty attach` provides on the host; the host calls Cloud DNS with the
human's own login, only under the names pinned in
`/workspace/.isotty/dns.yaml`.

```
isotty-dns status
isotty-dns list [<name>]
isotty-dns set <type> <name> <rrdata>... [--ttl 300]
isotty-dns delete <type> <name>
```

For example:

```
isotty-dns set A app.lab.example.org 203.0.113.10
isotty-dns set TXT _acme-challenge.app.lab.example.org '"token"'
isotty-dns delete A app.lab.example.org
```

`set` creates the record or replaces the one with the same name and type,
as one change; the values you give are the whole record. `list` prints
`name`, `type`, `ttl` and the data, tab-separated.

## What you may change

- Records whose name is one `isotty-dns status` lists or is under it
  (`*.app.lab.example.org` counts as under `lab.example.org`), of any
  type. Another name is refused by the gateway before any credential is
  used; if the task needs it, ask the human to pin it with
  `isotty host dns add`.
- `list` shows only the records under the pinned names; the rest of the
  zone is not yours to see.
- Write the data as DNS expects it: a TXT value in double quotes, a CNAME
  target with its trailing dot.
- The host records every change with the record's values before and after,
  and keeps a note of every record you created so the human can clean up
  after the project. Delete what you no longer need and say in your report
  which records you left.
- Do not edit `/workspace/.isotty/dns.yaml`. It is synced to the host, and
  the host refuses to attach when it differs from what the human pinned.

## Lifecycle: changes go through the attached host

Changes work only while the host is attached. When the human detaches or
their machine sleeps, every command fails within seconds with an explicit
error. Nothing is queued; retry after re-attach, and say so in your report.
