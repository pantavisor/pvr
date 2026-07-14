---
sidebar_position: 7
---

# Wi-Fi Provisioning Commands

Provision Pantavisor devices over BLE using the Improv Wi-Fi protocol. All
commands require Linux with BLE support.

---

## pvr wifi device-scan

Scan for nearby BLE devices advertising the Improv Wi-Fi service.

```bash
pvr wifi device-scan
pvr wifi device-scan --timeout=30s
pvr wifi device-scan --json
```

**Flags:**

| Flag | Description |
|------|-------------|
| `--timeout` | Scan duration. Default: `15s`. `0` = forever |
| `--json` | Emit one JSON object per device |

---

## pvr wifi provision

Connect to an Improv BLE device and send Wi-Fi credentials.

```bash
pvr wifi provision <BLE_ADDRESS>
pvr wifi provision --ssid MyWiFi --password secret
```

If `--password` is omitted, prompts interactively.

**Flags:**

| Flag | Description |
|------|-------------|
| `--ssid` | Wi-Fi SSID |
| `--password` | Wi-Fi password (omit for interactive prompt) |
| `--connect-timeout` | BLE connect timeout. Default: `20s` |
| `--state-timeout` | Wi-Fi connection timeout. Default: `60s` |

---

## pvr wifi identify

Ask a device to identify itself (e.g. blink an LED or beep).

```bash
pvr wifi identify <BLE_ADDRESS>
```

**Flags:**

| Flag | Description |
|------|-------------|
| `--connect-timeout` | BLE connect timeout. Default: `20s` |

---

## pvr wifi device-info

Read firmware and device information from an Improv device.

```bash
pvr wifi device-info <BLE_ADDRESS>
```

Output: firmware name, firmware version, hardware variant, device name.

**Flags:**

| Flag | Description |
|------|-------------|
| `--connect-timeout` | BLE connect timeout. Default: `20s` |

---

## pvr wifi hostname

Get or set the device hostname.

```bash
pvr wifi hostname <BLE_ADDRESS>               # get current hostname
pvr wifi hostname <BLE_ADDRESS> my-device     # set hostname
```

**Flags:**

| Flag | Description |
|------|-------------|
| `--connect-timeout` | BLE connect timeout. Default: `20s` |

---

## pvr wifi wifi-scan

Ask a device to scan for nearby Wi-Fi networks and report them.

```bash
pvr wifi wifi-scan <BLE_ADDRESS>
```

**Flags:**

| Flag | Description |
|------|-------------|
| `--connect-timeout` | BLE connect timeout. Default: `20s` |

---

## pvr wifi claim-info

Read Pantavisor claim material from a device.

```bash
pvr wifi claim-info <BLE_ADDRESS>
pvr wifi claim-info <BLE_ADDRESS> --json
```

Outputs the device ID and claim challenge needed to claim the device on
Pantacor Hub. Retries until the device has claim material (may take time after
first hub rendezvous).

**Flags:**

| Flag | Description |
|------|-------------|
| `--connect-timeout` | BLE connect timeout. Default: `20s` |
| `--retry-timeout` | Total retry duration. Default: `60s` |
| `--retry-interval` | Delay between retries. Default: `5s` |
| `--json` | Print result as JSON |
