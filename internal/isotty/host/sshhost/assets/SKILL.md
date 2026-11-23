---
name: isotty-ssh
description: Log in over SSH to the machines on the human's own network that the human named, such as a Raspberry Pi on their LAN, with plain ssh, scp and rsync. Use when a task is about such a machine; `isotty-ssh list` names them.
---

# isotty-ssh

This VM is not on the human's network. The machines on it that the human
named are reached through a gateway that `isotty attach` provides on the
host, and only while the host is attached. Each has a host name of its own
here, with its account, key and host key set up:

```
isotty-ssh list            # the machines this project may log in to
ssh pi                     # a shell on the machine named pi
ssh pi uname -a            # one command
scp build.tar.gz pi:/tmp/
rsync -a dist/ pi:app/
```

Use the name as it is; the account, the key and the address are in
`/etc/ssh/ssh_config.d/isotty-hosts.conf`. The host key was accepted by the
human and is checked strictly: when `ssh` reports a changed host key, do not
work around it; the address may now be another machine. Tell the human.

The machine is the human's, on their network, and what you do there is in
your hands as on this VM. The audit log the human reads records when you
were connected to which machine and how much went each way.

When the gateway is not reachable, the host is not attached; say so in your
report instead of retrying.
