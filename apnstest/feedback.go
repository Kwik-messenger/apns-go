package apnstest

import (
	"bufio"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net"

	"github.com/Kwik-messenger/apns-go"
)

const (
	FEEDBACK_BIND_ADDRESS = "127.0.0.1:2196"

	FRAME_TOKEN_LENGTH = 32
)

var stubFeedbackListener net.Listener

func sendBadTokens(conn net.Conn, tokens []apns.BadToken) {
	var length = []byte{0, 32}

	defer conn.Close()

	writer := bufio.NewWriter(conn)

	for _, token := range tokens {
		byteToken, _ := unpackToken(token.Token)
		timestamp := uint32(token.Timestamp.Unix())
		binary.Write(writer, binary.BigEndian, timestamp)
		writer.Write(length)
		writer.Write(byteToken)

		writer.Flush()
	}
}

func RunStubFeedbackService(certpath, keypath string, badTokens []apns.BadToken) {
	cert, err := tls.LoadX509KeyPair(certpath, keypath)
	if err != nil {
		panic(err)
	}

	cfg := &tls.Config{Certificates: []tls.Certificate{cert}}

	stubFeedbackListener, err = tls.Listen("tcp4", FEEDBACK_BIND_ADDRESS, cfg)
	if err != nil {
		panic(err)
	}

	// validate tokens on startup
	for _, badToken := range badTokens {
		_, err := unpackToken(badToken.Token)
		if err != nil {
			panic(err)
		}
	}

	for {
		conn, err := stubFeedbackListener.Accept()
		if err != nil {
			return
		}

		go sendBadTokens(conn, badTokens)
	}
}

func StopStubFeedbackService() {
	stubFeedbackListener.Close()
	stubFeedbackListener = nil
}

func unpackToken(token string) ([]byte, error) {
	data, err := hex.DecodeString(token)
	if err != nil {
		return nil, err
	}

	if uint16(len(data)) != FRAME_TOKEN_LENGTH {
		return nil, errors.New("invalid bad token")
	}

	return data, nil
}
