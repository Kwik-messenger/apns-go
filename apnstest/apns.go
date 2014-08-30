package apnstest

import (
	"crypto/tls"
	"io"
	"io/ioutil"
	"net"
)

const (
	APNS_BIND_ADDRESS = "127.0.0.1:2195"
)

var stubAPNSListener net.Listener

func justReadAPNSConn(conn io.ReadWriteCloser) {
	io.Copy(ioutil.Discard, conn)
}


func RunStubAPNSGate(certpath, keypath string) {
	cert, err := tls.LoadX509KeyPair(certpath, keypath)
	if err != nil {
		panic(err)
	}

	cfg := &tls.Config{Certificates: []tls.Certificate{cert}}

	stubAPNSListener, err = tls.Listen("tcp4", APNS_BIND_ADDRESS, cfg)
	if err != nil {
		panic(err)
	}

	for {
		conn, err := stubAPNSListener.Accept()
		if err != nil {
			return
		}

		go justReadAPNSConn(conn)
	}
}

func StopStubAPNSGate() {
	stubAPNSListener.Close()
	stubAPNSListener = nil
}
