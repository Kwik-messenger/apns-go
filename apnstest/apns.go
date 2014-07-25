package apnstest

import (
	"crypto/tls"
	"io"
	"io/ioutil"
	"net"
)

var stubAPNSSocket net.Listener

func justReadAPNSConn(conn io.ReadWriteCloser) {
	io.Copy(ioutil.Discard, conn)
}


func RunStubAPNSGate(certpath, keypath string) {
	cert, err := tls.LoadX509KeyPair(certpath, keypath)
	if err != nil {
		panic(err)
	}

	cfg := &tls.Config{Certificates: []tls.Certificate{cert}}

	stubAPNSSocket, err = tls.Listen("tcp4", "127.0.0.1:2195", cfg)
	if err != nil {
		panic(err)
	}

	for {
		conn, err := stubAPNSSocket.Accept()
		if err != nil {
			return
		}

		go justReadAPNSConn(conn)
	}
}

func StopStubAPNSGate() {
	stubAPNSSocket.Close()
	stubAPNSSocket = nil
}
