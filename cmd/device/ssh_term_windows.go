//go:build go1.22 && windows

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
	"time"

	"golang.org/x/term"
)

// watchWindowSize polls the console's size (Windows has no SIGWINCH) and
// calls fn when it changes, until ctx is done or the returned stop is
// called.
func watchWindowSize(ctx context.Context, fd int, fn func(width, height int)) (stop func()) {
	done := make(chan struct{})
	go func() {
		w0, h0, _ := term.GetSize(fd)
		t := time.NewTicker(500 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-t.C:
				if w, h, err := term.GetSize(fd); err == nil && (w != w0 || h != h0) {
					w0, h0 = w, h
					fn(w, h)
				}
			}
		}
	}()
	return func() { close(done) }
}
