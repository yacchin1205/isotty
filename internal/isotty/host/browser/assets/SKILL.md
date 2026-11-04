---
name: isotty-open
description: Show a URL or a file of this workspace to the human on the host with the isotty-open command. A URL opens in their browser; a file under /workspace opens there as the synced copy, so a figure or report you wrote needs no server and leaves the machine for nowhere. It is the BROWSER of the attached session, so gh, Python's webbrowser and your own "open in browser" use it without being told.
---

# isotty-open

```
isotty-open <url>
isotty-open <path>
```

A URL opens in the human's default browser on the host, in front of them,
while the host is attached with `isotty attach`. The attached session has
`BROWSER` set to this command, so tools that open a browser do so through
it on their own. Only `http` and `https` URLs are opened.

A path under `/workspace` opens in that browser too: the workspace is
synced to the host, so the file is already on their machine. The host
waits for the sync to carry it over and shows its copy in the browser, a
PNG as an image, a Markdown or HTML report as a page with the images it
refers to by relative path. Nothing is uploaded anywhere,
and no server is needed. Only paths under `/workspace`, and only what the
sync shares: secrets, keys and the like stay out of it on both sides. The
page is shown inert; a script in an HTML file you wrote does not run.

When it refuses, or the host is not attached, put the URL or the path in
your report for the human to open. Every open is recorded in the audit log
the human reads.
