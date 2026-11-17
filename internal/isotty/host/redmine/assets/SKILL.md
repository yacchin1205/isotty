---
name: isotty-redmine
description: Read the issues, wiki pages and attachments of this project's Redmine projects with the isotty-redmine command. Use whenever a task refers to a Redmine issue or ticket, a wiki page, or something attached to one.
---

# isotty-redmine

This VM holds no Redmine credential. The Redmine is reached through a
relay that `isotty attach` provides on the host, for the projects pinned
in `/workspace/.isotty/redmine.yaml` only, and for reading only.

```
isotty-redmine status
isotty-redmine issue list [--project <identifier>] [--status open|closed|all] [--limit <n>] [--json]
isotty-redmine issue view <id> [--json]
isotty-redmine issue children <id>
isotty-redmine issue related <id> [--hops <n>]
isotty-redmine wiki list [--project <identifier>]
isotty-redmine wiki view <title> [--project <identifier>] [--json]
isotty-redmine attachment get <id> --issue <issue id> | --wiki <identifier>/<title> [--out <path>]
```

When one project is pinned it is the default; with several, `--project`
names one. `isotty-redmine status` lists them.

## What the host hid

The host filters everything before it reaches this VM:

- The people Redmine itself names (author, assignee, whoever wrote a
  note, the watchers) appear as `Person-` and six hex digits. The same
  pseudonym is the same Redmine user everywhere, so "Person-3f9a2c asked
  Person-18313f to retest" is a real exchange between two people.
- In the text, names are replaced by `[name]`, e-mail addresses by
  `[email]`, phone numbers by `[phone]`, and tokens, keys and other
  secrets by `[secret]`. A `[name]` in the text is not tied to any
  `Person-` pseudonym and may be anyone.

There is no way to learn who anyone is from here, and no reason to try;
refer to people by their pseudonym or as "the person mentioned", and
never guess a name from context. The filter does not catch everything:
names in Latin letters and some Japanese names pass, and so do secrets of
forms it does not know. If you see a real name, an address, a phone
number or a secret, the filter missed it: do not repeat it, do not use
it, and tell the human it slipped through. Every answer is recorded on
the host's behalf in the VM event log.

A PDF or image attachment arrives as the text the host read from it
(OCR for scans and images), filtered like any text, and never as the
file: figures and layout are not there. A text attachment is filtered
like any text. Other attachments are refused, so say so instead of
working around it.

## What you may do

- Read the issues and wiki pages of the pinned projects, and attachments
  listed on them. An issue of another project is refused by number, and
  `issue related` reports such issues instead of following them.
- Nothing is written to the Redmine through this command: no new issue,
  no note, no status change. Ask the human for those.
