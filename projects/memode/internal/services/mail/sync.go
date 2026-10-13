package mail

import (
	"context"
	"log/slog"
	"time"
)

// syncer keeps the local index fresh: a full sync of every mailbox, then IDLE
// on INBOX for instant updates plus a periodic full resync.
// shortcut: IDLE watches only INBOX; other mailboxes refresh on the 10-minute
// resync and after every write. Upgrade to one IDLE connection per mailbox if
// other mailboxes need instant updates.
type syncer struct {
	remote      remote
	index       index
	resyncEvery time.Duration
	wake        chan struct{}
	synced      chan struct{} // closed informally after each completed pass
}

func newSyncer(r remote, ix index) *syncer {
	return &syncer{
		remote:      r,
		index:       ix,
		resyncEvery: 10 * time.Minute,
		wake:        make(chan struct{}, 1),
	}
}

// run blocks until ctx is done. Each loop: sync all mailboxes, then IDLE on
// INBOX until the resync interval elapses, a wake arrives or the connection
// drops.
func (s *syncer) run(ctx context.Context) {
	for {
		s.syncAll(ctx)
		idleCtx, cancel := context.WithTimeout(ctx, s.resyncEvery)
		err := s.remote.idleBlocks(idleCtx, "INBOX")
		cancel()
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			slog.Debug("idle ended", "err", err)
			// Back off after connection trouble so a broken server does not
			// turn the loop into a dial flood.
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
		}
		// Drain stale wakes so writes before the IDLE start force one resync.
		select {
		case <-s.wake:
		default:
		}
	}
}

// syncAll syncs every mailbox the server has.
func (s *syncer) syncAll(ctx context.Context) {
	names, err := s.remote.mailboxes(ctx)
	if err != nil {
		slog.Error("mail sync: list mailboxes", "err", err)
		return
	}
	for _, name := range names {
		if err := s.remote.syncMailbox(ctx, s.index, name); err != nil {
			slog.Error("mail sync: mailbox", "mailbox", name, "err", err)
		}
	}
}

// resyncNow synchronously syncs the given mailboxes, then the full set if
// none is given. Writes call this before returning so the local view is
// always consistent.
func (s *syncer) resyncNow(ctx context.Context, mailboxes ...string) {
	if len(mailboxes) == 0 {
		s.syncAll(ctx)
		return
	}
	for _, name := range mailboxes {
		if err := s.remote.syncMailbox(ctx, s.index, name); err != nil {
			slog.Error("mail resync: mailbox", "mailbox", name, "err", err)
		}
	}
	// Wake the IDLE loop so it re-syncs quickly too; stale wakes are fine.
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
