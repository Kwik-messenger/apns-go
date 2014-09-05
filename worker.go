package apns

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"time"
)

const (
	// Timeout for messages to be 'assumed delivered' to APNS. After message being written into
	// APNS connection, worker will put it into inflight queue. If no errors was recieved from APNS
	// during that amount of time, message assumed delivered to APNS.
	WORKER_ASSUME_SENT_TIMEOUT = time.Second * 5
	// Initial buffer size for inflight messages.
	WORKER_INFLIGHT_QUEUE_SIZE = 128
	// Period to check inflight queue.
	WORKER_QUEUE_UPDATE_FREQ = time.Millisecond * 500

	// Long-living connections to APNS tend to stale and stop working but server does not close
	// them. Thus worker will die after that time of inactivity.
	WORKER_IDLE_TIMEOUT = time.Minute * 5
)

// worker holds worker state.
type worker struct {
	inbox     chan *Message
	toRespawn chan deadWorker

	stopChan chan struct{}

	counter  uint32
	inflight *messageFlightQueue
	buf      *bytes.Buffer
}

// newWorker creates worker
func newWorker(inbox chan *Message, toRespawn chan deadWorker) *worker {
	return &worker{inbox: inbox, inflight: newMessageFlightQueue(WORKER_INFLIGHT_QUEUE_SIZE),
		buf: &bytes.Buffer{}, toRespawn: toRespawn, stopChan: make(chan struct{})}
}

// Run runs worker main loop. It will consume messages from delivery queue and write them into
// APNS connection simultaneously polling connection for APNS error reply. If there were any errors
// APNS will tell the number of last delivered message, worker then skips up to last not delivered
// message and request client for reconnect. Then it deliver any pending messages in queue.
func (w *worker) Run(c net.Conn) {
	defer c.Close()

	var readErrors = make(chan error, 1)
	go w.runErrorReader(c, readErrors)

	ticker := time.NewTicker(WORKER_QUEUE_UPDATE_FREQ)
	defer ticker.Stop()
	idleTicker := time.NewTicker(WORKER_IDLE_TIMEOUT)
	defer idleTicker.Stop()

	var lastSentMessagesCount int

	// resend notifications, if any

	oldLength := w.inflight.Len()
	for i := 0; i < oldLength; i++ {
		m, _ := w.inflight.Pop()
		err := w.send(m, c)
		if err != nil {
			w.die(err)
			return
		}
	}

	for {
		select {
		case msg, ok := <-w.inbox:
			if !ok {
				// message queue is empty and closed. So, nullify it (to prevent further reads)
				w.inbox = nil
				continue
			}

			// got new message to send
			err := w.send(msg, c)
			if err != nil {
				w.die(err)
				return
			}
			lastSentMessagesCount++
		case now := <-ticker.C:
			var m *Message
			var ok bool
			for m, ok = w.inflight.Pop(); ok; m, ok = w.inflight.Pop() {
				// forget about messages that was sent WORKER_ASSUME_SENT_TIMEOUT ago
				// break when found new enough message
				if now.Sub(m.sentAt) < WORKER_ASSUME_SENT_TIMEOUT {
					w.inflight.Pushback()
					break
				}
			}

			if !ok && w.inbox == nil {
				// there no messages left in flight queue and inbox was closed, so
				// there no work to do, finish and report to registry
				w.die(nil)
				return
			}

		case err := <-readErrors:
			// something went wrong...
			switch err.(type) {
			case messageDeliveryError:
				// message failed to be delivered
				// skip up to last accepted message
				msgerr := err.(messageDeliveryError)
				if msgerr.messageID != 0 {
					for m, ok := w.inflight.Pop(); ok; m, ok = w.inflight.Pop() {
						if m.MessageID == msgerr.messageID {
							msgerr.message = m
							break
						}
					}
				}
			}
			w.die(err)
			return

		case <-idleTicker.C:
			if lastSentMessagesCount == 0 {
				// no messages was sent in that period, kill worker to reconnect
				w.die(ErrIdlingTimeout)
				return
			} else {
				lastSentMessagesCount = 0
			}
		case <-w.stopChan:
			return
		}
	}
}

// send writes message into connection.
func (w *worker) send(msg *Message, c net.Conn) error {
	defer w.buf.Reset()
	// give it an ID and add to FlightQueue
	w.counter++
	// if we fail to deliver very first message id should be 0
	// TODO: may be just close connection when counter is overflowed?
	if w.counter == 0 {
		w.counter = 1
	}

	msg.sentAt = time.Now()
	msg.MessageID = w.counter
	w.inflight.Push(msg)
	// send it over the wire
	_, err := msg.WriteTo(w.buf)
	if err != nil {
		// something wrong with message...
		return err
	}
	_, err = w.buf.WriteTo(c)
	if err != nil {
		// something wrong with connection
		return err
	}
	return nil
}

// PopMessage returns next 'infligh' message, stored in worker queue.
func (w *worker) PopMessage() (*Message, bool) {
	return w.inflight.Pop()
}

// Stop stops the worker. Connection will be closed, but not delivered messages would remain in
// internal buffer. Stop will not send worker into deadWorkers queue.
func (w *worker) ForceStop() {
	w.stopChan <- struct{}{}
}

// runErrorReader polls for error response from APNS.
func (w *worker) runErrorReader(conn net.Conn, errors chan error) {
	var buf = make([]byte, 2)

	_, err := conn.Read(buf)
	if err != nil {
		// read error. Connection corrupted or closed?
		errors <- err
		return
	}

	if buf[0] != 8 {
		// wtf?
		errors <- ErrInvalidAppleResponse
		return
	}

	errorCode := uint8(buf[1])
	var messageID uint32
	err = binary.Read(conn, binary.BigEndian, &messageID)
	if err != nil {
		errors <- err
		return
	}

	errors <- messageDeliveryError{errorCode, messageID, nil}
}

// die sends worker with error to client.
func (w *worker) die(err error) {
	w.toRespawn <- deadWorker{w, err}
}

// messageDeliveryError is a custom error type for handling delivery errors.
type messageDeliveryError struct {
	code      uint8
	messageID uint32
	message   *Message
}

// Error implements error interface.
func (m messageDeliveryError) Error() string {
	return fmt.Sprintf("failed to deliver messages after %d: %s", m.messageID, APNSErrors[m.code])
}
