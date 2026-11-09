---
name: isotty-open
description: Show a URL or a file of this workspace to the human on the host with the isotty-open command, and read the comments they leave on it. A URL opens in their browser; a file under /workspace opens there as the synced copy in a viewer where the human can comment on a line, a paragraph or a spot of an image, so a figure or report you wrote needs no server and leaves the machine for nowhere. `isotty-open reviews` lists their comments with file and line. It is the BROWSER of the attached session, so gh, Python's webbrowser and your own "open in browser" use it without being told.
---

# isotty-open

```
isotty-open <url>
isotty-open <path>
isotty-open reviews [--all] [<path>]
```

A URL opens in the human's default browser on the host, in front of them,
while the host is attached with `isotty attach`. The attached session has
`BROWSER` set to this command, so tools that open a browser do so through
it on their own. Only `http` and `https` URLs are opened.

A path under `/workspace` opens in that browser too: the workspace is
synced to the host, so the file is already on their machine. The host
waits for the sync to carry it over and shows its copy in a viewer, a PNG
as an image, a Markdown report as a page with the images it refers to by
relative path, any other text line by line. Nothing is uploaded anywhere,
and no server is needed. Only paths under `/workspace`, and only what the
sync shares: secrets, keys and the like stay out of it on both sides. What
you wrote is shown inert; a script in an HTML file does not run, and HTML
is shown as text.

## Comments from the human

In the viewer the human can comment on a line or a range of a text, on a
paragraph, heading or list item of a Markdown page, on a spot or an area
of an image, or on the file as a whole. The comments reach this VM through
the sync, as `/workspace/.isotty/reviews/<path>.jsonl`. After you showed
a file, and before you report, read them:

```
isotty-open reviews              # every unresolved comment, by file
isotty-open reviews docs/report.md
```

Each comment names its place as the file was when it was written:
`L12-14` with the text that was there, `(31%, 62%)` on an image. When the
file has changed since, the listing says so and where the quoted text is
now; take that as the place. Act on the comments, say in your report what
you did about each, and leave marking them resolved to the human. Do not
edit the files under `.isotty/reviews/`.

When it refuses, or the host is not attached, put the URL or the path in
your report for the human to open. Every open is recorded in the audit log
the human reads.
