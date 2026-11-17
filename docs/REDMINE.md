# Redmine Design

IsoTTY can let the agent in the VM read a Redmine: the issues and wiki
pages of the projects the human named, filtered on the host before they
reach the VM. The relay lives in the host gateway, as the
[GitHub](GITHUB.md) one does; the credential and the filter stay on the
host. This document covers the model, the filter and what the VM sees.
Writing to Redmine is not part of it.

## Problem

The agent needs the issues to do its work, but a Redmine also holds what
the work does not need and what must not enter the VM: the names of the
people who wrote, were assigned or are mentioned, their addresses and
phone numbers, and the tokens and keys someone pasted into a note.
Whatever enters the VM can leave it, through the transcript, what the AI
provider receives, and what the agent writes (commits, notes, pads), and
the agent can act on it: a token in hand is a token it can use. People
are needed only as far as telling them apart; secrets are not needed at
all.

No filter of free text finds everything. The filter below is a set of
choices about what to catch and what to let through, made by the human;
what it hands the VM is recorded so that a human can find later what got
through.

## Model

```text
agent in VM --isotty-redmine--> 127.0.0.1:61001/redmine ==ssh -R==> host gateway in `isotty attach` --> Redmine
                                                                        |
                                                                        +--> poppler, tesseract (attachments, on the host)
```

* The relay is mounted under `/redmine`. It serves a fixed set of reads
  on the pinned projects and nothing else; there is no write.
* Every answer passes the filter before it leaves the host, and is
  recorded in the VM event log before the VM gets it. An attachment
  reaches the VM as its filtered text, never as the file.
* The pin names projects by their Redmine identifier (the slug in the
  URL). Issues are reached by number, as Redmine numbers them across
  projects, so the relay reads the issue first and answers only when its
  project is pinned.

## Credential

```text
~/.isotty/redmine.yaml
```

```yaml
url: https://redmine.example.org
api_key: ...
```

`isotty host redmine login <url>` asks for the API key without echo,
confirms it against `/users/current.json` and writes `url` and
`api_key`. The key is the human's own
Redmine API key, found under "My account"; the relay acts as the human and
sees what the human sees, which is why the pin narrows it to projects.

## Access

| operation | API calls |
|---|---|
| project view | `GET /projects/{identifier}.json` |
| issue list | `GET /issues.json?project_id={identifier}&subproject_id=!*` with `status_id`, `tracker_id`, `sort`, `limit`, `offset` |
| issue view | `GET /issues/{id}.json?include=children,relations,journals,attachments,watchers` |
| wiki list / view | `GET /projects/{identifier}/wiki/index.json`, `GET /projects/{identifier}/wiki/{title}.json?include=attachments` |
| attachment | `GET /attachments/download/{id}/{filename}`, reached through the issue or wiki page that lists it |

* Subprojects are left out of the list: `project_id` alone would include
  their issues, and they are projects of their own, pinned or not.
* An issue is answered only when its `project.id` is one of the pinned
  projects, whose ids the relay reads when `attach` starts. Children and
  relations are followed by the client issue by issue, so a related issue
  in another project is refused at that step, not leaked with its parent.
* An attachment is reached as `/issues/{id}/attachments/{aid}/download`
  or the wiki counterpart: the relay reads the issue or page, checks the
  pin and that the attachment is listed there, and only then downloads
  it. There is no `/attachments/{aid}` route, because the attachment
  itself does not say which project it belongs to.

## Filter

The filter runs on the host, on every string of every answer and on the
text of every attachment, in this order:

1. **Text.** A text attachment is taken as it is. A PDF or an image is
   turned into text on the host: `pdftotext` reads the text layer of each
   PDF page, and a page without one is rendered by `pdftoppm` and read by
   `tesseract`, as an image is. Any other type is refused, since the
   filter cannot read it, and the file itself never reaches the VM. The
   host needs poppler (with `poppler-data` for Japanese fonts) and
   tesseract with the `jpn` model, for example
   `brew install poppler tesseract tesseract-lang`.
2. **Patterns.** E-mail addresses and Japanese phone numbers become
   `[email]` and `[phone]`. The phone pattern takes the forms people
   write: hyphens, spaces or parentheses between the groups, ten or
   eleven digits without separators, and `+81`. Secrets, by the default
   rules of [gitleaks](https://github.com/gitleaks/gitleaks) built in,
   become `[secret]`.
3. **People.** A person field, any `author`, `assigned_to`, `user` or
   watcher object (`{id, name}`), has its `name` replaced by
   `Person-` and six hex digits of an HMAC-SHA256 of the Redmine user id,
   keyed with the project's salt, so the same user is the same pseudonym
   across answers and attaches. A person field without an id fails the
   answer. In text, every word the morphological analyser
   ([kagome](https://github.com/ikawaha/kagome) with the IPA dictionary)
   tags as a person's name becomes `[name]`.

Why it is built this way:

* Names in text are not looked up in a dictionary of the project's
  members and the people seen so far: what such a dictionary holds
  depends on what was read before, so the same text would come out
  differently and a miss could not be reproduced.
* Names in text are hidden, not given pseudonyms: they cannot be tied to
  a user id, and a pseudonym of their own would read as someone else.
* No language model judges the candidates: tried with context on
  synthetic Japanese documents, it called most words that are not names
  names as well, and the human judged it of no use for telling them
  apart.

What gets through, and has to be looked for in the record: a name the
analyser does not tag as a person's name. That covers names written in
Latin letters (`Taro Yamada`), names the IPA dictionary takes for a
place, an organisation or a common word (山口, 清水, 馬場), rare given
names it splits into pieces, and names whose tag the surrounding words
change (in 「山田 太郎 が」 the given name is tagged as a place). On
Nemotron-PII-Japan, a synthetic set of Japanese documents, about half of
the annotated names were not tagged as names. Secrets that none of the
gitleaks rules describe get through as well, a plain `password = ...`
among them.

Errors name an attachment by its id, not its file name, since the file
name has not been through the filter.

## Audit

Every answer is recorded in the VM event log
(`/var/log/isotty/events.jsonl`) as `redmine-read`, before the VM gets
it: the read the VM asked for, the content the VM receives, and every
replacement in it (the field as a JSON path, the characters of the
replacement, and its kind: `person`, `email`, `phone`, `secret` or
`name`). Nothing from before the filter is recorded; a human who wants to
know what was replaced reads it in the Redmine at the place the record
gives. The content can outgrow a command-line argument, so the record
reaches the VM over stdin. When it cannot be written, the answer is
withheld.

## Config

Project config, synced with the rest of `.isotty/`:

```text
./.isotty/redmine.yaml
```

```yaml
projects:
  - infra
  - helpdesk
```

```bash
isotty host redmine login https://redmine.example.org
isotty host redmine add --project infra --project helpdesk
isotty host redmine remove --project helpdesk
isotty host redmine show
isotty host redmine clear
```

`add` reads each project with the key before pinning, so a project the
key cannot see is refused on the host. The pin is
`~/.isotty/projects/<project-hash>.redmine.yaml`, and `attach` serves the
relay only when the synced file matches it, as for the other relays.

## VM Side

`isotty up` installs `/usr/local/bin/isotty-redmine` and the
`isotty-redmine` skill for each configured agent:

```
isotty-redmine status
isotty-redmine issue list [--project <identifier>] [--status open|closed|all] [--limit <n>] [--json]
isotty-redmine issue view <id> [--json]
isotty-redmine issue children <id>
isotty-redmine issue related <id> [--hops <n>]
isotty-redmine wiki list [--project <identifier>]
isotty-redmine wiki view <title> [--project <identifier>]
isotty-redmine attachment get <id> --issue <issue id> | --wiki <identifier>/<title> [--out <path>]
```

When one project is pinned it is the default; with several, `--project`
names one. The skill tells the agent what the pseudonyms and the marks
are, and that the filter misses some names, so a name it sees is one that
got through: not to be repeated, and to be reported to the human.
