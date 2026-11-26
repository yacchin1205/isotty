---
name: isotty-wake
description: Stop working and be woken later in this same conversation with the isotty-wake command, while the VM powers off in between. Use when work has to wait for something that takes a while, a CI run, a review, a time of day, and you would otherwise sit idle or poll.
---

# isotty-wake

```
isotty-wake in <after> "<prompt>"     # after: 30m, 1h, 2d
isotty-wake every "<cron>" "<prompt>"  # five fields, the host's local time
isotty-wake list
isotty-wake cancel <id>
```

`isotty-wake in 1h "Check whether CI on PR 12 passed and go on."` asks the
host to resume this conversation with that prompt an hour from now. Then
end your turn. With no agent work left the VM powers itself off, and the
host starts it again at that time and gives you the prompt, in this
conversation: what you knew is still there, but processes you started, a
server or a tail, are not.

Write the prompt for yourself as you will be then: what to check and what
to do next.

For work that comes back, a morning check say, ask once with `every`
instead of asking for the next wake each time: `isotty-wake every
"0 6 * * *" "<prompt>"` wakes this conversation at 06:00 every day until
you cancel it. A time at which the conversation still has work is skipped.

The human allows wakes project by project with `isotty schedule enable` on
the host; without it the wake is refused and says so, and you should tell
the human rather than wait. The host must be awake at the time: a wake
whose time passed while it slept runs once it wakes. Asking needs the host,
so it works only while the host is reachable.

Your own scheduled prompts (CronCreate, `/loop`) keep the VM running until
they fire; use them for minutes, and isotty-wake for longer.
