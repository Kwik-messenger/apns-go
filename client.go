package apns

import (
	"crypto/tls"
	"errors"
	"net"
	"strconv"
	"strings"
)

const (
	WORKERS_PER_HOST     = 1
	CLIENT_QUEUE_LENGTH  = 32
	RESPAWN_QUEUE_LENGTH = 4
	APNS_DEFAULT_PORT    = 2195
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

	gateAddresses []net.IP
	gatePort      int

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

	addrs, err := net.LookupIP(hostname)
	if err != nil {
		return nil, err
	}

	if len(addrs) == 0 {
		return nil, ErrNoIP
	}

	messages := make(chan *Message, CLIENT_QUEUE_LENGTH)
	deadWorkers := make(chan deadWorker, RESPAWN_QUEUE_LENGTH)
	return &Client{hostname, tlsCert, nil, addrs, port, messages, deadWorkers}, nil
}

// management api

// SetCallback sets callback for handling erroneous messages
func (c *Client) SetCallback(cb BadMessageCallback) {
	c.callback = cb
}

// Start client, return error if connection to APNS does not succeed
func (c *Client) Start() error {
	// for every gateway addr spawn appropriate number of workers
	var aliveAddrs []net.IP

createWorkers:
	for _, ip := range c.gateAddresses {
		for i := 0; i < WORKERS_PER_HOST; i++ {
			conn, err := c.connect(c.GatewayName, ip, c.gatePort, c.certificate)
			if err != nil {
				println("client: failed to connect to", ip.String(), ":", err.Error())
				continue createWorkers
			}

			worker := newWorker(c.msgQueue, c.deadWorkers)
			go worker.Run(conn, ip)
		}

		aliveAddrs = append(aliveAddrs, ip)
	}

	if len(aliveAddrs) == 0 {
		return ErrNoAliveGateways
	}

	c.gateAddresses = aliveAddrs

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

func (c *Client) connect(name string, ip net.IP, port int, cert tls.Certificate) (*tls.Conn,
	error) {

	// dial to host
	tcpaddr := &net.TCPAddr{IP: ip, Port: port}
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
			// TODO: manage ips
			var ip = dw.ip
			conn, err := c.connect(c.GatewayName, ip, c.gatePort, c.certificate)
			if err != nil {
				// TODO: handle reconnect errors
			}
			go dw.w.Run(conn, ip)
		}
	}
}

type deadWorker struct {
	w   *worker
	err error
	ip  net.IP
}
