---
name: isotty-sync
description: Tell whether /workspace has finished syncing from the host, and wait for it, with the isotty-sync command. Use when a prompt says /workspace is not fully synced, or when files you expect in /workspace are missing.
---

# isotty-sync

`/workspace` is synced from the human's machine. Right after `isotty up` it
may still be empty or incomplete: the files arrive all at once when the
first sync is done.

```
isotty-sync status   # how it stands
isotty-sync wait     # returns once the first sync is done
```

While the sync is not done, each prompt carries a line saying so. Do not
conclude a file is missing, and do not create one in its place, before
`isotty-sync wait` has returned. `wait` keeps waiting while the host is
asleep or offline, or its isotty daemon is not running, and says so; if your
command runner times out, run it again.
