# AWS Design

IsoTTY can let the agent in the VM use AWS, in one account, as an IAM role
the human chose, without the human's AWS credentials ever entering the VM.
The host assumes the role with its own AWS login; the host gateway hands
the VM the role's short-lived credentials.

## Problem

The usual way to give an agent AWS access is the human's own credentials,
or an access key made for it, which reach whatever that identity reaches,
for as long as the key lives. Nothing narrows that per project, and a key
in the VM outlives the work.

An IAM role is the opposite: it reaches what its policies allow, in its
account, and its credentials expire. AWS enforces both. Writing the
policies is an ordinary IAM operation the human already knows. So the unit
of permission is the role, as the service account is for
[Google](GOOGLE.md), and IsoTTY's job is only to hand out its credentials
without letting the human's out of the host.

## Model

```text
agent in VM --isotty-aws exec-- tool --> AWS APIs
                 |
                 +--GET /aws/credentials--> 127.0.0.1:61001 ==ssh -R==> host gateway in `isotty daemon`
                                                                          |
                                                          the host's aws CLI and its profile
```

* The host gateway mounts `/aws`, whose one operation returns credentials
  of the pinned role. The gateway runs `aws sts assume-role` with the
  host's `aws` CLI under the profile pinned for this project, for one hour,
  and reuses the credentials until they are about to expire. The `aws` CLI
  is used rather than an SDK built into IsoTTY, as `gcloud` is for GCP: the
  human's profiles, SSO among them, work as they do in the human's shell.
* The role session is named `isotty-<project-hash>`, so CloudTrail tells
  this project's calls from the human's own.
* The VM holds no credential of the human. `isotty-aws exec` runs a command
  with `AWS_CONTAINER_CREDENTIALS_FULL_URI` pointing at the gateway. The
  AWS CLI and SDKs read credentials from such an endpoint and fetch them
  again before they expire, so neither the credentials nor their renewal
  appear in the command. This differs from `isotty-google exec`, which
  hands the token itself: AWS tools read such an endpoint, while Google's
  counterpart is the GCE metadata server, which the VM is kept from using. API calls are not relayed:
  what the credentials can reach is already bounded by AWS.

## Config

Project config, synced with the rest of `.isotty/`:

```text
./.isotty/aws.yaml
```

```yaml
role_arn: arn:aws:iam::123456789012:role/isotty-agent
```

Host config, never synced:

```text
~/.isotty/projects/<project-hash>.aws-host.yaml
```

```yaml
profile: admin
```

```bash
isotty host aws enable --role-arn arn:aws:iam::123456789012:role/isotty-agent [--profile admin]
isotty host aws show
isotty host aws disable
```

The account is the one in the role's ARN. One project has one role, as one
project has one Google service account: a project that needs two reaches
gets a role that has both, and the human picks which one the agent is.

The profile is which of the host's AWS logins assumes the role. It is the
host's own matter, like a GitLab token, so it stays out of the synced file.
Without `--profile` the `aws` CLI's default applies, as the environment the
host daemon runs in sets it; the role is the pinned one either way.

`enable` assumes the role once and checks with `sts get-caller-identity`
that the account is the ARN's, and pins nothing when either fails, so a
role that cannot be used is found at `enable` and not when the agent
first asks for credentials. The failure, and `enable --help`, print how to make the role
ready: a trust policy that lets the host's identity assume it. What the
role may do is the policies the human attaches, and the guide says only
that.

### Pin

`.isotty/` is synced two-way, so an agent could change the role in
`aws.yaml`. `isotty host aws enable` records the accepted role on the host:

```text
~/.isotty/projects/<project-hash>.aws.yaml
```

`up` compares the synced file with the pin and refuses to continue when
they differ or when no pin exists. While the VM runs, the daemon issues no
credentials when they differ, and `isotty daemon status` says so. Accepting
a change means running `isotty host aws enable` on the host again.

## VM Side

`isotty up` installs, on every run:

* `/usr/local/bin/isotty-aws`, a Python client in the style of
  `isotty-google`:

  ```
  isotty-aws status
  isotty-aws exec -- <command> [arguments]
  ```

* the `isotty-aws` skill for each agent in `agent.yaml`

The `aws` CLI and the SDKs are the agent's to install, as the Google client
libraries are.

## Audit

Every time the gateway assumes the role, the VM event log
(`/var/log/isotty/events.jsonl`) records the role, the session name and the
expiry; the same log carries every `isotty-aws exec` invocation with the
command it ran. CloudTrail holds the authoritative record of what the role
did, under the session name.

## Lifecycle

Credentials are issued while the VM runs and the host's daemon can be
reached. Credentials already in hand keep working until they expire, at
most one hour after the host went away; after that every call fails until
the host is back.

## Security Posture

* the human's AWS login never leaves the host
* the VM reaches only what the role's policies allow, in the role's
  account; AWS enforces both
* the role is pinned on the host; the synced copy cannot change it
* credentials that leak out of the VM are useful for at most an hour and
  only within the same bounds
