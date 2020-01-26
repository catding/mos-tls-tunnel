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

package main

import (
	"context"
	"crypto/tls"
	"net"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/xtaci/smux"
)

const (
	muxCheckIdleInterval = time.Second * 2
	muxSessIdleTimeout   = time.Second * 30
)

var localTLSConfig *tls.Config
var wssURL string

func doLocal() {
	//checking
	if len(*path) == 0 {
		logrus.Fatal("bad url")
	}
	if len(*serverName) == 0 { //set serverName as remoteAddr
		serverName = remoteAddr //we already checked len(*remoteAddr) before
	}

	//init tls config
	localTLSConfig = new(tls.Config)
	localTLSConfig.InsecureSkipVerify = *insecureSkipVerify
	localTLSConfig.ServerName = *serverName
	var err error

	//init ws config
	if *enableWSS {
		wssURL = "wss://" + *serverName + *path
	}

	listenConfig := net.ListenConfig{Control: getControlFunc(defaultLeftTCPConfig)}
	listener, err := listenConfig.Listen(context.Background(), "tcp", *bindAddr)
	if err != nil {
		logrus.Fatalf("net.Listen: %v", err)
	}
	defer listener.Close()
	logrus.Printf("plugin listen at %s", listener.Addr())

	for {
		leftConn, err := listener.Accept()
		if err != nil {
			logrus.Fatalf("listener failed, %v", err)
		}
		logrus.Debugf("leftConn from %s accepted", leftConn.RemoteAddr())

		go forwardToServer(leftConn)
	}

}

func newRightConn() (net.Conn, error) {
	var rightConn net.Conn
	d := &net.Dialer{
		Control: getControlFunc(defaultRightTCPConfig),
		Timeout: handShakeTimeout,
	}

	if *enableWSS { // websocket enabled
		conn, err := dialWebsocketConn(d, wssURL, *remoteAddr, localTLSConfig)
		if err != nil {
			return nil, err
		}
		rightConn = conn
	} else {
		conn, err := tls.DialWithDialer(d, "tcp", *remoteAddr, localTLSConfig)
		if err != nil {
			return nil, err
		}
		rightConn = conn
	}
	return rightConn, nil
}

type smuxSessPool struct {
	pool sync.Map
}

var defaultSessPool = &smuxSessPool{}

func (p *smuxSessPool) dialNewSess() (*smux.Session, error) {
	rightConn, err := newRightConn()
	if err != nil {
		return nil, err
	}
	sess, err := smux.Client(rightConn, defaultSmuxConfig)
	if err != nil {
		rightConn.Close()
		return nil, err
	}

	// this go routine closes sess if it has been idle for a long time
	go func() {
		ticker := time.NewTicker(muxCheckIdleInterval)
		defer ticker.Stop()
		lastBusy := time.Now()
		for {
			if sess.IsClosed() {
				return
			}

			select {
			case now := <-ticker.C:
				if sess.NumStreams() > 0 {
					lastBusy = now
					continue
				}

				if now.Sub(lastBusy) > muxSessIdleTimeout {
					sess.Close()
					logrus.Debugf("sess %p closed, idle timeout", sess)
					return
				}
			}
		}
	}()

	p.pool.Store(sess, nil)
	logrus.Debugf("new sess %p opend", sess)
	return sess, nil
}

func (p *smuxSessPool) getStream() (*smux.Stream, error) {
	var stream *smux.Stream

	try := func(key, value interface{}) bool {
		sess := key.(*smux.Session)
		if sess.IsClosed() {
			p.pool.Delete(sess)
			logrus.Debugf("deleted closed sess %p", sess)
			return true
		}

		if sess.NumStreams() < *muxMaxStream {
			// try
			var er error
			stream, er = sess.OpenStream()
			if er != nil {
				p.pool.Delete(sess)
				logrus.Errorf("deleted err sess %p: open stream: %v", sess, er)
				return true
			}
			return false
		}
		return true
	}

	p.pool.Range(try)

	if stream == nil {
		sess, err := p.dialNewSess()
		if err != nil {
			return nil, err
		}
		return sess.OpenStream()
	}
	return stream, nil
}

func forwardToServer(leftConn net.Conn) {
	defer leftConn.Close()
	var rightConn net.Conn
	var err error

	if *enableMux {
		rightConn, err = defaultSessPool.getStream()
		if err != nil {
			logrus.Errorf("mux getStream: %v", err)
			return
		}
	} else {
		rightConn, err = newRightConn()
		if err != nil {
			logrus.Errorf("connect to remote: %v", err)
			return
		}
	}
	logrus.Debugf("rightConn from %s to %s established", leftConn.RemoteAddr(), rightConn.RemoteAddr())

	go openTunnel(rightConn, leftConn)
	openTunnel(leftConn, rightConn)
}
