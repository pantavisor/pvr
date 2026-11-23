---
sidebar_position: 4
---

# Published artifacts (`pvr artifact`, `pvr sdk`)

Publish a file to your Hub account under a kind, a name and a version, so
devices and tools can find it later. `pvr sdk` is the short form for SDK
installers (kind `sdk`).

Both use your normal `pvr login` and `--baseurl`. A Hub that has no
published artifacts answers `this Hub does not support published artifacts yet`.

## pvr artifact publish

```bash
pvr artifact publish toolchain.sh --kind sdk --name acme-image --version 1.0 \
  --description "SDK for acme-image" --meta machine=qemux86-64 --meta target=x86_64
```

pvr computes the sha256 and size, uploads the file to your object store
(skipped when the account already has it, with a progress line for big files)
and registers the entry. It prints the id and sha256, or the entry with `--json`.

**Flags:** `--kind`, `--name`, `--version` (required); `--description`;
`--meta key=value` (repeatable); `--json`

A kind, name and version can be published once; `rm` it first to replace it.

## pvr artifact ls

```bash
pvr artifact ls --kind sdk
pvr artifact ls --name acme-image --json
```

```
ID                        KIND  NAME        VERSION  SIZE      DESCRIPTION
0123456789abcdef01234567  sdk   acme-image  1.0      812.4 MiB SDK for acme-image
```

**Flags:** `--kind`, `--name`, `--json`

## pvr artifact rm

```bash
pvr artifact rm 0123456789abcdef01234567
pvr artifact rm sdk/acme-image@1.0
```

## pvr sdk

```bash
pvr sdk publish pv-sdk-installer.sh --name acme-image --version 1.0 \
  --machine qemux86-64 --target x86_64 --platform linux --image acme-image --build-id 5f3a
pvr sdk ls [--name acme-image] [--json]
pvr sdk rm acme-image@1.0        # or the id
```

`publish` takes the `artifact publish` flags except `--kind`, plus `--machine`,
`--target`, `--platform`, `--image` and `--build-id`, which are stored in the
entry's meta (`build_id` for `--build-id`). `ls` shows name, version, machine,
target and size.
