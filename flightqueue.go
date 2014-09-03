package apns

const (
	// Multiplier for message flight queue
	MESSAGE_FLIGHT_QUEUE_ENLARGE = 0.33
)

// messageFlightQueue basically a ring buffer that can grow.
// head is an index of next element to read;
// tail is an index of next element to write;
// when head == tail then queue is empty;
// when (tail + 1) % len(buf) == head then queue is full.
type messageFlightQueue struct {
	buf        []*Message
	head, tail int

	lastm *Message
}

// newMessageQueue initializes queue
func newMessageFlightQueue(size int) *messageFlightQueue {
	return &messageFlightQueue{make([]*Message, size), 0, 0, nil}
}

// Push pushes message into tail of queue
func (mq *messageFlightQueue) Push(m *Message) {
	newTail := (mq.tail + 1) % len(mq.buf)
	if newTail == mq.head {
		// buffer filled up, grow
		mq.grow()
		newTail = (mq.tail + 1) % len(mq.buf)
	}
	mq.buf[mq.tail] = m
	mq.tail = newTail
}

// Pop pops from head of queue. 'ok' will be false when queue is empty
func (mq *messageFlightQueue) Pop() (m *Message, ok bool) {
	if mq.head == mq.tail {
		// queue is empty
		return nil, false
	}

	m = mq.buf[mq.head]
	mq.head = (mq.head + 1) % len(mq.buf)
	mq.lastm = m
	return m, true
}

// Pushback reverses Pop. Only one last message can be unpopped
func (mq *messageFlightQueue) Pushback() error {
	if mq.lastm != nil {
		newHead := (mq.head - 1 + len(mq.buf)) % len(mq.buf)
		if newHead == mq.tail {
			// queue is full
			mq.grow()
			newHead = len(mq.buf) - 1
		}

		mq.buf[newHead] = mq.lastm
		mq.head = newHead
		mq.lastm = nil
		return nil
	} else {
		return errMessageAlreadyPushedBack
	}
}

// Len returns current count of messages in buffer
func (mq *messageFlightQueue) Len() int {
	return (mq.tail - mq.head + len(mq.buf)) % len(mq.buf)
}

// grow grows internal buffer.
func (mq *messageFlightQueue) grow() {
	newBuf := make([]*Message, int(float32(len(mq.buf))*(1+MESSAGE_FLIGHT_QUEUE_ENLARGE)))

	if mq.tail >= mq.head {
		// buf is not wrapped
		copy(newBuf, mq.buf[mq.head:mq.tail])

		mq.tail = mq.tail - mq.head
		mq.head = 0
	} else {
		// buf have wrapped
		n := copy(newBuf, mq.buf[mq.head:])
		copy(newBuf[n:], mq.buf[:mq.tail])
		mq.tail = len(mq.buf) + mq.tail - mq.head
		mq.head = 0
	}
	mq.buf = newBuf
}
