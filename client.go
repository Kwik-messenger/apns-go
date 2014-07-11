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
	callback    BadMessageCallback

	gatePort int

	msgQueue    chan *Message
	deadWorkers chan deadWorker
}

func CreateClient(gw string, cert, certKey []byte) (*Client, error) {
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
	return &Client{hostname, tlsCert, nil, port, messages, deadWorkers}, nil
}

// management api

// SetCallback sets callback for handling erroneous messages
func (c *Client) SetCallback(cb BadMessageCallback) {
	c.callback = cb
}

// Start client, return error if connection to APNS does not succeed
func (c *Client) Start(workerCount int) error {
	// for every gateway addr spawn appropriate number of workers

	for i := 0; i < workerCount; i++ {
		var conn net.Conn
		var err error

		for j := 0; j < WORKER_RETRIES; j++ {
			conn, err = c.connect(c.GatewayName, c.gatePort, c.certificate)
			if err == nil {
				break
			}
		}

		if err != nil {
			return err
		}

		worker := newWorker(c.msgQueue, c.deadWorkers)
		go worker.Run(conn)
	}

	// start worker respawner
	go c.respawnWorkers()

	// TODO: communicate with apple feedback service
	return nil
}

// Stop client workers gracefully
func (c *Client) Stop() error {
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

func (c *Client) connect(name string, port int, cert tls.Certificate) (*tls.Conn,
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
	tlsConf := &tls.Config{ServerName: name, Certificates: []tls.Certificate{cert}}
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
	for {
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
			// respawn worker
			var conn net.Conn
			var err error
			for i := 0; i < WORKER_RETRIES; i++ {
				conn, err = c.connect(c.GatewayName, c.gatePort, c.certificate)
				if err == nil {
					break
				}
			}

			if err != nil {
				// reconnection failed. Resend worker messages
				for m, ok := dw.w.Next(); ok; m, ok = dw.w.Next() {
					c.send(m)
				}

				// wait for some time and reconnect
				go func(w deadWorker) {
					w.err = nil
					time.Sleep(WORKER_RESSURECT_TIMEOUT)
					c.deadWorkers <- w
				}(dw)
				continue
			}

			go dw.w.Run(conn)
		}
	}
}

type deadWorker struct {
	w   *worker
	err error
}
