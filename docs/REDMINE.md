# Redmine Design

The agent in the VM reads the pinned Redmine projects through a relay in
the host gateway, read only, with the answers filtered on the host.

## Purpose

What enters the VM can leave it, through the transcript, what the AI
provider receives, and what the agent writes, and the agent can act on
it. The agent needs the issues to work, but not who the people are, only
how to tell them apart, and not the secrets someone pasted into a note.
So people are given pseudonyms or hidden, and contact details and
secrets are hidden.

No filter of free text finds everything. What to catch is the human's
choice, and every answer is recorded so that a human can find later what
got through.

## Decisions

* What the filter hides: names, contact details (e-mail addresses and
  phone numbers) and secrets. Addresses, dates of birth, account numbers
  and the like are not its concern.
* Names in text are not looked up in a dictionary of the project's
  members and the people seen so far: what such a dictionary holds
  depends on what was read before, so the same text would come out
  differently and a miss could not be reproduced.
* Names in text are hidden, not given pseudonyms: they cannot be tied to
  a user id, and a pseudonym of their own would read as someone else.
* No language model judges whether a word is a name: given context, it
  called most words that are not names names as well, and the human
  judged it of no use for telling them apart.
* What the answer does not show to be personal passes as it is, the
  values of a user-type custom field (user ids) among them: the filter
  cannot know it.
* The record carries what the VM received and where it was changed, never
  what was there before: a copy of the original would be a serious
  problem, and the place in the Redmine is enough to look it up.

The record is the VM's own account, as the rest of the VM event log is
(see [AUDIT.md](AUDIT.md)).
