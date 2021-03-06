// Copyright (c) 2019-2020 IrineSistiana
//
// Permission is hereby granted, free of charge, to any person obtaining a copy of
// this software and associated documentation files (the "Software"), to deal in
// the Software without restriction, including without limitation the rights to
// use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of
// the Software, and to permit persons to whom the Software is furnished to do so,
// subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS
// FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR
// COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER
// IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN
// CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.

package core

import (
	"sync"

	"github.com/xtaci/smux"
)

type muxSession struct {
	mu sync.Mutex
	*smux.Session
}

type muxStream struct {
	*smux.Stream
	onClose func() error
}

func newMuxSession(s *smux.Session) *muxSession {
	return &muxSession{Session: s}
}

func (s *muxSession) openStream(maxStreamLimit int) (*muxStream, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.NumStreams() >= maxStreamLimit {
		return nil, ErrTooManyStreams
	}

	stream, err := s.OpenStream()
	if err != nil {
		return nil, err
	}
	return &muxStream{Stream: stream, onClose: s.tryCloseOnIdle}, nil
}

func (s *muxSession) tryCloseOnIdle() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.NumStreams() == 0 {
		return s.Close()
	}
	return nil
}

func (s *muxStream) Close() error {
	s.Stream.Close()
	// tryCloseOnIdle
	return s.onClose()
}
