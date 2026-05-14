# Forward Design

IsoTTY forward exposes services running inside the isolated environment on the local machine.

## Config

Project-local forward definitions live in:

```text
./.isotty/forward.yaml
```

Format:

```yaml
forwards:
  web:
    local_port: 8080
    remote_port: 8080
```

## CLI

```bash
isotty forward add <name> --local-port <port> --remote-port <port>
isotty forward list
isotty forward remove <name>
```

## Lifecycle

Forwards are active only while `isotty attach` is active.

That means:

* `attach` loads all configured forwards
* `attach` opens all forwards before the interactive shell
* forwards disappear when the attach session exits

## Transport

The VM backend uses SSH local forwarding through the same `gcloud compute ssh` session used for `attach`.

Each forward maps:

```text
127.0.0.1:<local_port> -> 127.0.0.1:<remote_port>
```

## Security Posture

The default posture is:

* local bind target is `127.0.0.1`
* forwards are not kept alive in the background
* invalid forward definitions fail loudly
