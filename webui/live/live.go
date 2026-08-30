// Package live provides a real WhatsApp linked-device account linker for the
// research governance console.
//
// It links the *researcher's own* WhatsApp account via the official
// "Linked devices" QR flow (whatsmeow's GetQRChannel) so the console can show a
// scannable code. It does nothing else: it starts no probing and measures no
// one. Who may be measured remains gated by the console's participant consent
// allowlist, entirely separate from this link.
//
// SECURITY: the raw pairing code and any session token are never logged. The QR
// matrix is rendered for scanning (that is its purpose) but the underlying code
// string never leaves this process except as the on-screen QR.
package live

import (
	"context"
	"sync"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"
	"go.mau.fi/whatsmeow/webui"
)

// Linker implements webui.SessionLinker against a real WhatsApp account.
type Linker struct {
	log    waLog.Logger
	dbPath string

	mu         sync.Mutex
	container  *sqlstore.Container
	client     *whatsmeow.Client
	state      webui.ConnectionState
	qr         [][]bool
	account    string
	message    string
	codeExpiry time.Time
	cancel     context.CancelFunc
}

// New returns a live linker that persists its session in the SQLite database at
// dbPath. The database is opened lazily on the first Connect call.
func New(dbPath string, log waLog.Logger) *Linker {
	if log == nil {
		log = waLog.Noop
	}
	return &Linker{log: log, dbPath: dbPath, state: webui.ConnIdle}
}

// Connect starts (or resumes) linking the researcher's own account. The simulate
// hint is ignored — this is a real pairing.
func (l *Linker) Connect(_ string) webui.SessionStatus {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.state == webui.ConnConnected {
		return l.statusLocked()
	}
	l.cancelLocked()

	if err := l.ensureClientLocked(); err != nil {
		l.log.Errorf("live linker: open store: %v", err)
		l.state = webui.ConnError
		l.message = "Could not open the local session store."
		return l.statusLocked()
	}

	// Already paired in a previous run — just reconnect, no QR needed.
	if l.client.Store.ID != nil {
		if err := l.client.Connect(); err != nil {
			l.log.Errorf("live linker: reconnect: %v", err)
			l.state = webui.ConnError
			l.message = "Could not reconnect the linked account."
			return l.statusLocked()
		}
		l.state = webui.ConnConnected
		l.account = maskJID(l.client.Store.ID)
		l.qr = nil
		l.message = "Research account linked."
		return l.statusLocked()
	}

	ctx, cancel := context.WithCancel(context.Background())
	l.cancel = cancel
	qrChan, err := l.client.GetQRChannel(ctx)
	if err != nil {
		cancel()
		l.cancel = nil
		l.log.Errorf("live linker: qr channel: %v", err)
		l.state = webui.ConnError
		l.message = "Could not start pairing."
		return l.statusLocked()
	}
	if err := l.client.Connect(); err != nil {
		cancel()
		l.cancel = nil
		l.log.Errorf("live linker: connect: %v", err)
		l.state = webui.ConnError
		l.message = "Could not reach WhatsApp to start pairing (check the server's network/logs)."
		return l.statusLocked()
	}
	l.state = webui.ConnPending
	l.message = "Starting pairing…"
	go l.readQR(qrChan)
	return l.statusLocked()
}

func (l *Linker) readQR(ch <-chan whatsmeow.QRChannelItem) {
	for item := range ch {
		l.mu.Lock()
		switch item.Event {
		case whatsmeow.QRChannelEventCode:
			// SECURITY: encode the code into a scannable matrix; never log item.Code.
			if m, err := qrcode.New(item.Code, qrcode.Medium); err == nil {
				l.qr = m.Bitmap()
			}
			l.state = webui.ConnPending
			l.message = "Scan the code in WhatsApp → Linked devices."
			if item.Timeout > 0 {
				l.codeExpiry = time.Now().Add(item.Timeout)
			}
			l.mu.Unlock()
		case "success":
			l.state = webui.ConnConnected
			l.qr = nil
			if l.client != nil && l.client.Store.ID != nil {
				l.account = maskJID(l.client.Store.ID)
			}
			l.message = "Research account linked."
			l.mu.Unlock()
			l.log.Infof("live linker: account linked")
			return
		case "timeout":
			l.state = webui.ConnExpired
			l.qr = nil
			l.message = "Pairing timed out. Reconnect to try again."
			l.mu.Unlock()
			return
		default:
			l.state = webui.ConnError
			l.qr = nil
			l.message = "Pairing ended: " + item.Event
			l.mu.Unlock()
			l.log.Warnf("live linker: pairing ended with %q", item.Event)
			return
		}
	}
}

// Status returns the current link state.
func (l *Linker) Status() webui.SessionStatus {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.statusLocked()
}

func (l *Linker) statusLocked() webui.SessionStatus {
	st := webui.SessionStatus{State: l.state, Mock: false, Message: l.message}
	switch l.state {
	case webui.ConnPending:
		st.QRMatrix = l.qr
		if !l.codeExpiry.IsZero() {
			remain := int(time.Until(l.codeExpiry).Seconds())
			if remain < 0 {
				remain = 0
			}
			st.ExpiresInSec = remain
		}
	case webui.ConnConnected:
		st.Account = l.account
	}
	return st
}

// Disconnect tears down the socket but keeps the stored session so a later
// Connect can resume without re-scanning.
func (l *Linker) Disconnect() webui.SessionStatus {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cancelLocked()
	if l.client != nil {
		l.client.Disconnect()
	}
	l.state = webui.ConnDisconnected
	l.qr = nil
	l.account = ""
	l.message = "Disconnected. Reconnect to run experiments."
	return l.statusLocked()
}

// Close releases the client and database.
func (l *Linker) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cancelLocked()
	if l.client != nil {
		l.client.Disconnect()
	}
	if l.container != nil {
		return l.container.Close()
	}
	return nil
}

func (l *Linker) ensureClientLocked() error {
	if l.client != nil {
		return nil
	}
	ctx := context.Background()
	container, err := sqlstore.New(ctx, "sqlite3", "file:"+l.dbPath+"?_foreign_keys=on", l.log.Sub("Store"))
	if err != nil {
		return err
	}
	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		_ = container.Close()
		return err
	}
	l.container = container
	l.client = whatsmeow.NewClient(device, l.log.Sub("Client"))
	return nil
}

func (l *Linker) cancelLocked() {
	if l.cancel != nil {
		l.cancel()
		l.cancel = nil
	}
}

// maskJID renders a JID with only the last two digits of the user part visible,
// so the linked account can be recognized without exposing the full number.
func maskJID(j *types.JID) string {
	if j == nil {
		return ""
	}
	u := j.User
	if len(u) <= 2 {
		return "••" + u
	}
	return "••••" + u[len(u)-2:] + "@" + j.Server
}
