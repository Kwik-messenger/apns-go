package apns

import (
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"net"
	"time"
)

var (
	ErrFeedbackClientStopped = errors.New("feedback client have been stopped")
)

type FeedbackClient struct {
	cert tls.Certificate
	hostname string
	port int
	anyCert bool

	ticker *time.Ticker

	tokens chan []BadToken
	stop chan struct{}
}

type BadToken struct {
	Token string
	Timestamp time.Time
}

func NewFeedbackClient(cert tls.Certificate, hostname string, port int, poll time.Duration,
	anyCert bool) *FeedbackClient {
	return &FeedbackClient{cert, hostname, port, anyCert, time.NewTicker(poll),
		make(chan []BadToken), make(chan struct{})}
}

func (fc *FeedbackClient) Start() error {
	_, err := net.ResolveIPAddr("ip4", fc.hostname)
	if err != nil {
		return err
	}

	go fc.serve()
	return nil
}

func (fc *FeedbackClient) Stop() {
	fc.ticker.Stop()
	close(fc.stop)
}

func (fc *FeedbackClient) GetBadTokens() ([]BadToken, error) {
	tokens, ok := <-fc.tokens
	if !ok {
		return nil, ErrFeedbackClientStopped
	}
	return tokens, nil
}

func (fc *FeedbackClient) serve() {
	for {
		select {
		case <-fc.ticker.C:
			// connect and recieve tokens from gate
			tokens, err := fc.recvTokens()
			log.Println("apns-feedback:", tokens, err)
			if err != nil {
				// TODO: log errors
				continue
			}
			if len(tokens) > 0 {
				fc.tokens <- tokens
			}

		case <-fc.stop:
			close(fc.tokens)
			return
		}
	}
}

func (fc *FeedbackClient) recvTokens() ([]BadToken, error) {
	// dial to host
	ipaddr, err := net.ResolveIPAddr("ip4", fc.hostname)
	if err != nil {
		return nil, err
	}

	tcpaddr := &net.TCPAddr{IP: ipaddr.IP, Port: fc.port}
	conn, err := net.DialTCP("tcp4", nil, tcpaddr)
	if err != nil {
		return nil, err
	}

	// initiate tls connection
	tlsConf := &tls.Config{ServerName: fc.hostname, Certificates: []tls.Certificate{fc.cert},
		InsecureSkipVerify: fc.anyCert}
	tlsConn := tls.Client(conn, tlsConf)
	defer tlsConn.Close()

	err = tlsConn.Handshake()
	if err != nil {
		return nil, err
	}

	return fc.readTokens(tlsConn)
}

func (fc *FeedbackClient) readTokens(conn net.Conn) ([]BadToken, error) {
	var buf = make([]byte, 34)
	var timestamp uint32

	var tokens []BadToken

	var err error

	for {
		// read timestamp
		err = binary.Read(conn, binary.BigEndian, &timestamp)
		if err != nil {
			if err == io.EOF {
				return tokens, nil
			}
			return nil, err
		}

		// read token (including length buf which always 32)
		_, err = conn.Read(buf)
		if err != nil {
			return nil, err
		}

		// make bad token record
		token := hex.EncodeToString(buf[2:])
		badToken := BadToken{token, time.Unix(int64(timestamp), 0)}
		tokens = append(tokens, badToken)
	}
	return tokens, nil
}
