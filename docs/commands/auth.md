---
sidebar_position: 8
---

# Authentication Commands

Manage Pantahub authentication.

---

## pvr login

Log in to Pantahub with your username and password.

```bash
pvr login
pvr login https://api2.pantahub.com/auth
pvr login --interactive
pvr login --oauth
pvr login --netrc
```

If no endpoint is given, defaults to `<PVR_BASEURL>/auth/auth_status`.

**Flags:**

| Flag | Description |
|------|-------------|
| `--interactive, -i` | Login using only interactive mode |
| `--oauth, -o` | Login with OAuth |
| `--netrc, -n` | Login using only credentials found in `.netrc` for the auth endpoint's host |

`--interactive`, `--oauth`, and `--netrc` are each single-method: if the
chosen method fails (e.g. no matching `.netrc` entry, or the credentials are
rejected), `pvr login` fails immediately with the underlying error — it does
not fall back to any other method (cached token, refresh token, interactive
prompt, etc).

The `.netrc` file is looked up in the current directory first, then
`$HOME/.netrc`, matched against the host of the actual auth endpoint (as
advertised by the server), not necessarily the host you passed on the
command line.

---

## pvr whoami

Show the currently logged-in user.

```bash
pvr whoami
pvr whoami https://auth.pantahub.com
```

Output:
```
username(prn:::accounts:/<ID>) at https://api.pantahub.com/auth
```

**Flags:** none

---

## pvr register

Register a new Pantahub user account.

```bash
pvr register https://api.pantahub.com -u myuser -p mypass -e me@example.com
```

Registration is a two-step process:
1. Complete a CAPTCHA to confirm you are not a bot
2. Confirm your email address

**Flags:**

| Flag | Description |
|------|-------------|
| `--user, -u` | Desired username |
| `--pass, -p` | Password |
| `--email, -e` | Email address |

---

## pvr curl

Thin wrapper around the system `curl(1)` that injects the Pantahub bearer
token as an `Authorization: Bearer <token>` header.

```bash
pvr curl https://api.pantahub.com/trails/
pvr curl -sS https://api.pantahub.com/trails/ | jq .
pvr curl -X POST -H 'Content-Type: application/json' -d '{"key":"val"}' https://api.pantahub.com/some/endpoint
pvr curl -o firmware.bin <signed-get-url>
```

The bearer token is resolved through the same auth chain as `pvr clone`/`get`/
`whoami`: cached token → refresh → `.netrc` → interactive login.

If you pass your own `-H Authorization:` header, pvr does not override it.

All flags are passed through to `curl(1)` unchanged — pvr only injects the
auth header. Requires `curl(1)` in `PATH`.

**Flags:** none (all arguments forwarded to curl)

---

## pvr claim

Claim ownership of a device through a secret challenge only accessible to
device owners. The device must already have registered itself with
Pantahub. Prompts for the challenge if not provided as an argument.

```bash
pvr claim https://api.pantahub.com/devices/xxxxxxxxxx
pvr claim <device-id-or-nick> --challenge=<secret>
```

For a device set up over Bluetooth, [`pvr wifi claim-info`](wifi.md#pvr-wifi-claim-info)
prints the device-id and challenge.

Alias: `cl`

**Flags:**

| Flag | Description |
|------|-------------|
| `--challenge, -c` | Secret Challenge |
