//
// Copyright 2026  Pantacor Ltd.
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

package device

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// maxFrameBytes: the Hub takes at most 32 KiB per WebSocket frame.
const maxFrameBytes = 32 * 1024

// wsConn adapts an SSH session's WebSocket to a net.Conn: binary messages
// carry the raw SSH bytes both ways (docs/ssh.md). Writes are split into
// messages of at most maxFrameBytes. When the Hub closes the socket, Read
// answers io.EOF and EndReason the close reason (the session's end reason).
type wsConn struct {
	ws *websocket.Conn

	rmu sync.Mutex
	r   io.Reader

	wmu sync.Mutex

	mu     sync.Mutex
	reason string
	closed bool // the peer sent its close frame

	closeOnce sync.Once
}

func newWSConn(ws *websocket.Conn) *wsConn { return &wsConn{ws: ws} }

func (c *wsConn) Read(p []byte) (int, error) {
	c.rmu.Lock()
	defer c.rmu.Unlock()
	for {
		if c.r == nil {
			mt, r, err := c.ws.NextReader()
			if err != nil {
				return 0, c.readError(err)
			}
			if mt != websocket.BinaryMessage {
				continue // only binary messages carry SSH bytes
			}
			c.r = r
		}
		n, err := c.r.Read(p)
		if err == io.EOF {
			c.r = nil
			if n > 0 {
				return n, nil
			}
			continue
		}
		if err != nil {
			return n, c.readError(err)
		}
		return n, nil
	}
}

func (c *wsConn) readError(err error) error {
	var ce *websocket.CloseError
	if errors.As(err, &ce) {
		c.mu.Lock()
		c.closed = true
		if c.reason == "" {
			c.reason = ce.Text
			if c.reason == "" && ce.Code != websocket.CloseNormalClosure {
				c.reason = fmt.Sprintf("closed with code %d", ce.Code)
			}
		}
		c.mu.Unlock()
		return io.EOF
	}
	return err
}

// EndReason is the close reason the Hub gave, "" if it gave none (yet).
func (c *wsConn) EndReason() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reason
}

// ClosedByPeer: the Hub closed the socket.
func (c *wsConn) ClosedByPeer() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *wsConn) Write(p []byte) (int, error) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	written := 0
	for len(p) > 0 {
		chunk := p
		if len(chunk) > maxFrameBytes {
			chunk = chunk[:maxFrameBytes]
		}
		if err := c.ws.WriteMessage(websocket.BinaryMessage, chunk); err != nil {
			return written, err
		}
		written += len(chunk)
		p = p[len(chunk):]
	}
	return written, nil
}

// Close sends a normal close frame (which stops the session) and closes
// the connection.
func (c *wsConn) Close() error {
	var err error
	c.closeOnce.Do(func() {
		_ = c.ws.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
		err = c.ws.Close()
	})
	return err
}

func (c *wsConn) LocalAddr() net.Addr  { return c.ws.LocalAddr() }
func (c *wsConn) RemoteAddr() net.Addr { return c.ws.RemoteAddr() }

func (c *wsConn) SetDeadline(t time.Time) error {
	if err := c.ws.SetReadDeadline(t); err != nil {
		return err
	}
	return c.ws.SetWriteDeadline(t)
}

func (c *wsConn) SetReadDeadline(t time.Time) error  { return c.ws.SetReadDeadline(t) }
func (c *wsConn) SetWriteDeadline(t time.Time) error { return c.ws.SetWriteDeadline(t) }

// sshSubprotocol carries the WebSocket ticket (docs/ssh.md, "WebSocket
// tickets"): the user's token never goes into the handshake.
func sshSubprotocol(ticket string) string { return "ticket." + ticket }

// dialSSHSocket opens the session's WebSocket with its ticket.
func dialSSHSocket(ctx context.Context, u, ticket string, insecure bool) (*websocket.Conn, error) {
	proto := sshSubprotocol(ticket)
	d := websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: 30 * time.Second,
		Subprotocols:     []string{proto},
	}
	if insecure {
		d.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	ws, resp, err := d.DialContext(ctx, u, nil)
	if err != nil {
		if resp != nil {
			switch resp.StatusCode {
			case http.StatusUnauthorized:
				return nil, fmt.Errorf("opening the session's WebSocket: the Hub refused the ticket (HTTP 401)")
			case http.StatusConflict:
				return nil, fmt.Errorf("opening the session's WebSocket: a WebSocket is already attached (HTTP 409)")
			case http.StatusGone:
				return nil, fmt.Errorf("opening the session's WebSocket: the session has ended (HTTP 410)")
			}
			return nil, fmt.Errorf("opening the session's WebSocket: %v (HTTP %d)", err, resp.StatusCode)
		}
		return nil, fmt.Errorf("opening the session's WebSocket: %w", err)
	}
	if ws.Subprotocol() != proto {
		ws.Close()
		return nil, errors.New("opening the session's WebSocket: the Hub did not accept the ticket subprotocol")
	}
	return ws, nil
}
