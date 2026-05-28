# Tools Design

IsoTTY can prepare typical working environments in the VM as named tool
bundles, so you select an environment by name instead of listing every package.

## Config

Project-local tool configuration lives in:

```text
./.isotty/tools.yaml
```

Example:

```yaml
tools:
  doc-tools: {}
```

Or manage it from the CLI:

```bash
isotty runtime tools available        # list selectable bundles
isotty runtime tools enable doc-tools
isotty runtime tools disable doc-tools
isotty runtime tools list             # bundles enabled for this project
```

## Why a separate concept

`apt` packages are an unstructured list the user maintains by hand. A tool
bundle is a curated, named environment that contributes apt packages *and* any
follow-up setup (for example creating a Python virtualenv). Picking `doc-tools`
is meant to convey "give me the document-handling toolkit", not "install these
six packages".

## Install Timing

Tool bundles are installed during `isotty up`.

Bundle apt packages are merged with the packages from `apt.txt` and installed in
a single `apt-get install` pass (de-duplicated). Any follow-up commands run
afterward, once the apt packages are present.

## Bundles

### `doc-tools`

For reading PDF announcements, checking Word/Excel templates, and organizing
submitted documents.

apt packages:

* `poppler-utils` — extract text and page info from PDFs
* `libreoffice` — convert and inspect Word/Excel formats
* `unzip` — quick text extraction from inside `.docx`
* `ripgrep` — fast search across a document set
* `python3-pip`

Python libraries are installed into the system interpreter:

* `pypdf`
* `pdfplumber`
* `pymupdf`
* `python-docx`

Because the VM is disposable, these are installed with
`pip3 install --break-system-packages` rather than a virtualenv. The default
`python3` can import them directly, so there is no venv path or launcher for an
agent to discover:

```bash
python3 -c "import pdfplumber; print(pdfplumber.__version__)"
```

## Adding Bundles

New bundles are registered in `internal/isotty/runtime/tools.go` (`builtinTools`).
Each bundle declares a summary, its apt packages, and optional follow-up
commands. Only registered bundle names are accepted by `tools enable`.
