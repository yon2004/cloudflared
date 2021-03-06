package h2mux

// ReadyList multiplexes several event signals onto a single channel.
type ReadyList struct {
	signalC chan uint32
	waitC   chan uint32
}

func NewReadyList() *ReadyList {
	rl := &ReadyList{
		signalC: make(chan uint32),
		waitC:   make(chan uint32),
	}
	go rl.run()
	return rl
}

// ID is the stream ID
func (r *ReadyList) Signal(ID uint32) {
	r.signalC <- ID
}

func (r *ReadyList) ReadyChannel() <-chan uint32 {
	return r.waitC
}

func (r *ReadyList) Close() {
	close(r.signalC)
}

func (r *ReadyList) run() {
	defer close(r.waitC)
	var queue readyDescriptorQueue
	var firstReady *readyDescriptor
	activeDescriptors := newReadyDescriptorMap()
	for {
		if firstReady == nil {
			// Wait for first ready descriptor
			i, ok := <-r.signalC
			if !ok {
				// closed
				return
			}
			firstReady = activeDescriptors.SetIfMissing(i)
		}
		select {
		case r.waitC <- firstReady.ID:
			activeDescriptors.Delete(firstReady.ID)
			firstReady = queue.Dequeue()
		case i, ok := <-r.signalC:
			if !ok {
				// closed
				return
			}
			newReady := activeDescriptors.SetIfMissing(i)
			if newReady != nil {
				// key doesn't exist
				queue.Enqueue(newReady)
			}
		}
	}
}

type readyDescriptor struct {
	ID   uint32
	Next *readyDescriptor
}

// readyDescriptorQueue is a queue of readyDescriptors in the form of a singly-linked list.
// The nil readyDescriptorQueue is an empty queue ready for use.
type readyDescriptorQueue struct {
	Head *readyDescriptor
	Tail *readyDescriptor
}

func (q *readyDescriptorQueue) Empty() bool {
	return q.Head == nil
}

func (q *readyDescriptorQueue) Enqueue(x *readyDescriptor) {
	if x.Next != nil {
		panic("enqueued already queued item")
	}
	if q.Empty() {
		q.Head = x
		q.Tail = x
	} else {
		q.Tail.Next = x
		q.Tail = x
	}
}

// Dequeue returns the first readyDescriptor in the queue, or nil if empty.
func (q *readyDescriptorQueue) Dequeue() *readyDescriptor {
	if q.Empty() {
		return nil
	}
	x := q.Head
	q.Head = x.Next
	x.Next = nil
	return x
}

// readyDescriptorQueue is a map of readyDescriptors keyed by ID.
// It maintains a free list of deleted ready descriptors.
type readyDescriptorMap struct {
	descriptors map[uint32]*readyDescriptor
	free        []*readyDescriptor
}

func newReadyDescriptorMap() *readyDescriptorMap {
	return &readyDescriptorMap{descriptors: make(map[uint32]*readyDescriptor)}
}

// create or reuse a readyDescriptor if the stream is not in the queue.
// This avoid stream starvation caused by a single high-bandwidth stream monopolising the writer goroutine
func (m *readyDescriptorMap) SetIfMissing(key uint32) *readyDescriptor {
	if _, ok := m.descriptors[key]; ok {
		return nil
	}

	var newDescriptor *readyDescriptor
	if len(m.free) > 0 {
		// reuse deleted ready descriptors
		newDescriptor = m.free[len(m.free)-1]
		m.free = m.free[:len(m.free)-1]
	} else {
		newDescriptor = &readyDescriptor{}
	}
	newDescriptor.ID = key
	m.descriptors[key] = newDescriptor
	return newDescriptor
}

func (m *readyDescriptorMap) Delete(key uint32) {
	if descriptor, ok := m.descriptors[key]; ok {
		m.free = append(m.free, descriptor)
		delete(m.descriptors, key)
	}
}
