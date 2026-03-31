package uart

import (
	"bufio"
	"fmt"
	"os"
	"sync"

	"github.com/beriberikix/zephyr-simulator-server/internal/types"
)

// Multiplexer manages multiple UART backends and broadcasts output in real-time
type Multiplexer struct {
	uartReaders map[uartReaderKey]*UARTReader
	subscribers map[string][]chan types.UARTDataEvent // map[sessionID] -> list of subscriber channels
	mu         sync.RWMutex
	done       chan struct{}
	bufferSize int
	started    bool
}

type uartReaderKey struct {
	sessionID string
	idx       int
}

// UARTReader reads from a named pipe and buffers output
type UARTReader struct {
	sessionID   string
	idx         int
	fifoPath    string
	multiplexer *Multiplexer
	file        *os.File
	buffer      *CircularBuffer
	mu          sync.Mutex
	started     bool
}

// CircularBuffer maintains a sliding window of recent UART output
type CircularBuffer struct {
	data    []string
	maxSize int
	idx     int
	mu      sync.RWMutex
	full    bool
}

// NewMultiplexer creates a new UART multiplexer for a session
func NewMultiplexer(_ string, bufferSize int) *Multiplexer {
	return &Multiplexer{
		uartReaders: make(map[uartReaderKey]*UARTReader),
		subscribers: make(map[string][]chan types.UARTDataEvent),
		done:        make(chan struct{}),
		bufferSize:  bufferSize,
	}
}

// AddUARTBackend registers a FIFO named pipe for reading for the default session.
func (m *Multiplexer) AddUARTBackend(idx int, fifoPath string) (*UARTReader, error) {
	return m.AddSessionUARTBackend("", idx, fifoPath)
}

// AddSessionUARTBackend registers a FIFO named pipe for a specific session.
func (m *Multiplexer) AddSessionUARTBackend(sessionID string, idx int, fifoPath string) (*UARTReader, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := uartReaderKey{sessionID: sessionID, idx: idx}
	if existing, ok := m.uartReaders[key]; ok {
		existing.closeFile()
		delete(m.uartReaders, key)
	}

	reader := &UARTReader{
		sessionID:   sessionID,
		idx:         idx,
		fifoPath:    fifoPath,
		multiplexer: m,
		buffer:      NewCircularBuffer(m.bufferSize),
	}
	m.uartReaders[key] = reader

	if m.started {
		reader.started = true
		go reader.startReading()
	}
	return reader, nil
}

// RegisterSessionBackends registers all FIFO backends for a session.
func (m *Multiplexer) RegisterSessionBackends(sessionID string, fifoPaths []string) error {
	m.mu.Lock()
	m.removeSessionReadersLocked(sessionID)
	for idx, fifoPath := range fifoPaths {
		key := uartReaderKey{sessionID: sessionID, idx: idx}
		m.uartReaders[key] = &UARTReader{
			sessionID:   sessionID,
			idx:         idx,
			fifoPath:    fifoPath,
			multiplexer: m,
			buffer:      NewCircularBuffer(m.bufferSize),
		}
	}
	readersToStart := []*UARTReader{}
	if m.started {
		for key, reader := range m.uartReaders {
			if key.sessionID == sessionID {
				reader.started = true
				readersToStart = append(readersToStart, reader)
			}
		}
	}
	m.mu.Unlock()

	for _, reader := range readersToStart {
		go reader.startReading()
	}

	return nil
}

// UnregisterSessionBackends removes FIFO readers for a session.
func (m *Multiplexer) UnregisterSessionBackends(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removeSessionReadersLocked(sessionID)
}

func (m *Multiplexer) removeSessionReadersLocked(sessionID string) {
	for key, reader := range m.uartReaders {
		if key.sessionID == sessionID {
			reader.closeFile()
			delete(m.uartReaders, key)
		}
	}
}

// Start begins reading from all UART backends in background goroutines
func (m *Multiplexer) Start() error {
	m.mu.Lock()
	m.started = true
	readers := make([]*UARTReader, 0, len(m.uartReaders))
	for _, reader := range m.uartReaders {
		if reader.started {
			continue
		}
		reader.started = true
		readers = append(readers, reader)
	}
	m.mu.Unlock()

	for _, reader := range readers {
		go reader.startReading()
	}

	return nil
}

// Subscribe adds a subscriber channel for a session's UART events
func (m *Multiplexer) Subscribe(sessionID string) chan types.UARTDataEvent {
	m.mu.Lock()
	defer m.mu.Unlock()

	ch := make(chan types.UARTDataEvent, 100)
	m.subscribers[sessionID] = append(m.subscribers[sessionID], ch)

	return ch
}

// Unsubscribe removes a subscriber
func (m *Multiplexer) Unsubscribe(sessionID string, ch chan types.UARTDataEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()

	subs, ok := m.subscribers[sessionID]
	if !ok {
		return
	}

	for i, sub := range subs {
		if sub == ch {
			// Remove from slice
			m.subscribers[sessionID] = append(subs[:i], subs[i+1:]...)
			close(ch)
			break
		}
	}
}

// Broadcast sends a UART event to all subscribers
func (m *Multiplexer) Broadcast(event types.UARTDataEvent) {
	m.mu.RLock()
	subs := m.subscribers[event.SessionID]
	m.mu.RUnlock()

	for _, ch := range subs {
		select {
		case ch <- event:
		case <-m.done:
			return
		default:
			// Channel full, drop event to avoid blocking
		}
	}
}

// HasSessionBackends reports whether a session has any registered UART FIFO backends.
func (m *Multiplexer) HasSessionBackends(sessionID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for key := range m.uartReaders {
		if key.sessionID == sessionID {
			return true
		}
	}
	return false
}

// GetHistory returns the buffered UART output for a specific UART index
func (m *Multiplexer) GetHistory(uartIdx int) []string {
	m.mu.RLock()
	reader, ok := m.uartReaders[uartReaderKey{sessionID: "", idx: uartIdx}]
	m.mu.RUnlock()

	if !ok {
		return nil
	}

	return reader.buffer.GetAll()
}

// Stop closes the multiplexer and all readers
func (m *Multiplexer) Stop() error {
	select {
	case <-m.done:
		// already closed
	default:
		close(m.done)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.started = false

	for _, reader := range m.uartReaders {
		reader.closeFile()
	}

	return nil
}

func (u *UARTReader) closeFile() {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.file != nil {
		_ = u.file.Close()
		u.file = nil
	}
}

// startReading begins reading from the FIFO backend
func (u *UARTReader) startReading() {
	file, err := os.Open(u.fifoPath)
	if err != nil {
		fmt.Printf("error opening UART FIFO %s: %v\n", u.fifoPath, err)
		return
	}
	defer file.Close()

	u.mu.Lock()
	u.file = file
	u.mu.Unlock()

	// Use line-buffered reader
	scanner := bufio.NewScanner(file)
	// Increase buffer size for long lines
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024)

	for scanner.Scan() {
		line := scanner.Text()

		// Add to circular buffer
		u.buffer.Add(line)

		// Broadcast to subscribers
		event := types.UARTDataEvent{
			SessionID: u.sessionID,
			UARTIdx:   u.idx,
			Data:      line,
		}
		u.multiplexer.Broadcast(event)
	}

	if err := scanner.Err(); err != nil {
		fmt.Printf("UART reader error: %v\n", err)
	}
}

// NewCircularBuffer creates a new circular buffer with max size
func NewCircularBuffer(maxSize int) *CircularBuffer {
	return &CircularBuffer{
		data:    make([]string, maxSize),
		maxSize: maxSize,
		idx:     0,
		full:    false,
	}
}

// Add adds a line to the circular buffer
func (cb *CircularBuffer) Add(line string) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.data[cb.idx] = line
	cb.idx = (cb.idx + 1) % cb.maxSize

	if cb.idx == 0 {
		cb.full = true
	}
}

// GetAll returns all buffered lines in order
func (cb *CircularBuffer) GetAll() []string {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	result := make([]string, 0)

	if !cb.full {
		// Buffer not yet full, return from start to current index
		return append(result, cb.data[:cb.idx]...)
	}

	// Buffer is full, return in circular order
	result = append(result, cb.data[cb.idx:]...)
	result = append(result, cb.data[:cb.idx]...)

	return result
}

// Flush clears the circular buffer
func (cb *CircularBuffer) Flush() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.data = make([]string, cb.maxSize)
	cb.idx = 0
	cb.full = false
}

// Size returns current number of items in buffer
func (cb *CircularBuffer) Size() int {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	if cb.full {
		return cb.maxSize
	}
	return cb.idx
}
