package apns

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"time"
)

const (
	WORKER_INFLIGHT_QUEUE_SIZE = 128
	WORKER_QUEUE_UPDATE_FREQ   = time.Millisecond * 500
	WORKER_ASSUME_SENT_TIMEOUT = time.Second * 5
)

var (
	ErrInvalidAppleResponse = errors.New("invalid apple response")
)

type worker struct {
	inbox     chan *Message
	toRespawn chan deadWorker

	counter  uint32
	inflight *messageFlightQueue
	buf      *bytes.Buffer
}

func newWorker(inbox chan *Message, toRespawn chan deadWorker) *worker {
	return &worker{inbox: inbox, inflight: newMessageFlightQueue(WORKER_INFLIGHT_QUEUE_SIZE),
		buf: &bytes.Buffer{}, toRespawn: toRespawn}
}

func (w *worker) Run(c net.Conn, ip net.IP) {
	defer c.Close()

	var readErrors = make(chan error, 1)
	go w.runErrorReader(c, readErrors)

	ticker := time.NewTicker(WORKER_QUEUE_UPDATE_FREQ)
	defer ticker.Stop()

	// resend notifications, if any

	oldLength := w.inflight.Len()
	for i := 0; i < oldLength; i++ {
		m, _ := w.inflight.Pop()
		err := w.send(m, c)
		if err != nil {
			w.die(err, ip)
		}
	}

	for {
		select {
		case msg := <-w.inbox:
			// got new message to send
			err := w.send(msg, c)
			if err != nil {
				w.die(err, ip)
			}
			println("worker: sent:", msg.MessageID, string(msg.Payload))
		case now := <-ticker.C:
			for m, ok := w.inflight.Pop(); ok; m, ok = w.inflight.Pop() {
				// forget about messages that was sent WORKER_ASSUME_SENT_TIMEOUT ago
				// break when found new enough message
				if now.Sub(m.sentAt) < WORKER_ASSUME_SENT_TIMEOUT {
					w.inflight.Unpop()
					break
				}
				println("worker: assuming delivered:", string(m.Payload))
			}

		case err := <-readErrors:
			// something went wrong...
			switch err.(type) {
			case messageDeliveryError:
				// message failed to be delivered
				// skip up to last accepted message
				msgerr := err.(messageDeliveryError)
				if msgerr.messageId != 0 {
					for m, ok := w.inflight.Pop(); ok; m, ok = w.inflight.Pop() {
						if m.MessageID == msgerr.messageId {
							msgerr.message = m
							break
						}
					}
				}
			}
			w.die(err, ip)
		}
	}
}

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
	var messageId uint32
	err = binary.Read(conn, binary.BigEndian, &messageId)
	if err != nil {
		errors <- err
		return
	}

	errors <- messageDeliveryError{errorCode, messageId, nil}
}

func (w *worker) die(err error, ip net.IP) {
	println("worker: dead:", err.Error())
	w.toRespawn <- deadWorker{w, err, ip}
}

type messageDeliveryError struct {
	code      uint8
	messageId uint32
	message   *Message
}

func (m messageDeliveryError) Error() string {
	return fmt.Sprintf("failed to deliver messages after %d: %s", m.messageId, APNSErrors[m.code])
}
