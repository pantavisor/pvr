# Agent instructions

pvr is a Go CLI that manages Pantavisor device state: a versioned JSON document
plus a content-addressed object store, pushed to devices and to Pantahub.
Start with [README.md](README.md); command reference is in
[docs/index.md](docs/index.md).

## Layout

| Path | What is there |
|------|---------------|
| `main.go`, `cmd/` | CLI. `cmd/pvrcli` wires the commands; `cmd/<name>/` holds command groups such as `app`, `device`, `sig`, `wifi`, `image`, `builds` |
| `libpvr/` | Core library: state, objects, remotes, docker/registry access, self-upgrade (`packageupdater.go`) |
| `models/`, `api/`, `utils/` | Shared types, API clients, helpers |
| `templates/` | App templates used by `pvr app add` ([docs/PVR_TEMPLATES.md](docs/PVR_TEMPLATES.md)) |
| `docs/` | Command reference, one file per command group in `docs/commands/` |
| `_test/` | Shell tests (`*.sh`, shunit2 style), expected output in `_test/expected/` |
| `ci/` | Release script, asset packaging, manifest check, Go patches |
| `.github/workflows/` | CI: `test`, `build`, `release`, `sync-gitlab` |
| `.gitlab-ci.yml` | Old CI, still running during the move to GitHub |

## Build and test

```bash
go build -o pvr .          # needs Go 1.24
./run_docker_test.sh       # tests in a container (needs docker)
```

The tests download a 100 MB fixture from the `test-data` release on first run.
Go 1.21 builds use `go1.21.mod` and `Dockerfile.go1.21`; code that needs newer
Go is gated by `//go:build go1.22`.

## Rules

- Follow [CONTRIBUTING.md](CONTRIBUTING.md), especially the release rules.
- Write commit messages as Conventional Commits. The changelog is generated from them.
- Never delete, move or recreate a published tag.
- Never delete or re-create the `test-data` release.
- Update `docs/commands/*.md` when you change a command or flag, and check
  every flag you write against `pvr <command> --help`.
- The Go module path is `github.com/pantavisor/pvr`. GitLab references that
  remain are the self-upgrade fallbacks and the sync bridge, see
  [docs/github-migration.md](docs/github-migration.md).
- Docker images must stay plain v2 manifests, no attestation index
  (`ci/verify-plain-manifest.sh`): old `pvr self-upgrade` cannot read an index.
