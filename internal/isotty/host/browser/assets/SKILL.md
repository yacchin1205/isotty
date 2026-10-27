---
name: isotty-open
description: Open a URL in the human's browser on the host from this VM with the isotty-open command. It is the BROWSER of the attached session, so gh, Python's webbrowser and your own "open in browser" use it without being told.
---

# isotty-open

```
isotty-open <url>
```

The URL opens in the human's default browser on the host, in front of
them, while the host is attached with `isotty attach`. The attached session
has `BROWSER` set to this command, so tools that open a browser do so
through it on their own.

Only `http` and `https` URLs are opened. When it refuses, or the host is
not attached, put the URL in your report for the human to open. Every open
is recorded in the audit log the human reads.
