---
name: isotty-aws
description: Run the AWS CLI, SDKs and other AWS tools as this project's IAM role with the isotty-aws command. Use whenever a task needs AWS.
---

# isotty-aws

This VM holds no AWS credential. AWS is reached as an IAM role the human
chose, in one account, assumed on the host with the human's login;
`isotty attach` provides a gateway that hands out the role's one-hour
credentials. The role is pinned in `/workspace/.isotty/aws.yaml`.

```
isotty-aws status
isotty-aws exec -- <command> [arguments]
```

`exec` runs the command with `AWS_CONTAINER_CREDENTIALS_FULL_URI` pointing
at the gateway, and with the variables through which another identity would
answer removed. The AWS CLI and SDKs read credentials from that endpoint
and fetch them again before they expire, for example:

```
isotty-aws exec -- aws sts get-caller-identity
```

## What you may reach

- What the role's policies allow, in its account. A call the policies do
  not allow is refused by AWS; ask the human if the task needs it.
- A profile or credentials of your own under `~/.aws/` come before the
  endpoint in the AWS credential chain; keep none there.
- Do not edit `/workspace/.isotty/aws.yaml`. It is synced to the host, and
  the host refuses to attach when it differs from what the human pinned.

## Lifecycle: credentials come from the attached host

Credentials are issued only while the host is attached and last an hour.
When the human detaches or their machine sleeps, `exec` fails within
seconds with an explicit error, and credentials obtained earlier stop
working within the hour. Nothing is queued; retry after re-attach, and say
so in your report.
