package apns

import (
	"crypto/tls"
	"net"
	"strconv"
	"strings"
	"time"
)

const (
	// How many times try to connect to APNS (prevents denial on dns failures).
	WORKER_RETRIES           = 3
	// Waits specified amount of time for next connection attempt.
	WORKER_RESSURECT_TIMEOUT = 15 * time.Second
	// Message queue length.
	CLIENT_QUEUE_LENGTH      = 32
	// Worker respawn queue length.
	RESPAWN_QUEUE_LENGTH     = 4
	// Default port for APNS.
	APNS_DEFAULT_PORT        = 2195
	// Default port for feedback service.
	FEEDBACK_DEFAULT_PORT    = 2196
)


// BadMessageCallback defines function type to call back on messages APNS fails to deliver.
type BadMessageCallback func(m *Message, code uint8)

// Client is an APNS client. It provides API to send messages, manages message queue,
// orchestrates workers. It is also used to instantiate feedback service client. 
type Client struct {
	gatewayName string
	certificate tls.Certificate
	anyServerCert bool
	callback    BadMessageCallback

	gatePort int

	msgQueue    chan *Message
	workers     []*worker
	deadWorkers chan deadWorker
	stopped     chan struct{}
}

// CreateClient creates Client, loads tls certificate. If parameter 'anyServerCert' is set to true,
// client will be able to connect to APNS gateway with invalid certificate. Can be used for
// testing purposes.
//
// After client has been created it should be started using Start() to actually send messages.
// But even if not having been started it still can be used - all messages will be queued and then 
// sent. It can be handy because workers on startup can take some time to connect to APNS.
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

// SetCallback sets callback for handling erroneous messages. Every call executed in its own
// goroutine. 
func (c *Client) SetCallback(cb BadMessageCallback) {
	c.callback = cb
}

// SetupFeedback setups feedback client from APNS client config. Feedback client will inherit 
// a certificate and 'anyServerCert' option. After client is created it needs to 
// be started to actually talk to feedback service.
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

	return newFeedbackClient(c.certificate, hostname, port, poll, c.anyServerCert), nil
}

// Start starts a client, returns error if connection to APNS does not succeed.
func (c *Client) Start(workerCount int) error {

	if workerCount <= 0 {
		return ErrInvalidWorkerCount
	}

	for i := 0; i < workerCount; i++ {
		var conn net.Conn
		var err error

		for j := 0; j < WORKER_RETRIES; j++ {
			conn, err = c.connect(c.gatewayName, c.gatePort, c.certificate, c.anyServerCert)
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

	return nil
}

// Stop stops client gracefully. Users should not send messages after invoking Stop; those calls 
// will result in panic. Pending messages will be sent to APNS as usual (callbacks will be invoked 
// for malformed messages). Stop call will return when all pending messages assumed delivered or 
// appropriate callbacks are invoked.
func (c *Client) Stop() error {
	close(c.msgQueue)
	<-c.stopped
	return nil
}

// client api

// Send enqueues message to delivery. 'to' is a hex-encoded device token, 'expiry' - expiration
// time of message in unix-time, 'priority' - either PRIORITY_IMMEDIATE for immediate notification
// delivery or PRIORITY_DELAYED for background delivery (it is an APNS property. It does not affect
// client's schedule for message), 'payload' - message payload.
//
// Send will make a message and put it into delivery queue. Thus, Send will return an error only in
// case of malformed input - invalid token, expiry, priority or payload. Send() to invalid but 
// well-formed token will succeed but later, when actual delivery fails, BadMessageCallback will
// be invoked.
//
// Client's delivery queue has limited capacity  and Send() will block if queue is full. This can 
// happen when workers are overloaded or not yet started.
//
// Send can accept messages before client has been started. Messages will be queued and sent after
// client startup.
//
// Send does not guarantee ordering of messages sent. Also keep in mind that APNS itself does not
// guarantee message delivery. However, every sent message either will be 'assumed delivered' to
// APNS or callback will be called.
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

// connect makes connection to APNS. 
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

// send enqueues message.
func (c *Client) send(m *Message) error {
	c.msgQueue <- m
	return nil
}

// respawnWorker waits for dead worker then reconnects to APNS and restarts worker.
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
				conn, err = c.connect(c.gatewayName, c.gatePort, c.certificate, c.anyServerCert)
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

// deadWorker is a struct to hold worker and error worker die with.
type deadWorker struct {
	w   *worker
	err error
}
