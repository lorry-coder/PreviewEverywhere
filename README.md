# PreviewEverywhere

**Read the documents your coding agent writes — on your phone.**

English · [简体中文](README.zh-CN.md)

[![CI](https://github.com/lorry-coder/PreviewEverywhere/actions/workflows/ci.yml/badge.svg)](https://github.com/lorry-coder/PreviewEverywhere/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/lorry-coder/PreviewEverywhere)](https://github.com/lorry-coder/PreviewEverywhere/releases)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

---

## What it's for

A coding agent can produce a dozen reports, plans, risk assessments and migration
checklists in a day. They pile up in the `docs/` folder of every project, with
filenames longer than the last — and the moments you actually have time to read
them (commuting, waiting in line, before bed) are exactly the moments you only
have a phone in your hand.

PreviewEverywhere is a small always-on service that runs on your dev machine.
It watches your project directories; every document your agent writes gets
ingested, rendered and indexed automatically. Your phone joins the same Wi-Fi,
scans a QR code once, and that's it for a year.

**The whole platform is a single executable** with no runtime dependencies.
Cross-compiling needs no toolchain on the target, so moving it to a NAS or a
Raspberry Pi is one `scp`.

> **Heads-up on language.** The web UI, the CLI output and the
> [user manual](docs/使用手册.md) are currently **Chinese only**. The code,
> comments and this README are the parts available in English. If you don't read
> Chinese, the screenshots and messages will not be usable to you yet.
> Text normalization also contains one CJK-specific rule (see
> [design decisions](#four-decisions-not-to-undo-lightly)).

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/lorry-coder/PreviewEverywhere/main/install.sh | sh
pe setup
```

`pe setup` asks three questions — which directory to watch, whether to start on
boot, whether to hook into your agent — does the rest itself, and prints a QR
code at the end. Scan it with your phone and you're in.

Other ways:

```bash
docker run -d --name pe -p 8080:8080 \
  -v pe-data:/data -e TZ=Asia/Shanghai \
  ghcr.io/lorry-coder/previeweverywhere:latest     # the right choice on a NAS
```

Or grab an archive for your machine from
[Releases](https://github.com/lorry-coder/PreviewEverywhere/releases), unpack it
and put `pe` on your `PATH`. Checksums are in `checksums.txt`.

Builds are published for linux and macOS on amd64 and arm64, plus 32-bit arm
(Raspberry Pi).

## What it does

**Reading**
- The home page is a **timeline**, not a knowledge-base cover page. Documents are
  grouped by agent session when a session ID is available, and fall back to
  "date + project" when it isn't. For a stream of run output, *"what came out of
  last night's run"* is a far more common question than *"every document in this
  project"*.
- Mermaid diagrams and KaTeX are loaded on demand; the first paint ships 230 KB
  of JS.
- **Readable offline.** Add it to your home screen and it's a PWA. On a subway
  with no signal, documents you opened before still read to the end, images are
  still there, and your highlights are still visible.

**Finding**
- SQLite FTS5 full-text search, using the trigram tokenizer for Chinese. The
  search box takes a small query language (⌘K to focus):

  ```
  migration risk          full text
  "dual-write window"     exact phrase
  tag:review              has a tag
  tag:risk -tag:resolved  combine and exclude
  project:auth dual       within one project
  is:unread  kind:html    state and type
  ```
- Manual tag edits are tombstoned — a front-matter tag you deleted will not come
  back the next time the agent regenerates the document.

**Annotating**
- Four kinds of inline annotation: highlight, note, todo, question.
- **Annotations survive the document being rewritten.** This is the only genuinely
  hard part of the project. The strategy has three tiers: block-ID hit (free) →
  fuzzy quote match (relocated automatically, flagged for review) → orphaned
  (never deleted; the original text is snapshotted and can be re-anchored by hand).
- Todos and questions are collected across documents and can be exported as
  Markdown to feed back to the agent.
- Version diffs, with a "changes only" view.

**Taking it with you**
- Export as a single self-contained HTML file (images inlined), a zip bundle, a
  server-generated PDF (CJK font subset embedded — no system print dialog
  involved), or just download the original file.

## Getting documents in

Four channels, from "install once and forget" to fully manual:

| Channel | Good for | Setup |
|---|---|---|
| **Claude Code hook** (recommended) | Day to day | Client token, once |
| Directory watching | You already have a fixed `docs/` layout | Watch path, once |
| `pe push` | Scripts, CI, another machine | Client token |
| MCP | Letting the agent decide "this one is worth showing" | Client token |

With the hook installed, every `.md` / `.html` your agent writes arrives
automatically **regardless of which directory it lands in**, and output from one
session is grouped together on the timeline by `session_id`:

```bash
pe agent install --write     # writes to ~/.claude/settings.json (backs it up first)
```

> Start a new Claude Code session for it to take effect. If nothing shows up,
> check `pe agent status` first, then run
> `echo '{"cwd":"/x","tool_input":{"file_path":"/x/a.md"}}' | pe hook-ingest --verbose`
> to see what it is actually complaining about.

Watched directories support globs, and **the quotes are not optional**:

```bash
pe source add ~/projects/docs
pe source add '~/Code/*/docs'      # quotes keep the glob intact; it expands at runtime
pe source list
```

> Without quotes your shell expands the glob first. If it matches several
> directories the command fails with a usage error — annoying but obvious. The
> nasty case is when it matches **exactly one**: the command silently succeeds and
> you end up with that single path pinned forever, so directories created later
> never get picked up. When in doubt, `pe source list` shows you whether what got
> stored is a glob or a concrete path.

Changing watch rules **does not require a restart** — a running server picks them
up within a couple of seconds.

### Let the agent add metadata for you

A few lines of front-matter save you all the manual filing later. They are
stripped at render time:

```markdown
---
title: Migration risk assessment
project: auth-refactor
tags: [risk, needs-review]
summary: The dual-write window is the main source of risk
---
```

Worth putting in your project's `CLAUDE.md`: *"when generating report-style
documents, add front-matter with project and tags."*

## Common commands

```bash
pe setup                    # first-run wizard
pe status                   # running? watching what? how much came in? which devices?
pe doctor --fix             # self-check; fixes what it can

pe pair                     # add a device: prints a one-time pairing code
pe device list              # which devices are signed in
pe device revoke <id>       # revoke one; the others are unaffected

pe source add|list|rm       # manage watched directories
pe service install          # install as a user service (systemd / launchd)
pe service logs             # tail the logs
pe client set               # configure the client (used by push / hook / MCP)

pe push report.md --tag risk    # push a document from any machine
cat notes.md | pe push -        # or from a pipe

pe upgrade                  # replace the binary in place
pe completion zsh           # shell completion
```

The full reference is in **[docs/使用手册.md](docs/使用手册.md)** (Chinese).

## Things to know

### Security boundary (please read)

This program is built on the assumption of **a single user on a trusted LAN**.
Concretely:

- **It binds `0.0.0.0:8080` and speaks plain HTTP by default.** There is no way to
  get a trusted certificate for a LAN address, and a self-signed one makes the
  phone warn on every visit, so plain HTTP is the deliberate choice.
  **Do not expose this port to the public internet.** If you need to read from
  outside, put it behind something like [Tailscale](https://tailscale.com/) and
  keep it on a private network.
- **Authentication is one shared token plus a year-long cookie.** There are no
  accounts and no permission levels. Anyone with the token can read everything.
- **The master token is stored as a SHA-256 hash only.** If you lose it there is no
  way to get it back — you rotate it with `pe token rotate`. To add a device day
  to day, use `pe pair` instead: it issues that device its own credential and
  leaves the others alone.
- All data lives in one directory, `~/.local/share/pe/`, and nothing is sent
  anywhere. There are exactly two outbound requests in the whole program and both
  are avoidable: fetching CDN chart libraries referenced by agent-generated HTML
  so they still render offline (turn it off with `localize_cdn = false`), and
  `pe upgrade` when you explicitly run it.

### Known limits

- **The single-user assumption is load-bearing.** Read state hangs directly off
  `doc`, and annotations have no owner. The schema leaves room, but going
  multi-user is a real migration, not an added column.
- **Annotation relocation is not 100%.** When the agent rewrites a document
  heavily, some annotations will be orphaned. The answer is "never delete, keep a
  snapshot of the original text, support manual re-anchoring" rather than a
  cleverer algorithm — once the original text is truly gone, any algorithm is
  guessing, and a wrong guess is worse than not finding it.
- An annotation belongs to exactly one block; a selection spanning paragraphs is
  truncated at the end of the first one.
- In paragraphs containing formulas or diagrams, annotation highlights skip the
  formula/diagram itself (it has no layout rectangle). The text around it is
  unaffected.
- Full-text search only indexes the head version. Content that existed in an older
  version and has since been deleted is not searchable.
- Two-character Chinese queries fall back to a `LIKE` scan instead of the index.
  Imperceptible at tens of thousands of documents; at hundreds of thousands the
  answer is application-level segmentation, not more `LIKE`.
- `blobs/` only grows; there is no GC yet. `pe doctor --fix` removes orphaned
  files, but not blobs that were referenced by an old version and are no longer
  wanted.
- **inotify watches.** Recursively watching a large repository can exceed the
  system limit. The symptom is "new documents sometimes don't show up", with no
  error anywhere. `pe doctor` detects this and tells you how to fix it.
- **Always set `TZ` in a container.** The timeline groups by the *server's* local
  date while the UI labels "today / yesterday" using your *phone's* timezone;
  a mismatch puts documents written around midnight in the wrong bucket.

## When something goes wrong

Start here. It turns the troubleshooting table from the manual into one command:

```bash
pe doctor            # ten checks, each with a concrete next step
pe doctor --fix      # fixes what can be fixed automatically
pe status            # running? port answering? client configured?
```

The UI also has an **environment self-check** page (bottom of the sidebar) and a
**feedback** form — submitted reports are stored locally with a snapshot of the
environment at the time, readable via `pe feedback` or by opening `feedback.md`
in the data directory.

## Deployment

| Environment | Recommendation |
|---|---|
| Ordinary Linux, a small home server, macOS | `pe service install` (user service, no root) |
| Synology / QNAP / unRAID / TrueNAS SCALE | Docker |

```bash
pe service install     # installs, starts, and enables linger for you
pe service status
pe service logs
```

**Remote deployment means push mode.** This matters more than which runtime you
pick: your agent and your files are on your dev machine, and the remote box cannot
see those directories at all. So directory watching is essentially unusable in a
remote deployment — what actually works there is the hook and `pe push`, neither
of which needs a single mount. On the dev machine, point the client at the remote:

```bash
pe client set --endpoint http://<server-ip>:8080 --token <token>
```

Details (the three Docker gotchas, backups, moving machines, full reset) are in
[sections 5 and 7 of the manual](docs/使用手册.md).

## Uninstalling

Nothing here happens implicitly. In particular, **no uninstall step touches your
data** — you delete that yourself, on purpose.

### 1 · Stop and remove the service

```bash
pe service uninstall     # stops it, removes the unit, leaves the data alone
```

One thing it deliberately does *not* undo: `pe service install` enabled systemd
*linger* for your user, and uninstall leaves it on, because other user services of
yours may depend on it. Turn it off yourself if nothing else needs it:

```bash
sudo loginctl disable-linger "$USER"
```

### 2 · Remove the binary

```bash
sudo rm /usr/local/bin/pe    # installed by install.sh (or rm ~/.local/bin/pe)
rm ./pe                      # built from source — or just delete the clone
```

### 3 · Decide what to do with the data

Everything lives in one directory. Here is the complete list of what is on your
disk and whether the steps above removed it:

| Path | What it is | Removed above? |
|---|---|---|
| `~/.local/share/pe/` | **All your data.** See the breakdown below. | **No** |
| ` ├ pe.db`, `pe.db-wal`, `pe.db-shm` | The SQLite database — three files that belong together | No |
| ` ├ blobs/` | Content-addressed copies of every original file. Usually most of the size. | No |
| ` ├ pe.toml` | Server config, including the token hash | No |
| ` ├ runtime.json` | pid/port of a running server; deleted on a clean exit | — |
| ` └ feedback.md` | Plain-text projection of submitted feedback (only exists if there is any) | No |
| `~/.config/pe/config.toml` | Client config — endpoint plus the token **in plaintext** | No |
| `~/.config/systemd/user/pe.service` | systemd user unit (Linux) | Yes |
| `~/Library/LaunchAgents/pe.plist` | launchd agent (macOS) | Yes |
| `~/.claude/settings.json` | The hook entry added by `pe agent install` | **No — see below** |

Back it up first if you might want it later. Stop the service before copying:
recent writes sit in `pe.db-wal`, so copying `pe.db` alone from a running server
loses them.

```bash
pe service stop
tar czf pe-backup-$(date +%F).tar.gz -C ~/.local/share pe
```

Then remove it:

```bash
rm -rf ~/.local/share/pe     # documents, annotations, tags, read state — all of it
rm -rf ~/.config/pe          # client config (contains the token in plaintext)
```

**There is no `pe agent uninstall`.** Remove the `hook-ingest` entry from
`~/.claude/settings.json` by hand, or restore the backup that
`pe agent install --write` made before editing it:

```bash
mv ~/.claude/settings.json.bak ~/.claude/settings.json
```

### Docker

The named volume is the data, and it survives removing the container:

```bash
docker rm -f pe
docker volume rm pe-data                                # ← this is your data
docker image rm ghcr.io/lorry-coder/previeweverywhere
```

### Resetting without uninstalling

```bash
rm -rf ~/.local/share/pe
```

The next `pe serve` treats it as a fresh install: rebuilds the database, generates
a new token, prints a new QR code. The binary and the service stay as they are.

## Building from source

Requires Go 1.25+ and Node 20+.

```bash
git clone https://github.com/lorry-coder/PreviewEverywhere
cd PreviewEverywhere
make build          # builds the frontend, embeds it, produces ./pe
```

### Running it

`make build` leaves `./pe` in the repository — it is **not** on your `PATH`:

```bash
./pe setup          # same wizard; answer y to "start on boot" and it's running
./pe serve          # or run it in the foreground yourself
```

Three things that only bite you when running from a source build:

**Every command needs the `./`.** The wizard's own hints print `pe serve`,
`pe status` and so on without it, because it doesn't know how it was invoked.
Either keep typing `./`, or put the clone on your `PATH`:

```bash
export PATH="$PATH:$PWD"
```

**Rebuilding while it runs is safe, but does not swap the version.**
`make build` succeeds even with the server running — `go build -o` writes a new
file and renames over the old one, so you never get `text file busy`. But the
running process is holding the *old* inode and keeps serving the old build until
you restart it. `./pe status` says so plainly:

```
⚠ 跑着的是 xxx，盘上的是 yyy —— 重启才会换过去
```

**If you install the service from here, the unit points at this directory.**
`pe service install` writes the absolute path of `./pe` into the unit, so moving
or deleting the clone afterwards leaves a service that cannot start
(`ConditionFileIsExecutable` blocks it). To decouple them, copy the binary onto
your `PATH` first — and stop the service before you do, because `cp` truncates
in place and a running binary gives `text file busy`:

```bash
pe service stop
sudo cp pe /usr/local/bin/pe
pe service install
```

Data goes to `~/.local/share/pe/` either way, so you can start from a source
build today and switch to a release binary later without losing anything.

### Other make targets

```bash
make test           # go test + frontend typecheck + front/back parity
make check-docs     # actually runs every command documented in the READMEs and manual
make run            # backend only, for development (run the frontend with npm run dev)
make snapshot       # full release pipeline locally; artifacts in dist/, nothing uploaded
make cross          # cross-compile
```

> **`go install` is deliberately not supported.** The frontend build output is not
> in version control (the filenames are content-hashed; committing them is pure
> noise), so a `go install` binary would be a shell that serves no pages at all.
> Use a release archive, Docker, or `make build`.

## How it's put together

```
cmd/pe/            CLI and service entry points
internal/
  config/          pe.toml (server) and ~/.config/pe/config.toml (client)
  store/           SQLite + migrations + content-addressed blobs + search / timeline / annotations / diff
  render/          goldmark → sanitize → block IDs / plain text / TOC / image localization
  anchor/          annotation anchoring and relocation (the fuzzy matching lives here)
  search/          query-language parser for the search box
  ingest/          ingestion pipeline, fsnotify watcher, CDN inlining
  server/          HTTP API, auth, SSE
  pdf/             server-side PDF generation (bundled CJK font subset)
web/               React + Vite frontend, embedded into the binary at build time
scripts/parity.sh  front/back consistency checks
scripts/docs-check.sh  runs everything the docs claim you can do
```

### Four decisions not to undo lightly

**1 · The platform is downstream of the filesystem.**
The original file is *copied* into `blobs/`, not merely referenced by path. So when
the agent deletes an intermediate artifact or you switch branches, your phone can
still read it. The cost is double disk usage (negligible for text).

**2 · Markdown is rendered server-side, and block IDs are content hashes.**
Every leaf block carries a `data-blk` whose value is the SHA-256 of that block's
normalized text. The property that matters is *same content ⇒ same ID*: when the
agent rewrites a document, untouched paragraphs keep their IDs and annotations hit
for free. Moving rendering to the client would introduce small DOM differences
between phone and desktop, and annotation anchors would drift.

Normalization contains one CJK-specific rule: **a line break between two Han
characters produces no space.** Without it, the agent changing its wrap width once
would change every block ID in the paragraph — and re-wrapping is by far the most
common meaningless diff.

**3 · Document identity has a fallback chain.**
`explicit key → path within the repo → filename → title → content hash`. The last
two exist for pipe input: `cat a.md | pe push -` has no filename, and if everything
fell back to one shared name, pushing two documents in a row would turn the second
into a new version of the first.

**4 · The ingestion channel only affects where metadata comes from.**
Directory watching, `pe push`, the HTTP API, the hook and MCP all converge on the
same pipeline from "decide which project this belongs to" onward. Adding a channel
means adding a caller, not touching the pipeline.

### Two modes for agent-generated HTML

| Mode | How | Trade-off |
|---|---|---|
| `reader` | Sanitized, then styled by the platform | Loses the original styling; gains annotation, search, and a sane mobile layout |
| `raw` | Dropped into `<iframe sandbox="allow-scripts">` | Not annotatable; keeps charts and interactivity intact |

The sandbox deliberately grants `allow-scripts` and **not** `allow-same-origin`:
the iframe lives in an opaque origin, so scripts run but cannot reach the parent
DOM or cookies. Granting both is equivalent to no sandbox at all, and is the most
common mistake in this kind of design. Even in `raw` mode the pipeline still runs
a `reader` pass to extract plain text, so the document remains searchable.

## Contributing

Issues and PRs are welcome. Before you start, run:

```bash
make test && make check-docs
```

`check-docs` exists for the documentation: in a fake `HOME`, it runs every command
the READMEs and the manual tell you to run. Stale docs never fail a compile or a
unit test — they only strand the person following them — so it gets its own gate.

Please write comments that explain **why**, especially where something looks like
it could be simpler. Almost every non-obvious line in this repository has a
specific mistake behind it.

## License

[MIT](LICENSE)
