---
sidebar_position: 7
---

# Wi-Fi Provisioning Commands

Provision Pantavisor devices over BLE using the [Improv Wi-Fi](https://www.improv-wifi.com/ble/)
protocol. All commands require Linux with BLE support.

On the device side these commands talk to `pvwificonnect`, the core
provisioning container shipped in the Pantavisor starter image. It advertises
the Improv BLE service by default (no configuration needed) alongside its own
setup access point, so a factory-fresh device can be joined to Wi-Fi either
over Bluetooth or by joining its `pvwificonnect` AP.

The Improv BLE client is built into pvr, so this is the only tool you need —
there is no separate provisioning binary to install. The same flow is also
available in a browser at
[hub.pantacor.com/provision-wifi](https://hub.pantacor.com/provision-wifi)
(Web Bluetooth: Chrome/Edge, not Firefox or Safari), which then hands you into
claiming the device with its ID and challenge prefilled.

Prerequisites: BlueZ running (`bluetoothd`) and access to the adapter —
membership in the `bluetooth` group, or root.

Addresses are MACs on Linux (`AA:BB:CC:DD:EE:FF`). Every subcommand below
except `device-scan` takes the address as a positional argument.

### Which device is which

pvwificonnect advertises as `PV-<model>-<id>`, e.g. `PV-RPi5-3F2A`: `<model>`
is a short board name (`RPi5`, `CM5`, `RPi3B+`, ...) and `<id>` the last four
hex digits of the device's Bluetooth address. The name can be changed with
`ble.device_name` in its `config.json`.

The scan also shows each device's Improv state: `authorized` while it waits
for Wi-Fi credentials, `provisioned` once it is online. An online device stops
advertising when its pairing window ends (10 minutes after boot or after it
came online, `ble.pairing_window`); run `pvwificonnect-cli ble-pairing` on the
device to make it discoverable again.

---

## pvr wifi device-scan

Scan for nearby BLE devices advertising the Improv Wi-Fi service, and print
their address, name, signal and state.

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
pvr wifi provision <BLE_ADDRESS> --ssid MyWiFi
pvr wifi provision <BLE_ADDRESS> --ssid MyWiFi --password secret
```

`--ssid` is required. If `--password` is omitted, the command prompts
interactively — preferred, since the flag leaks the password into shell
history.

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

Output: firmware name, firmware version, hardware variant, device name, and
the Pantavisor device-id once the device has one.

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

Prints the device-id and claim challenge. This only **reads** them; claiming
is done on the hub, with [`pvr claim`](auth.md#pvr-claim) or the Pantacor Hub
web app:

```bash
pvr claim <device-id> --challenge <challenge>
```

This is a Pantacor extension to Improv (RPC `0x07`, advertised as the
`pantavisor-claim` capability), so it only works against `pvwificonnect`
devices.

- **Not online yet**: the device gets its device-id and challenge when it first
  registers with Pantacor Hub, so provision it first. Until then it answers
  "not ready" and the command retries every `--retry-interval` up to
  `--retry-timeout`.
- **Already claimed**: there is no challenge left; the command prints only the
  device-id and `status: already claimed`.
- **No longer advertising**: an online device stops advertising when its
  pairing window ends. Run `pvwificonnect-cli ble-pairing` on it first.

**Flags:**

| Flag | Description |
|------|-------------|
| `--connect-timeout` | BLE connect timeout. Default: `20s` |
| `--retry-timeout` | Total retry duration. Default: `60s` |
| `--retry-interval` | Delay between retries. Default: `5s` |
| `--json` | Print result as JSON |

---

## Typical flow

```bash
pvr wifi device-scan                                  # find the device
pvr wifi identify AA:BB:CC:DD:EE:FF                   # confirm it is the right one
pvr wifi wifi-scan AA:BB:CC:DD:EE:FF                  # see what it can reach
pvr wifi provision AA:BB:CC:DD:EE:FF --ssid MyWiFi    # prompts for password
pvr wifi claim-info AA:BB:CC:DD:EE:FF                 # device-id + challenge
pvr claim <device-id> --challenge=<challenge>         # claim it on the hub
```

See [`pvr claim`](auth.md#pvr-claim) for the claiming step.

## Related

- [pvwificonnect](https://docs.pantavisor.io/meta-pantavisor/overview/examples/pvwificonnect/) — the
  device-side provisioning container: BLE, setup AP, captive portal, and its
  `config.json`
- [Improv Wi-Fi](https://www.improv-wifi.com/) — protocol spec
