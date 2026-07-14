---
sidebar_position: 11
---

# Deprecated Commands

These commands are deprecated. Use the `pvr device` equivalents instead.

---

## pvr scan (deprecated)

Use `pvr device scan` instead.

Scans for Pantavisor devices announcing themselves via mDNS on the local
network.

---

## pvr ps (deprecated)

Use `pvr device ps` instead.

Lists owned devices in a table format (ID, nick, rev, status, state, seen,
message).

---

## pvr logs (deprecated)

Use `pvr device logs` instead.

```bash
pvr logs <deviceid>[/source][@level][#platform]
```

Fetches logs from Pantahub for owned devices.
