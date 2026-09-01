package webui

import (
	"sync"
	"time"
)

// SessionLinker abstracts the research-account link lifecycle so the console can
// run against either the default offline mock flow or a real WhatsApp
// linked-device pairing (see the webui/live package).
//
// A linker only ever links the *researcher's own* account. It has nothing to do
// with who may be measured — that is gated separately by the participant
// consent allowlist.
type SessionLinker interface {
	// Connect starts (or restarts) a pairing attempt. The simulate hint is only
	// meaningful to the mock linker and is ignored by real implementations.
	Connect(simulate string) SessionStatus
	// Status returns the current link state, advancing any time-based flow.
	Status() SessionStatus
	// Disconnect tears the link down.
	Disconnect() SessionStatus
	// Close releases any resources held by the linker.
	Close() error
}

// mockLinker is the default, fully offline QR flow. It pairs no real account and
// produces an obviously-fake placeholder code that encodes nothing.
type mockLinker struct {
	mu      sync.Mutex
	now     func() time.Time
	state   ConnectionState
	since   time.Time
	qr      [][]bool
	outcome ConnectionState // Terminal state a pending session resolves to.
	account string
}

func newMockLinker(now func() time.Time) *mockLinker {
	if now == nil {
		now = time.Now
	}
	return &mockLinker{now: now, state: ConnIdle}
}

func (m *mockLinker) Connect(simulate string) SessionStatus {
	m.mu.Lock()
	defer m.mu.Unlock()

	outcome := ConnConnected
	switch simulate {
	case "expired":
		outcome = ConnExpired
	case "error":
		outcome = ConnError
	}
	m.state = ConnPending
	m.since = m.now()
	m.qr = mockQRMatrix(m.since.UnixNano())
	m.outcome = outcome
	m.account = "research-lab-01 (mock)"
	return m.statusLocked()
}

func (m *mockLinker) Status() SessionStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.advanceLocked()
	return m.statusLocked()
}

func (m *mockLinker) Disconnect() SessionStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state = ConnDisconnected
	m.qr = nil
	m.account = ""
	return m.statusLocked()
}

func (m *mockLinker) Close() error { return nil }

// forceConnected is a test-only helper to place the mock linker in the connected
// state without waiting for the simulated scan delay.
func (m *mockLinker) forceConnected(account string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state = ConnConnected
	m.account = account
	m.qr = nil
}

// advanceLocked drives the mock state machine based on elapsed time.
func (m *mockLinker) advanceLocked() {
	if m.state != ConnPending {
		return
	}
	elapsed := m.now().Sub(m.since)
	switch m.outcome {
	case ConnError:
		if elapsed >= mockScanDelay {
			m.state = ConnError
			m.qr = nil
		}
	case ConnExpired:
		if elapsed >= mockQRExpiry {
			m.state = ConnExpired
			m.qr = nil
		}
	default: // ConnConnected
		if elapsed >= mockScanDelay {
			m.state = ConnConnected
			m.qr = nil
		} else if elapsed >= mockQRExpiry {
			m.state = ConnExpired
			m.qr = nil
		}
	}
}

func (m *mockLinker) statusLocked() SessionStatus {
	st := SessionStatus{State: m.state, Mock: true}
	switch m.state {
	case ConnPending:
		remain := int((mockQRExpiry - m.now().Sub(m.since)).Seconds())
		if remain < 0 {
			remain = 0
		}
		st.ExpiresInSec = remain
		st.QRMatrix = m.qr
		st.Message = "Scan the placeholder code to link the research account (mock — encodes nothing)."
	case ConnConnected:
		st.Account = m.account
		st.Message = "Research account linked (mock). Live probing is not wired in this build."
	case ConnExpired:
		st.Message = "The pairing code expired. Generate a new one to try again."
	case ConnDisconnected:
		st.Message = "Disconnected. Reconnect to run experiments."
	case ConnError:
		st.Message = "Pairing failed. Check the integration and try again."
	default:
		st.Message = "Not connected. Link an approved research account to begin."
	}
	return st
}
