package apns

import (
	"crypto/tls"
	"errors"
	"net"
	"strconv"
	"strings"
)

const (
	WORKERS_PER_HOST = 1
	CLIENT_QUEUE_LENGTH = 32
	RESPAWN_QUEUE_LENGTH = 4
	APNS_DEFAULT_PORT = 2195
)

var (
	ErrNoIP = errors.New("no ip addresses for hostname found")
	ErrNoAliveGateways = errors.New("no alive gateways")

	ErrInvalidPriority = errors.New("invalid priority")
)

type Client struct {
	GatewayName string
	certificate tls.Certificate

	gateAddresses []net.IP
	gatePort      int

	msgQueue chan *message
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

	messages := make(chan *message, CLIENT_QUEUE_LENGTH)
	deadWorkers := make(chan deadWorker, RESPAWN_QUEUE_LENGTH)
	return &Client{gw, tlsCert, addrs, port, messages, deadWorkers}, nil
}

// management api

// Start client, return error if connection to APNS does not succeed
func (c *Client) Start() error {
	// for every gateway addr spawn appropriate number of workers
	var aliveAddrs []net.IP

createWorkers:
	for _, ip := range c.gateAddresses {
		for i := 0; i < WORKERS_PER_HOST; i++ {
			conn, err := c.connect(c.GatewayName, ip, c.gatePort, c.certificate)
			if err != nil {
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

	message := newMessage(token, expiry, priority, data)
	return c.send(message)
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

func (c *Client) send(m *message) error {
	c.msgQueue <- m
	return nil
}

func (c *Client) respawnWorkers() {

}

type deadWorker struct {
	w *worker
	err error
	ip net.IP
}
