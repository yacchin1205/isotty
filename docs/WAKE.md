# Wake Design

An agent in the VM can stop working and be woken later in the same
conversation, while the VM is off in between: it asks the host for a wake,
ends its turn, and the VM powers itself off ([Idle Stop](VM.md#idle-stop));
at the time the host starts the VM, its daemon holds the gateway as for
any running VM, and gives the conversation the prompt the agent left for
itself.

## Problem

Work often waits: for CI, a review, a time of day. Claude Code's own
schedules, `/loop` and CronCreate, fire only inside a running session, so
they keep the VM up, and paid for, the whole wait. Their cloud and desktop
counterparts, and Codex's, run outside the VM, which would undo what the VM
is for.

The VM cannot start itself, and while it is off nothing holds the gateway,
so a VM a schedule on GCP started would have none of the relays the human
agreed to. So the host starts it, and holds the gateway while it runs.

## Model

```text
VM:    agent  --isotty-wake in 1h "<prompt>"-->  gateway /wake  --> host ~/.isotty/projects/<hash>.wakes.json
       agent ends its turn; no work for 30 minutes; the VM powers off
host:  isotty daemon at the time:
         VM off?  start it as `up` does; the daemon holds its gateway as for any running VM
         in the VM: the conversation's agent still runs?  type the prompt into its tmux pane
                    otherwise                            the agent resumed with the prompt in a new tmux window
       the agent works; no work for 30 minutes; the VM powers off again
```

* The wake names the conversation, which `isotty-wake` finds by walking up
  from its own process to the agent whose session the idle hooks recorded.
  Resuming it keeps what the agent knew; the processes it ran, a server or
  a tail, are gone with the power-off.
* The wake is kept on the host, never in the VM: only the host is there
  while the VM is off.
* The prompt is given to the agent as a human's message would be, in its
  interactive session, not to a headless run. The idle hooks then see the
  work as for any session, an approval the agent waits for keeps the VM up
  until a human answers, and a human who attaches later finds the
  conversation.
* A wake is one time, or recurring: `isotty-wake every "0 6 * * *"
  "<prompt>"` wakes the conversation at each time five cron fields name, in
  the host's local time, as Claude Code's own CronCreate takes them. An
  agent could ask for its next wake each time it is woken, but one turn
  that fails to ask ends the chain unseen; a recurring wake comes back
  whatever the last time did.
* No wake is typed into a conversation that still has work or waits for an
  approval: the keys would cut into the work, or answer the approval in the
  human's place. A recurring wake skips that time; a one-time one waits ten
  minutes. A time the host slept through is run once when it wakes, and the
  times before it are dropped.
* A wake that cannot be delivered is told to the human on the card of the
  VM's [opeco session](OPECO.md#keeping-the-session), when the VM can be
  reached, and in the daemon's log. A one-time wake is tried again ten
  minutes later, three times at most, so one that never can be delivered
  does not start the VM for ever; a recurring one waits for its next time.
* `isotty-wake cancel <id>` forgets a wake; `isotty schedule disable` on the
  host forgets them all and refuses new ones, which is how the human stops
  an agent's wakes. `isotty down` forgets them too: the conversations they
  would resume go with the VM.
* A prompt is at most 8 KiB, since it goes to the VM in one command.
* Claude Code and Codex are woken alike, each with its own way to resume a
  conversation with a first message: `claude --resume <id> "<prompt>"` and
  `codex resume --no-daemon <id> "<prompt>"`. Codex runs without its
  shared background server, which went with the power-off and leaves state
  a new one trips on.
* A conversation is known by the session its agent's hooks recorded, so a
  Codex session whose hooks the human has not yet reviewed with `/hooks`
  cannot ask for a wake; `isotty-wake` says so.

## Consent

Starting a VM costs the human, so the human allows wakes project by project
on the host:

```bash
isotty schedule enable     # in the project directory
isotty schedule disable
isotty schedule show
```

`enable` records the consent in `~/.isotty/projects/<hash>.schedule.yaml`,
which is never synced. The gateway refuses the wakes of a project without
it, saying who can allow them, so the agent tells the human instead of
waiting. `disable` withdraws it and forgets the project's wakes. Within a
project that allowed them, a wake is the agent's own: it is kept as asked.

## The Runner

The [host daemon](FORWARD.md#the-host-daemon) serves the wakes of every
project that allows them: for a due one it starts the VM as `up` does,
which then has its forwards and gateway as any running VM, and gives the
conversation the prompt. There is no runner apart from it.

The daemon works only while the host user is logged in and the host is
awake. A wake whose time passed meanwhile runs when the daemon next looks.
A wake that fails is tried again ten minutes later rather than at every
look, so a VM that cannot start is not started over and over.

## Audit

The VM event log records each wake asked for (`wake-request`), cancelled
(`wake-cancel`) and delivered, skipped or postponed (`wake`), with the
conversation, the time and the prompt, and the power-off between them as
`idle-stop`.

## Security Posture

* the VM is started for a wake only in a project whose human allowed wakes
  on the host
* the relays the woken agent reaches are the ones the project pins, served
  while the VM runs as for any running VM
* the agent works within its own settings; a wake loosens nothing
