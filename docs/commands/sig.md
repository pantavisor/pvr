---
sidebar_position: 6
---

# Signature Commands

Manage digital signatures for PVR state documents using JSON Web Signatures
(JWS). Signatures protect the integrity of container configurations and
firmware updates.

---

## pvr sig add

Embed a JWS signature protecting JSON document elements of a selected part.

```bash
pvr sig add --part nginx
pvr sig add --part nginx --key ./private.pem
pvr sig add --parts="nginx,base-os" --include="**/*.json" --exclude="src.json"
pvr sig add --noconfig                       # exclude _config parts
```

**Flags:**

| Flag | Env | Description |
|------|-----|-------------|
| `--part, -p` | — | Select which part to sign |
| `--raw, -r` | — | Use raw include/exclude (bypasses `--noconfig`) |
| `--include, -i` | — | Glob patterns for files to include. Default: `**` |
| `--exclude, -e` | — | Glob patterns for files to exclude. Default: `src.json` |
| `--noconfig, -n` | — | Exclude `_config` parts from signature |

**Parent flags** (shared by all `pvr sig` subcommands):

| Flag | Env | Description |
|------|-----|-------------|
| `--key, -k` | `PVR_SIG_KEY` | Private key in PEM format |
| `--x5c, -x` | `PVR_X5C_PATH` | Path to cert chain for JWS x5c header. Use `no` to exclude |
| `--pubkey, -p` | `PVR_SIG_PUBKEY` | Public key store for signature validation |
| `--cacerts, -c` | `PVR_SIG_CACERTS` | CA cert file/directory. Use `__system__` for system store |
| `--certs-url` | `PVS_CERTS_URL` | URL for downloading PVS certificates tarball |
| `--with-payload` | `PVS_SIG_WITH_PAYLOAD` | Include full payload in JOSE serialization |
| `--output` | `PVS_SIG_OUTPUT` | Output file for signature. `-` prints to stdout |

---

## pvr sig update

Re-sign an existing `pvs@2` signature using the encoded match rule.

```bash
pvr sig update --part nginx --key ./private.pem
```

Uses the same match rule from the original `sig add`. Useful after modifying
the protected files.

**Parent flags:** same as `pvr sig add`

---

## pvr sig ls

Verify and list signatures in the repository.

```bash
pvr sig ls                     # list all signatures
pvr sig ls nginx               # list a specific signature
pvr sig ls --with-sigs         # show full JWS in output
pvr sig ls --part nginx        # filter by part
```

**Flags:**

| Flag | Env | Description |
|------|-----|-------------|
| `--part, -p` | `PVR_SIG_ADD_PART` | Filter signatures by part |
| `--with-sigs, -s` | `PVR_SIG_LS_WITH_SIGS` | Show full JWS serializations |

---

## pvr sig download

Download PVS certificates (CA cert, signing key, x5c chain).

```bash
pvr sig download                            # default URL
pvr sig download --url https://example.com/certs.tar.gz
```

Used during CI/CD image builds to provision signing materials.

**Flags:**

| Flag | Env | Description |
|------|-----|-------------|
| `--url` | `PVR_SIG_CERTS_URL` | URL to download certificates from |
