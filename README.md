# axiom-agent

A local extraction agent and GUI for [AXIOM](https://axiom.ruvelta.com), a
sovereign intelligence document system. `axiom-agent` runs on your own
machine, extracts a local git repository's content, and uploads it to your
AXIOM backend for processing — without requiring AXIOM to have direct access
to your filesystem or a full repository mirror.

This is the **local-extraction-only** path: an alternative to linking a
GitHub repository through the AXIOM GitHub App, for cases where you'd rather
not grant repository access to a third-party GitHub App at all.

## What it does

- Watches a local git repository for changes (gitignore-aware).
- Extracts real commit and file content via a local git read, not a clone of
  the remote.
- Uploads the extracted content to your configured AXIOM server over HTTPS.
- Runs as a background daemon (`axiom-agent-daemon`) with a companion
  desktop GUI (`axiom-agent-gui`) for setup, folder selection, and
  first-run configuration.
- Stores your API key locally in an AES-256-GCM encrypted file store, never
  in plaintext on disk.

## What it does not do

- It does not send your source code anywhere other than the AXIOM server URL
  you explicitly configure.
- It does not require a GitHub App installation or repository access grant.
- The private key used to encrypt your locally stored API key never leaves
  your machine.

## Building

Requires Go 1.21+.

```bash
go build -o axiom-agent-daemon ./cmd/axiom-agent
go build -o axiom-agent-gui ./cmd/axiom-agent-gui
```

The GUI build requires a working [Fyne](https://fyne.io) toolchain — see
Fyne's own [getting started guide](https://docs.fyne.io/started/) for
platform-specific prerequisites.

## Configuration

On first run, the GUI will prompt you for:

- The folder(s) you want watched and extracted.
- Your AXIOM server URL.
- An AXIOM API key (generated from your AXIOM account's API Keys page).

These are stored locally under your OS's standard config directory, with the
API key itself held only in the encrypted credential store — never in a
plaintext environment file.

## Platform support

Linux is supported and actively tested. Windows support is untested; issues
and PRs are welcome. macOS support is not yet available in this release.

## License

MIT — see [LICENSE](LICENSE).
