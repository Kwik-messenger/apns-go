package apns

import (
	"bufio"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"io"
	"net"
	"strings"
	"time"
)


// FeedbackClient is a client for APNS Feedback service. It provides API to retrieve
// device tokens that are no longer valid.
type FeedbackClient struct {
	cert tls.Certificate
	hostname string
	port int
	anyCert bool

	ticker *time.Ticker

	tokens chan badTokensReply
	stop chan struct{}
}

// BadToken represents entry recieved from feedback service. It contains token and expiration date.
type BadToken struct {
	// Hex-encoded device token
	Token string
	// Timestamp of token expiration date
	Timestamp time.Time
}

// badTokensReply is an internal struct for passing feedback service results through channel.
type badTokensReply struct {
	tokens []BadToken
	err error
}

// newFeedbackClient creates feedback client with given certificate, address and poll interval.
func newFeedbackClient(cert tls.Certificate, hostname string, port int, poll time.Duration,
	anyCert bool) *FeedbackClient {
	return &FeedbackClient{cert, hostname, port, anyCert, time.NewTicker(poll),
		make(chan badTokensReply), make(chan struct{})}
}

// Start starts a client. Will return error if cant resolve feedback service name.
func (fc *FeedbackClient) Start() error {
	_, err := net.ResolveIPAddr("ip4", fc.hostname)
	if err != nil {
		return err
	}

	go fc.serve()
	return nil
}

// Stop stops the client.
func (fc *FeedbackClient) Stop() {
	fc.ticker.Stop()
	close(fc.stop)
}

// GetBadTokens returns bad tokens retrieved from feedback service. It will block until feedback
// service sends some tokens or an error occurs.
func (fc *FeedbackClient) GetBadTokens() ([]BadToken, error) {
	reply, ok := <-fc.tokens
	if !ok {
		return nil, ErrFeedbackClientStopped
	}
	return reply.tokens, reply.err
}

// serve is main routine of feedback client.
func (fc *FeedbackClient) serve() {
	for {
		select {
		case <-fc.ticker.C:
			// connect and recieve tokens from gate
			tokens, err := fc.recvTokens()
			if err != nil {
				fc.tokens <- badTokensReply{nil, err}
			} else if len(tokens) > 0 {
				fc.tokens <- badTokensReply{tokens, nil}
			}

		case <-fc.stop:
			close(fc.tokens)
			return
		}
	}
}

// recvTokens makes connect to feedback service and read tokens from it.
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

	// FIXME: apple uses same certificate for push gw (hostname gateway.push.apple.com) and
	// feedback service (hostname feedback.push.apple.com)
	// thus, tls hostname check failing there
	// workaround: change hostname for tls
	tlsHostname := strings.Replace(fc.hostname, "feedback", "gateway", 1)
	// initiate tls connection
	tlsConf := &tls.Config{ServerName: tlsHostname, Certificates: []tls.Certificate{fc.cert},
		InsecureSkipVerify: fc.anyCert}
	tlsConn := tls.Client(conn, tlsConf)
	defer tlsConn.Close()

	err = tlsConn.Handshake()
	if err != nil {
		return nil, err
	}

	return fc.readTokens(tlsConn)
}

// readTokens reads tokens from connection.
func (fc *FeedbackClient) readTokens(conn io.Reader) ([]BadToken, error) {
	var buf = make([]byte, 34)
	var timestamp uint32

	var tokens []BadToken

	var err error

	reader := bufio.NewReader(conn)

	for {
		// read timestamp
		err = binary.Read(reader, binary.BigEndian, &timestamp)
		if err != nil {
			if err == io.EOF {
				return tokens, nil
			}
			return nil, err
		}

		// read token (including length buf which always 32)
		n, err := reader.Read(buf)
		if err != nil {
			if ! (n == 34 && err == io.EOF) {
				return nil, err
			}
		}

		// make bad token record
		token := hex.EncodeToString(buf[2:])
		badToken := BadToken{token, time.Unix(int64(timestamp), 0)}
		tokens = append(tokens, badToken)
	}
	return tokens, nil
}
