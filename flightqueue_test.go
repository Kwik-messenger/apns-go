package apns

import (
	"testing"
)

func TestMFQEmptyRead(t *testing.T) {
	mq := newMessageFlightQueue(4)
	_, ok := mq.Pop()
	if ok {
		t.Error("successful read from empty queue")
	}
}

func TestMFQSimpleReadWrite(t *testing.T) {
	mq := newMessageFlightQueue(4)
	var i uint32
	for i = 0; i < 3; i++ {
		mq.Push(&Message{MessageID: i})
	}

	for i = 0; i < 3; i++ {
		m, ok := mq.Pop()
		if !ok {
			t.Error("unsuccessful read from non-empty queue at index", i)
		} else {
			if m.MessageID != i {
				t.Errorf("malformed read: wanted %d, got %d", i, m.MessageID)
			}
		}
	}

	// overlap there
	for i = 0; i < 3; i++ {
		mq.Push(&Message{MessageID: i})
	}

	for i = 0; i < 3; i++ {
		m, ok := mq.Pop()
		if !ok {
			t.Error("unsuccessful read from non-empty queue after overlap at index", i)
		} else {
			if m.MessageID != i {
				t.Errorf("malformed read after overlap: wanted %d, got %d", i, m.MessageID)
			}
		}
	}

	if _, ok := mq.Pop(); ok {
		t.Error("successful read from empty queue after overlap")
	}
}

func TestMFQGrowReadWrite(t *testing.T) {
	mq := newMessageFlightQueue(4)
	var i uint32
	for i = 0; i < 4; i++ {
		mq.Push(&Message{MessageID: i})
	}

	length := mq.Len()
	if length != 4 {
		t.Errorf("buffer length corrupted: want %d, got %d", 4, length)
		t.FailNow()
	}

	for i = 0; i < 4; i++ {
		m, ok := mq.Pop()
		if !ok {
			t.Error("unsuccessful read from non-empty queue at index", i)
		} else {
			if m.MessageID != i {
				t.Errorf("malformed read: wanted %d, got %d", i, m.MessageID)
			}
		}
	}

	if _, ok := mq.Pop(); ok {
		t.Error("successful read from empty queue after grow")
	}
}

func TestMFQGrowOverlapReadWrite(t *testing.T) {
	mq := newMessageFlightQueue(4)
	var i uint32
	for i = 0; i < 2; i++ {
		mq.Push(&Message{MessageID: i})
		mq.Pop()
	}

	for i = 0; i < 4; i++ {
		mq.Push(&Message{MessageID: i})
	}

	length := mq.Len()
	if length != 4 {
		t.Errorf("buffer length corrupted: want %d, got %d", 4, length)
		t.FailNow()
	}

	for i = 0; i < 4; i++ {
		m, ok := mq.Pop()
		if !ok {
			t.Error("unsuccessful read from non-empty overlapped queue at index", i)
		} else {
			if m.MessageID != i {
				t.Errorf("malformed read: wanted %d, got %d", i, m.MessageID)
			}
		}
	}

	if _, ok := mq.Pop(); ok {
		t.Error("successful read from empty, being overlapped queue after grow")
	}
}

func TestMFQSimpleUnpop(t *testing.T) {
	mq := newMessageFlightQueue(4)
	var i uint32

	for i = 0; i < 3; i++ {
		mq.Push(&Message{MessageID: i})
	}

	var err error

	err = mq.Unpop()
	if err == nil {
		t.Error("successful unpop without pops")
	}

	mq.Pop()
	err = mq.Unpop()
	if err != nil {
		t.Error("unsuccessful unpop after pop:", err)
	}
	err = mq.Unpop()
	if err == nil {
		t.Error("successful second unpop")
	}

	msg, ok := mq.Pop()
	if !ok {
		t.Error("unsuccessful pop after unpop")
	}
	if msg.MessageID != 0 {
		t.Error("invalid unpopped message. Want message id %d, got %d", 0, msg.MessageID)
	}

	// test unpop on full queue
	mq.Push(&Message{MessageID: 3})
	err = mq.Unpop()
	if err != nil {
		t.Error("unsuccessful unpop after pop and push")
	}

	// validate queue
	length := mq.Len()
	if length != 4 {
		t.Errorf("buffer length corrupted: want %d, got %d", 4, length)
		t.FailNow()
	}

	for i = 0; i < 4; i++ {
		m, ok := mq.Pop()
		if !ok {
			t.Error("unsuccessful read from non-empty queue at index", i)
		} else {
			if m.MessageID != i {
				t.Errorf("malformed read: wanted %d, got %d", i, m.MessageID)
			}
		}
	}
}
