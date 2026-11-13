// Package lifecycle keeps the in-memory runtime state of each WhatsApp client
// (one per instance) and the guards that the original code was missing.
//
// Lander patch (2026-11-12), motivated by the "405 client outdated" incident:
//
//   - A client whose Connect() failed used to stay registered forever in
//     clientPointer/myClientPointer. /instance/connect then answered "already
//     running" without doing anything and /instance/qr only read an empty
//     qrcode column. The registry below lets the service know that a client is
//     dead (not connected and not starting), so it can be discarded.
//   - Two code paths could start the same instance at the same time
//     (/instance/connect starts a client asynchronously and /instance/qr starts
//     another one when the first is not registered yet; the send path and the
//     Disconnected handler do the same). TryBeginStart serialises them.
//   - After WhatsApp rejects the client version (405), every automatic restart
//     is guaranteed to fail and only adds load. OutdatedRecently lets automatic
//     paths stop retrying for a cooldown period. Explicit operator actions
//     (/instance/connect, /instance/qr) still get one fresh attempt.
//   - Every StartClient opened a new database pool that was never closed. The
//     registry keeps a closer per client so the pool is closed when the client
//     is discarded.
//
// Nothing here is persisted and nothing here touches the device/session store:
// discarding a client never deletes a pairing.
package lifecycle

import (
	"sync"
	"time"
)

// Phase is what the process knows about an instance right now.
type Phase string

const (
	PhaseIdle       Phase = "idle"       // no client in memory
	PhaseStarting   Phase = "starting"   // StartClient running, Connect() not finished yet
	PhaseConnecting Phase = "connecting" // socket open, waiting for login or for the first QR
	PhaseQR         Phase = "qr"         // a QR code is available to be scanned
	PhasePaired     Phase = "paired"     // QR scanned, waiting for the first login
	PhaseConnected  Phase = "connected"  // logged in (events.Connected)
	PhaseFailed     Phase = "failed"     // Connect() failed or WhatsApp refused the login
	PhaseOutdated   Phase = "outdated"   // WhatsApp refused the client version (405)
	PhaseLoggedOut  Phase = "logged_out" // the device was removed from the account
	PhaseStopped    Phase = "stopped"    // client discarded on purpose
)

// Snapshot is a copy of the state of one instance.
type Snapshot struct {
	Phase      Phase
	LastError  string
	Since      time.Time
	OutdatedAt time.Time
}

type closer struct {
	owner any
	close func() error
}

// Registry is safe for concurrent use.
type Registry struct {
	mu       sync.Mutex
	starting map[string]time.Time
	state    map[string]Snapshot
	closers  map[string]closer

	// Now is overridable in tests.
	Now func() time.Time
	// StartTimeout: a start older than this is considered stuck and no longer
	// blocks a new one (protects against a goroutine that never returned).
	StartTimeout time.Duration
	// OutdatedCooldown: how long automatic restarts stay disabled after a 405.
	OutdatedCooldown time.Duration
}

// New creates an empty registry with the production defaults.
func New() *Registry {
	return &Registry{
		starting:         map[string]time.Time{},
		state:            map[string]Snapshot{},
		closers:          map[string]closer{},
		Now:              time.Now,
		StartTimeout:     90 * time.Second,
		OutdatedCooldown: 10 * time.Minute,
	}
}

// Default is the registry used by the services.
var Default = New()

// TryBeginStart reserves the right to start the instance. It returns false
// when another start is already in progress (and not stuck).
func (r *Registry) TryBeginStart(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.Now()
	if since, ok := r.starting[id]; ok && now.Sub(since) < r.StartTimeout {
		return false
	}
	r.starting[id] = now
	r.setLocked(id, PhaseStarting, "")
	return true
}

// EndStart releases the start reservation. Safe to call more than once.
func (r *Registry) EndStart(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.starting, id)
}

// IsStarting reports whether a (non-stuck) start is in progress.
func (r *Registry) IsStarting(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	since, ok := r.starting[id]
	return ok && r.Now().Sub(since) < r.StartTimeout
}

// Set records the current phase. lastError may be empty.
func (r *Registry) Set(id string, phase Phase, lastError string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.setLocked(id, phase, lastError)
}

func (r *Registry) setLocked(id string, phase Phase, lastError string) {
	prev := r.state[id]
	next := Snapshot{Phase: phase, LastError: lastError, Since: r.Now(), OutdatedAt: prev.OutdatedAt}
	if phase == PhaseOutdated {
		next.OutdatedAt = next.Since
	}
	if phase == PhaseConnected || phase == PhaseQR || phase == PhasePaired {
		// The version was accepted: the 405 is over for this instance.
		next.OutdatedAt = time.Time{}
	}
	if lastError == "" && phase != PhaseConnected && phase != PhaseQR && phase != PhasePaired {
		next.LastError = prev.LastError
	}
	r.state[id] = next
}

// MarkOutdated records a 405 for the instance.
func (r *Registry) MarkOutdated(id, detail string) {
	r.Set(id, PhaseOutdated, detail)
}

// OutdatedRecently reports whether the instance got a 405 inside the cooldown.
func (r *Registry) OutdatedRecently(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	at := r.state[id].OutdatedAt
	return !at.IsZero() && r.Now().Sub(at) < r.OutdatedCooldown
}

// Get returns a copy of the state. Unknown instances are PhaseIdle.
func (r *Registry) Get(id string) Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.state[id]
	if !ok {
		return Snapshot{Phase: PhaseIdle}
	}
	return s
}

// SetCloser registers the resource to close when the client identified by
// owner is discarded. A previous closer for the same instance is returned to
// the caller (not closed) so it can decide what to do with it.
func (r *Registry) SetCloser(id string, owner any, fn func() error) (previous func() error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if old, ok := r.closers[id]; ok && old.owner != owner {
		previous = old.close
	}
	r.closers[id] = closer{owner: owner, close: fn}
	return previous
}

// CloseFor closes the resource registered for this instance, but only when it
// belongs to owner. This prevents a goroutine that is tearing down an old
// client from closing the pool of the client that replaced it.
func (r *Registry) CloseFor(id string, owner any) error {
	r.mu.Lock()
	c, ok := r.closers[id]
	if !ok || c.owner != owner {
		r.mu.Unlock()
		return nil
	}
	delete(r.closers, id)
	r.mu.Unlock()
	if c.close == nil {
		return nil
	}
	return c.close()
}

// Owns reports whether owner is the client currently registered for the
// instance. The per-client loop in StartClient uses it to exit when its client
// was replaced or discarded, without reading the service maps (which have no
// lock) more often than the original code did.
func (r *Registry) Owns(id string, owner any) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.closers[id]
	return ok && c.owner == owner
}

// OpenResources is the number of registered closers (for tests/monitoring).
func (r *Registry) OpenResources() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.closers)
}

// Version is a WhatsApp web client version (major.minor.patch).
type Version [3]uint32

// IsNewer reports whether a is strictly newer than b.
func IsNewer(a, b Version) bool {
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

// Plausible filters garbage coming from scraping web.whatsapp.com: WhatsApp
// web versions have looked like 2.3000.<build> for years.
func Plausible(v Version) bool {
	return v[0] == 2 && v[1] >= 3000 && v[2] > 0
}
