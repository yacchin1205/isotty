# SSH Host Design

IsoTTY can let the agent in the VM log in over SSH to a machine on the
host's network, a Raspberry Pi on the human's LAN for one, which the VM on
GCP cannot reach by itself. The human names the machine once; the agent
then runs `ssh pi`, `scp` and `rsync` as on any machine.

## Problem

The VM is not on the host's network, and the human's own SSH keys must
not enter the VM: they open every machine the human can log in to. A
port forward of the machine's SSH port alone would leave the agent
without a key and without a way to tell the machine it reaches from
another one that took its address, which on a LAN with DHCP is a matter
of time.

## Model

```text
agent in VM: ssh pi --ProxyCommand isotty-ssh pi--> 127.0.0.1:61001/ssh/pi ==ssh -R==> host gateway in `isotty daemon` --> raspberrypi.local:22
```

* The host relays TCP only, to the machine the human named and nowhere
  else; SSH runs between the VM and the machine, so the host sees neither
  the session nor a key.
* The relay is one more service of the host gateway, under `/ssh`: the
  VM's `ssh` reaches it through a `ProxyCommand` that turns the request
  into a byte stream with an HTTP upgrade. It needs no port of its own in
  the VM, and it exists exactly while the gateway does, that is while
  the VM runs and the host's daemon can reach it.
* Each machine gets a key of its own for the project, made on the host at
  `add` and given to the VM, as a [child VM](CHILD.md)'s key is: it opens
  that one machine, under the account the human named. It is ed25519, or
  ECDSA with `--key-type ecdsa` for an OpenSSH older than 6.5, which has
  no ed25519; ECDSA rather than RSA, which such a machine would verify
  only with SHA-1 signatures that the VM's ssh has to be told to make. The
  key carries no `authorized_keys` restriction; the agreement is the
  machine and the account, and what the agent does there is its business.
* The machine's host key is recorded at `add`, from a login the human
  makes and confirms, and the VM's `ssh` checks it strictly, asking for no
  other type of host key than those recorded; an old machine's RSA host
  key is so asked for by name. A machine that took the name or the address
  does not get the session.
* The machine is on the human's network, so an agent logged in to it
  reaches what it reaches. That is inside the agreement: the human chose
  the machine.

## Config

Project config, synced with the rest of `.isotty/` and pinned on the host:

```text
./.isotty/ssh.yaml
```

```yaml
hosts:
  pi:
    host: raspberrypi.local
    port: 22
    user: pi
    host_keys:
      - ssh-ed25519 AAAA...
```

The private keys are not in it; they are kept on the host under
`~/.isotty/projects/<hash>.ssh/`, since `.isotty/` is synced and the key
is the host's to give.

```bash
isotty host ssh add pi --host raspberrypi.local --user pi [--port 22] [--key-type ed25519|ecdsa]
isotty host ssh remove pi [--force]
isotty host ssh show
```

`add` logs in to the machine with the host's own `ssh` and the human's own
credentials, with a known-hosts file of its own so that `ssh` shows the
host key and asks whether to accept it, and in that same login appends the
new public key to the account's `~/.ssh/authorized_keys`. What the human
accepted is what is recorded.

`remove` logs in the same way, under the recorded host key, and takes that
public key out of `authorized_keys` before it forgets the machine.
`--force` forgets it without logging in, for a machine that is gone; its
`authorized_keys` keeps the key then. `down` leaves the machines alone:
they belong to the project, not to the VM.

## VM Side

`isotty up` installs `/usr/local/bin/isotty-ssh`, an `isotty-ssh` skill for
each configured agent, the keys under `~/.ssh/isotty-hosts/` and
`/etc/ssh/ssh_config.d/isotty-hosts.conf`, which names each machine as a
host with its user, its key, its host key and the `ProxyCommand`. The keys
reach the VM on the standard input of the command that writes them, not
on its command line, which the audit log records.

## Audit

The relay records `ssh-open` when a connection to a machine is made and
`ssh-close` when it ends, with the machine, the bytes each way and how
long it lasted, so the audit log tells when the agent was on which
machine. What it did there is in the session, which the host does not
see.
