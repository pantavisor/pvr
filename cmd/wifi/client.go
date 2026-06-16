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

package wifi

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"tinygo.org/x/bluetooth"
)

// ImprovDevice describes an Improv-capable BLE device observed during a scan.
type ImprovDevice struct {
	Address      string
	Name         string
	RSSI         int16
	State        byte // 0 if unknown (service data not present)
	Capabilities byte
	HasState     bool
}

// Client wraps the local BLE adapter and a single connected Improv device.
type Client struct {
	adapter *bluetooth.Adapter

	mu sync.Mutex
	// device is a pointer (not a value) because tinygo/bluetooth's
	// Device contains a context.CancelFunc on some platforms (Windows),
	// which makes the struct non-comparable — we can't `== Device{}`
	// to detect "not connected". nil means no active connection.
	device  *bluetooth.Device
	chars   map[string]bluetooth.DeviceCharacteristic // keyed by UUID string
	stateCh chan byte
	errCh   chan byte
	resCh   chan RPCPacket
}

// NewClient enables the default adapter and returns a ready-to-use Client.
func NewClient() (*Client, error) {
	adapter := bluetooth.DefaultAdapter
	if err := adapter.Enable(); err != nil {
		return nil, fmt.Errorf("enable BLE adapter: %w", err)
	}
	return &Client{
		adapter: adapter,
		chars:   map[string]bluetooth.DeviceCharacteristic{},
	}, nil
}

// improvServiceUUID returns the tinygo UUID for the Improv GATT service.
func improvServiceUUID() bluetooth.UUID {
	u, _ := bluetooth.ParseUUID(ImprovServiceUUID)
	return u
}

// advServiceUUID16 returns the short-form UUID used in advertisements
// (`00004677-0000-1000-8000-00805f9b34fb`, short 0x4677).
func advServiceUUID16() bluetooth.UUID {
	u, _ := bluetooth.ParseUUID(ImprovAdvServiceUUID)
	return u
}

// Scan streams Improv devices to onResult until ctx is done. A device is
// considered Improv if it advertises the Improv full service UUID or the
// short-form (0x4677) service data. Duplicates are suppressed; the most
// recently observed RSSI / service data wins.
func (c *Client) Scan(ctx context.Context, onResult func(ImprovDevice)) error {
	seen := map[string]ImprovDevice{}
	var seenMu sync.Mutex

	done := make(chan error, 1)
	go func() {
		err := c.adapter.Scan(func(_ *bluetooth.Adapter, r bluetooth.ScanResult) {
			dev, ok := parseImprovAdv(r)
			if !ok {
				return
			}
			seenMu.Lock()
			prev, existed := seen[dev.Address]
			if existed {
				// Prefer the record that carries state/caps data.
				if prev.HasState && !dev.HasState {
					dev.State = prev.State
					dev.Capabilities = prev.Capabilities
					dev.HasState = true
				}
				if dev.Name == "" {
					dev.Name = prev.Name
				}
			}
			seen[dev.Address] = dev
			seenMu.Unlock()
			onResult(dev)
		})
		done <- err
	}()

	select {
	case <-ctx.Done():
		_ = c.adapter.StopScan()
		<-done // drain
		return nil
	case err := <-done:
		return err
	}
}

// parseImprovAdv returns (device, true) if the scan result matches the Improv
// advertising pattern. It prefers the 0x4677 service data payload (state+caps)
// but falls back to the full service UUID for devices that omit service data.
func parseImprovAdv(r bluetooth.ScanResult) (ImprovDevice, bool) {
	dev := ImprovDevice{
		Address: strings.ToUpper(r.Address.String()),
		Name:    r.LocalName(),
		RSSI:    r.RSSI,
	}

	if sd := r.ServiceData(); len(sd) > 0 {
		short := advServiceUUID16()
		for _, el := range sd {
			if !el.UUID.Is16Bit() {
				// Some backends expose the same data under the 128-bit form.
				if el.UUID == short {
					applyImprovServiceData(&dev, el.Data)
					return dev, true
				}
				continue
			}
			// 16-bit UUIDs compare via Get16Bit().
			if el.UUID.Get16Bit() == 0x4677 {
				applyImprovServiceData(&dev, el.Data)
				return dev, true
			}
		}
	}

	if r.HasServiceUUID(improvServiceUUID()) || r.HasServiceUUID(advServiceUUID16()) {
		return dev, true
	}
	return ImprovDevice{}, false
}

func applyImprovServiceData(dev *ImprovDevice, data []byte) {
	if len(data) >= 1 {
		dev.State = data[0]
		dev.HasState = true
	}
	if len(data) >= 2 {
		dev.Capabilities = data[1]
	}
}

// Connect opens a BLE connection to address, discovers the Improv service, and
// caches the five characteristic handles. State/error/result notifications
// are delivered on channels exposed by StateEvents/ErrorEvents/ResultEvents.
func (c *Client) Connect(ctx context.Context, address string) error {
	addr, err := parseAddress(address)
	if err != nil {
		return err
	}

	connectCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	// On Linux+BlueZ, adapter.Connect reads properties on the device object
	// path directly; if BlueZ hasn't discovered the device this invocation,
	// that path doesn't exist and Properties.Get fails. A brief targeted
	// discovery ensures BlueZ has the device registered before Connect.
	if err := c.ensureDiscovered(connectCtx, address); err != nil {
		return fmt.Errorf("discover before connect: %w", err)
	}

	type connectResult struct {
		dev bluetooth.Device
		err error
	}
	ch := make(chan connectResult, 1)
	go func() {
		d, e := c.adapter.Connect(addr, bluetooth.ConnectionParams{})
		ch <- connectResult{d, e}
	}()

	var dev bluetooth.Device
	select {
	case <-connectCtx.Done():
		go func() {
			r := <-ch
			if r.err == nil {
				_ = r.dev.Disconnect()
			}
		}()
		return fmt.Errorf("connect timeout: %w", connectCtx.Err())
	case r := <-ch:
		if r.err != nil {
			return fmt.Errorf("connect: %w", r.err)
		}
		dev = r.dev
	}

	services, err := dev.DiscoverServices([]bluetooth.UUID{improvServiceUUID()})
	if err != nil {
		_ = dev.Disconnect()
		return fmt.Errorf("discover Improv service: %w", err)
	}
	if len(services) == 0 {
		_ = dev.Disconnect()
		return errors.New("device does not expose the Improv service")
	}

	wantedUUIDs := []string{
		CurrentStateCharUUID,
		ErrorStateCharUUID,
		RPCCommandCharUUID,
		RPCResultCharUUID,
		CapabilitiesCharUUID,
	}
	wantedParsed := make([]bluetooth.UUID, len(wantedUUIDs))
	for i, s := range wantedUUIDs {
		u, err := bluetooth.ParseUUID(s)
		if err != nil {
			_ = dev.Disconnect()
			return fmt.Errorf("parse UUID %s: %w", s, err)
		}
		wantedParsed[i] = u
	}

	chars, err := services[0].DiscoverCharacteristics(wantedParsed)
	if err != nil {
		_ = dev.Disconnect()
		return fmt.Errorf("discover characteristics: %w", err)
	}

	c.mu.Lock()
	c.device = &dev
	// Notification channels are recreated on every Connect and replaced;
	// old channels are garbage-collected once no goroutines reference them.
	c.stateCh = make(chan byte, 8)
	c.errCh = make(chan byte, 8)
	c.resCh = make(chan RPCPacket, 32)
	for _, ch := range chars {
		c.chars[strings.ToLower(ch.UUID().String())] = ch
	}
	c.mu.Unlock()

	if err := c.subscribe(); err != nil {
		_ = dev.Disconnect()
		return err
	}
	return nil
}

func (c *Client) subscribe() error {
	stateChar, ok := c.char(CurrentStateCharUUID)
	if !ok {
		return errors.New("missing Current State characteristic")
	}
	if err := stateChar.EnableNotifications(func(buf []byte) {
		if len(buf) < 1 {
			return
		}
		select {
		case c.stateCh <- buf[0]:
		default:
			fmt.Fprintf(os.Stderr, "warning: state event dropped (channel full)\n")
		}
	}); err != nil {
		return fmt.Errorf("enable state notifications: %w", err)
	}

	errChar, ok := c.char(ErrorStateCharUUID)
	if !ok {
		return errors.New("missing Error State characteristic")
	}
	if err := errChar.EnableNotifications(func(buf []byte) {
		if len(buf) < 1 {
			return
		}
		select {
		case c.errCh <- buf[0]:
		default:
			fmt.Fprintf(os.Stderr, "warning: error event dropped (channel full)\n")
		}
	}); err != nil {
		return fmt.Errorf("enable error notifications: %w", err)
	}

	resChar, ok := c.char(RPCResultCharUUID)
	if !ok {
		return errors.New("missing RPC Result characteristic")
	}
	if err := resChar.EnableNotifications(func(buf []byte) {
		pkt, err := DecodeRPCPacket(buf)
		if err != nil {
			return
		}
		select {
		case c.resCh <- pkt:
		default:
			fmt.Fprintf(os.Stderr, "warning: result event dropped (channel full)\n")
		}
	}); err != nil {
		return fmt.Errorf("enable result notifications: %w", err)
	}
	return nil
}

// char returns the cached characteristic for the given UUID string, if any.
func (c *Client) char(uuid string) (bluetooth.DeviceCharacteristic, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch, ok := c.chars[strings.ToLower(uuid)]
	return ch, ok
}

// Capabilities reads the Capabilities characteristic once.
func (c *Client) Capabilities() (byte, error) {
	ch, ok := c.char(CapabilitiesCharUUID)
	if !ok {
		return 0, errors.New("capabilities characteristic not discovered")
	}
	buf := make([]byte, 1)
	if _, err := ch.Read(buf); err != nil {
		return 0, err
	}
	return buf[0], nil
}

// StateEvents/ErrorEvents/ResultEvents expose the notification streams.
func (c *Client) StateEvents() <-chan byte       { return c.stateCh }
func (c *Client) ErrorEvents() <-chan byte       { return c.errCh }
func (c *Client) ResultEvents() <-chan RPCPacket { return c.resCh }

// SendRPC writes an encoded RPC packet (with checksum) to the command char.
func (c *Client) SendRPC(pkt RPCPacket) error {
	ch, ok := c.char(RPCCommandCharUUID)
	if !ok {
		return errors.New("RPC command characteristic not discovered")
	}
	if _, err := ch.WriteWithoutResponse(pkt.Encode()); err != nil {
		return fmt.Errorf("write RPC command: %w", err)
	}
	return nil
}

// Disconnect tears down the current BLE connection, if any.
func (c *Client) Disconnect() error {
	c.mu.Lock()
	d := c.device
	c.device = nil
	c.chars = map[string]bluetooth.DeviceCharacteristic{}
	c.mu.Unlock()
	if d == nil {
		return nil
	}
	return d.Disconnect()
}

// ensureDiscovered runs a short BLE scan until the target address is seen
// (or ctx is done). It's a workaround for tinygo/bluetooth on Linux, which
// cannot connect to a device BlueZ hasn't added to its object tree.
func (c *Client) ensureDiscovered(ctx context.Context, address string) error {
	target := strings.ToUpper(address)
	found := make(chan struct{}, 1)
	scanDone := make(chan error, 1)

	go func() {
		scanDone <- c.adapter.Scan(func(_ *bluetooth.Adapter, r bluetooth.ScanResult) {
			if strings.EqualFold(r.Address.String(), target) {
				select {
				case found <- struct{}{}:
				default:
				}
			}
		})
	}()

	select {
	case <-found:
		_ = c.adapter.StopScan()
		<-scanDone
		return nil
	case <-ctx.Done():
		_ = c.adapter.StopScan()
		<-scanDone
		return ctx.Err()
	case err := <-scanDone:
		return err
	}
}

// parseAddress converts a "AA:BB:CC:DD:EE:FF" string to bluetooth.Address.
// (On macOS bluetooth.Address uses a UUID under the hood, but tinygo still
// exposes a MAC-shaped type on all platforms for parsing.)
func parseAddress(s string) (bluetooth.Address, error) {
	mac, err := bluetooth.ParseMAC(s)
	if err != nil {
		return bluetooth.Address{}, fmt.Errorf("parse address %q: %w", s, err)
	}
	var a bluetooth.Address
	a.MAC = mac
	return a, nil
}
