package apns

import (
	"encoding/binary"
	"io"
	"time"
)

const (
	// Tell APNS to deliver message without delay.
	PRIORITY_IMMEDIATE = 10
	// Tell APNS to deliver message in background.
	PRIORITY_DELAYED = 5
)

// There are constants for APNS protocol frames.
const (
	FRAME_TOKEN_LENGTH           uint16 = 32
	FRAME_NOTIFICATION_ID_LENGTH uint16 = 4
	FRAME_EXPIRATION_DATE_LENGTH uint16 = 4
	FRAME_PRIORITY_LENGTH        uint16 = 1

	FRAME_TOKEN_ID           uint8 = 1
	FRAME_PAYLOAD_ID         uint8 = 2
	FRAME_NOTIFICATION_ID_ID uint8 = 3
	FRAME_EXPIRATION_DATE_ID uint8 = 4
	FRAME_PRIORITY_ID        uint8 = 5

	FRAME_PROTO_VERSION uint8 = 2
)

// APNSErrors contains descriptions for error codes.
var APNSErrors = map[uint8]string{
	0:   "No errors",
	1:   "Processing error",
	2:   "Missing device token",
	3:   "Missing topic",
	4:   "Missing payload",
	5:   "Invalid token size",
	6:   "Invalid topic size",
	7:   "Invalid payload size",
	8:   "Invalid token",
	10:  "Shutdown",
	255: "Unknown",
}

// Message represents a ready-to-go-to-wire message.
type Message struct {
	Token     []byte
	MessageID uint32
	Expiry    int32
	Priority  uint8
	Payload   []byte

	sentAt time.Time
}

// newMessage allocates message.
func newMessage(token []byte, expiry int32, priority uint8, p []byte) *Message {
	return &Message{token, 0, expiry, priority, p, time.Time{}}
}

// WriteTo implements io.WriterTo.
func (m *Message) WriteTo(to io.Writer) (n int64, err error) {
	// this helper will count written bytes and record in 'n'
	counter := writerFunc(func(data []byte) (int, error) {
		nn, err := to.Write(data)
		n += int64(nn)
		return nn, err
	})

	// just wrap repeated code
	write := func(i interface{}) error {
		return binary.Write(counter, binary.BigEndian, i)
	}

	// write frame header
	if err = write(FRAME_PROTO_VERSION); err != nil {
		return
	}

	// write frame length
	// length = token   ++  payload     ++  message id  ++  expires  ++  prio
	//          32 + 3  ++  len(p) + 3  ++  4 + 3       ++  4 + 3    ++  1 + 3
	// 3 is a item header length
	length := 32 + 3 + len(m.Payload) + 3 + 4 + 3 + 4 + 3 + 1 + 3
	if err = write(uint32(length)); err != nil {
		return
	}

	// write token
	if err = write(FRAME_TOKEN_ID); err != nil {
		return
	}
	if err = write(FRAME_TOKEN_LENGTH); err != nil {
		return
	}
	if err = write(m.Token); err != nil {
		return
	}

	// write payload
	if err = write(FRAME_PAYLOAD_ID); err != nil {
		return
	}
	if err = write(uint16(len(m.Payload))); err != nil {
		return
	}
	if err = write(m.Payload); err != nil {
		return
	}

	// write message id
	if err = write(FRAME_NOTIFICATION_ID_ID); err != nil {
		return
	}
	if err = write(FRAME_NOTIFICATION_ID_LENGTH); err != nil {
		return
	}
	if err = write(m.MessageID); err != nil {
		return
	}

	// write expiration date
	if err = write(FRAME_EXPIRATION_DATE_ID); err != nil {
		return
	}
	if err = write(FRAME_EXPIRATION_DATE_LENGTH); err != nil {
		return
	}
	if err = write(m.Expiry); err != nil {
		return
	}

	// write priority
	if err = write(FRAME_PRIORITY_ID); err != nil {
		return
	}
	if err = write(FRAME_PRIORITY_LENGTH); err != nil {
		return
	}
	if err = write(m.Priority); err != nil {
		return
	}

	return
}
