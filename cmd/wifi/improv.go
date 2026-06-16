//
// Copyright 2017-2025  Pantacor Ltd.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//

// ponytail: copied from gitlab.com/pantacor/pvwificonnect/modules/ble to keep
// the new `pvr wifi` subcommand self-contained. Upgrade path: import the
// upstream ble module once it is published/available as a go dependency.

package wifi

import (
	"errors"
	"fmt"
)

// Improv BLE service and characteristic UUIDs.
const (
	ImprovServiceUUID    = "00467768-6228-2272-4663-277478268000"
	CurrentStateCharUUID = "00467768-6228-2272-4663-277478268001"
	ErrorStateCharUUID   = "00467768-6228-2272-4663-277478268002"
	RPCCommandCharUUID   = "00467768-6228-2272-4663-277478268003"
	RPCResultCharUUID    = "00467768-6228-2272-4663-277478268004"
	CapabilitiesCharUUID = "00467768-6228-2272-4663-277478268005"
	ImprovAdvServiceUUID = "00004677-0000-1000-8000-00805f9b34fb"
	ImprovAdvServiceShort = "4677"
)

// State values (Current State characteristic / Serial type 0x01).
const (
	StateAuthRequired byte = 0x01
	StateAuthorized   byte = 0x02
	StateProvisioning byte = 0x03
	StateProvisioned  byte = 0x04
)

// Error values (Error State characteristic / Serial type 0x02).
const (
	ErrorNone          byte = 0x00
	ErrorInvalidRPC    byte = 0x01
	ErrorUnknownRPC    byte = 0x02
	ErrorUnableConnect byte = 0x03
	ErrorNotAuthorized byte = 0x04
	ErrorBadHostname   byte = 0x05
	ErrorUnknown       byte = 0xFF
)

// Capabilities bitmask.
const (
	CapIdentify        byte = 1 << 0
	CapDeviceInfo      byte = 1 << 1
	CapScan            byte = 1 << 2
	CapHostname        byte = 1 << 3
	CapPantavisorClaim byte = 1 << 4
)

// RPC command IDs.
const (
	RPCSendWiFi        byte = 0x01
	RPCIdentify        byte = 0x02
	RPCDeviceInfo      byte = 0x03
	RPCScan            byte = 0x04
	RPCHostname        byte = 0x05
	RPCDeviceName      byte = 0x06
	RPCPantavisorClaim byte = 0x07
)

// Serial packet types (byte 8 of an IMPROV frame).
const (
	SerialTypeCurrentState byte = 0x01
	SerialTypeErrorState   byte = 0x02
	SerialTypeRPCCommand   byte = 0x03
	SerialTypeRPCResult    byte = 0x04
)

// SerialHeader is the 7-byte prefix of every Improv serial frame: "IMPROV" + version 0x01.
var SerialHeader = []byte{'I', 'M', 'P', 'R', 'O', 'V', 0x01}

// ErrBadChecksum is returned when a received packet's checksum byte does not match.
var ErrBadChecksum = errors.New("improv: bad checksum")

// ErrShortPacket is returned when a buffer is too small to contain a valid packet.
var ErrShortPacket = errors.New("improv: short packet")

// checksum returns the low byte of the sum of all input bytes.
func checksum(b []byte) byte {
	var s uint32
	for _, x := range b {
		s += uint32(x)
	}
	return byte(s & 0xFF)
}

// RPCPacket is a decoded RPC Command or RPC Result payload (without the outer
// serial framing — this is what sits on the BLE characteristic as-is, and inside
// the Data section of a serial frame of type 0x03/0x04).
//
// Wire layout:
//   [0]   Command ID
//   [1]   Data length N
//   [2..] Data (N bytes)
//   [N+2] Checksum (LSB of sum of bytes 0..N+1)
type RPCPacket struct {
	Command byte
	Data    []byte
}

// Encode serialises the packet with its trailing checksum.
func (p RPCPacket) Encode() []byte {
	out := make([]byte, 0, 3+len(p.Data))
	out = append(out, p.Command, byte(len(p.Data)))
	out = append(out, p.Data...)
	out = append(out, checksum(out))
	return out
}

// DecodeRPCPacket parses an RPC packet with trailing checksum.
func DecodeRPCPacket(buf []byte) (RPCPacket, error) {
	if len(buf) < 3 {
		return RPCPacket{}, ErrShortPacket
	}
	dataLen := int(buf[1])
	if len(buf) < 3+dataLen {
		return RPCPacket{}, ErrShortPacket
	}
	end := 2 + dataLen
	if checksum(buf[:end]) != buf[end] {
		return RPCPacket{}, ErrBadChecksum
	}
	return RPCPacket{
		Command: buf[0],
		Data:    append([]byte(nil), buf[2:end]...),
	}, nil
}

// EncodeStringList builds the Data section for an RPC Result: a concatenation
// of length-prefixed strings. Used by Send Wi-Fi (redirect URL), Device Info,
// Scan (one entry per network), etc.
func EncodeStringList(items []string) []byte {
	total := 0
	for _, s := range items {
		total += 1 + len(s)
	}
	out := make([]byte, 0, total)
	for _, s := range items {
		out = append(out, byte(len(s)))
		out = append(out, []byte(s)...)
	}
	return out
}

// DecodeStringList is the inverse of EncodeStringList.
func DecodeStringList(buf []byte) ([]string, error) {
	var out []string
	for i := 0; i < len(buf); {
		n := int(buf[i])
		i++
		if i+n > len(buf) {
			return nil, fmt.Errorf("improv: truncated string list at offset %d", i)
		}
		out = append(out, string(buf[i:i+n]))
		i += n
	}
	return out, nil
}

// ParseSendWiFi extracts SSID and password from the Data section of a
// Send Wi-Fi Settings RPC (command 0x01). Layout:
//   [0]  SSID length
//   [1..]SSID bytes
//   [n]  password length
//   [n+1..]password bytes
func ParseSendWiFi(data []byte) (ssid, password string, err error) {
	if len(data) < 1 {
		return "", "", fmt.Errorf("improv: missing SSID length")
	}
	ssidLen := int(data[0])
	if 1+ssidLen > len(data) {
		return "", "", fmt.Errorf("improv: truncated SSID")
	}
	ssid = string(data[1 : 1+ssidLen])

	rest := data[1+ssidLen:]
	if len(rest) < 1 {
		return "", "", fmt.Errorf("improv: missing password length")
	}
	passLen := int(rest[0])
	if 1+passLen > len(rest) {
		return "", "", fmt.Errorf("improv: truncated password")
	}
	password = string(rest[1 : 1+passLen])
	return ssid, password, nil
}

// EncodeSendWiFi builds the Data section of a Send Wi-Fi Settings RPC.
// Useful for tests and for the serial protocol's client side.
func EncodeSendWiFi(ssid, password string) []byte {
	out := make([]byte, 0, 2+len(ssid)+len(password))
	out = append(out, byte(len(ssid)))
	out = append(out, []byte(ssid)...)
	out = append(out, byte(len(password)))
	out = append(out, []byte(password)...)
	return out
}
