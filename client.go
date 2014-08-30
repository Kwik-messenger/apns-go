package apns

import (
	"crypto/tls"
	"errors"
	"net"
	"strconv"
	"strings"
	"time"
)

const (
	WORKER_RETRIES           = 3
	WORKER_RESSURECT_TIMEOUT = 15 * time.Second
	CLIENT_QUEUE_LENGTH      = 32
	RESPAWN_QUEUE_LENGTH     = 4
	APNS_DEFAULT_PORT        = 2195
	FEEDBACK_DEFAULT_PORT    = 2196
)

var (
	ErrNoIP            = errors.New("no ip addresses for hostname found")
	ErrNoAliveGateways = errors.New("no alive gateways")

	ErrInvalidPriority = errors.New("invalid priority")
)

type BadMessageCallback func(m *Message, code uint8)

type Client struct {
	GatewayName string
	certificate tls.Certificate
	anyServerCert bool
	callback    BadMessageCallback

	gatePort int

	msgQueue    chan *Message
	workers     []*worker
	deadWorkers chan deadWorker
	stopped     chan struct{}
}

func CreateClient(gw string, cert, certKey []byte, anyServerCert bool) (*Client, error) {
	tlsCert, err := tls.X509KeyPair(cert, certKey)
	if err != nil {
		return nil, err
	}

	var port int

	parts := strings.Split(gw, ":")
	hostname := parts[0]
	if len(parts) == 1 {
		port = APNS_DEFAULT_PORT
	} else {
		port, err = strconv.Atoi(parts[1])
		if err != nil {
			return nil, err
		}
	}

	messages := make(chan *Message, CLIENT_QUEUE_LENGTH)
	deadWorkers := make(chan deadWorker, RESPAWN_QUEUE_LENGTH)
	stopped := make(chan struct{})
	return &Client{hostname, tlsCert, anyServerCert, nil, port, messages, nil, deadWorkers,
		stopped}, nil
}

// management api

// SetCallback sets callback for handling erroneous messages
func (c *Client) SetCallback(cb BadMessageCallback) {
	c.callback = cb
}

func (c *Client) SetupFeedback(gw string, poll time.Duration) (*FeedbackClient, error) {
	parts := strings.Split(gw, ":")
	hostname := parts[0]
	var port int
	var err error
	if len(parts) == 1 {
		port = FEEDBACK_DEFAULT_PORT
	} else {
		port, err = strconv.Atoi(parts[1])
		if err != nil {
			return nil, err
		}
	}

	return NewFeedbackClient(c.certificate, hostname, port, poll, c.anyServerCert), nil
}

// Start client, return error if connection to APNS does not succeed
func (c *Client) Start(workerCount int) error {

	if workerCount <= 0 {
		return errors.New("apns: worker count must be greater than zero")
	}

	for i := 0; i < workerCount; i++ {
		var conn net.Conn
		var err error

		for j := 0; j < WORKER_RETRIES; j++ {
			conn, err = c.connect(c.GatewayName, c.gatePort, c.certificate, c.anyServerCert)
			if err == nil {
				break
			}
		}

		if err != nil {
			// stop previous workers
			for _, w := range c.workers {
				w.ForceStop()
			}
			c.workers = nil
			return err
		}

		worker := newWorker(c.msgQueue, c.deadWorkers)
		c.workers = append(c.workers, worker)
		go worker.Run(conn)
	}

	// start worker respawner
	go c.respawnWorkers()

	// TODO: communicate with apple feedback service
	return nil
}

// Stop client gracefully. Users should not send messages after invoking Stop; those calls will 
// result in panic. Pending messages will be sent to APNS as usual (callbacks will be invoked for
// malformed messages). Stop call will return when all pending messages assumed delivered or 
// appropriate callbacks are invoked
func (c *Client) Stop() error {
	close(c.msgQueue)
	<-c.stopped
	return nil
}

// client api

// Send adds messate to queue and then sends it. Return error if message is not valid
func (c *Client) Send(to string, expiry int32, priority uint8,
	payload map[string]interface{}) error {

	// validate all input and form new message
	data, err := marshalPayload(payload)
	if err != nil {
		return err
	}

	token, err := unpackToken(to)
	if err != nil {
		return err
	}

	if priority != PRIORITY_IMMEDIATE && priority != PRIORITY_DELAYED {
		return ErrInvalidPriority
	}

	Message := newMessage(token, expiry, priority, data)
	return c.send(Message)
}

// internal methods

func (c *Client) connect(name string, port int, cert tls.Certificate, anyCert bool) (*tls.Conn,
	error) {

	// dial to host
	ipaddr, err := net.ResolveIPAddr("ip4", name)
	if err != nil {
		return nil, err
	}

	tcpaddr := &net.TCPAddr{IP: ipaddr.IP, Port: port}
	conn, err := net.DialTCP("tcp4", nil, tcpaddr)
	if err != nil {
		return nil, err
	}

	// close connection if any error occur afterwards
	defer func() {
		if err != nil {
			conn.Close()
		}
	}()

	// initiate tls connection
	tlsConf := &tls.Config{ServerName: name, Certificates: []tls.Certificate{cert},
		InsecureSkipVerify: anyCert}
	tlsConn := tls.Client(conn, tlsConf)

	err = tlsConn.Handshake()
	if err != nil {
		return nil, err
	}

	return tlsConn, nil
}

func (c *Client) send(m *Message) error {
	c.msgQueue <- m
	return nil
}

func (c *Client) respawnWorkers() {
	var finishedWorkers int
	for {

		if finishedWorkers == len(c.workers) {
			// all workers finished, nothing to do
			close(c.stopped)
			return
		}

		select {
		case dw := <-c.deadWorkers:
			if mderr, ok := dw.err.(messageDeliveryError); ok {
				if mderr.code > 0 && mderr.code < 9 {
					// malformed message, pop it and send it back to caller
					if c.callback != nil {
						go c.callback(mderr.message, mderr.code)
					}
				}
			}

			if dw.err == nil {
				// worker just finished it work. Do not respawn it
				finishedWorkers++
				continue
			}

			// respawn worker
			var conn net.Conn
			var err error
			for i := 0; i < WORKER_RETRIES; i++ {
				conn, err = c.connect(c.GatewayName, c.gatePort, c.certificate, c.anyServerCert)
				if err == nil {
					break
				}
			}

			if err != nil {
				// reconnection failed. Resend worker messages
				for m, ok := dw.w.PopMessage(); ok; m, ok = dw.w.PopMessage() {
					c.send(m)
				}

				// wait for some time and reconnect
				go func(w deadWorker, err error) {
					w.err = err
					time.Sleep(WORKER_RESSURECT_TIMEOUT)
					c.deadWorkers <- w
				}(dw, err)
				continue
			}

			// reconnection succeed, run worker on new connection
			go dw.w.Run(conn)
		}
	}
}

type deadWorker struct {
	w   *worker
	err error
}
