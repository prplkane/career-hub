package mailtransport

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCancelQueuedCommandDoesNotAbandonActiveCommand(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	f := newTLSFixture(t, false, func(p *imapPeer) error {
		if err := p.bootstrap(); err != nil {
			return err
		}
		tag, _, err := p.next("UID FETCH 9 ")
		if err != nil {
			return err
		}
		close(started)
		select {
		case <-release:
		case <-time.After(2 * time.Second):
			return errors.New("synthetic active command not released")
		}
		if err := p.write("* 1 FETCH (UID 9 RFC822.SIZE 3 BODY[] {3}\r\nabc)\r\n" + tag + " OK complete\r\n"); err != nil {
			return err
		}
		return p.waitClose()
	})
	f.options.operationTimeout = time.Second
	m := openFixture(t, f)
	result := make(chan error, 1)
	go func() {
		body, err := m.FetchMessage(context.Background(), 9)
		if err == nil && string(body) != "abc" {
			err = errors.New("synthetic body changed")
		}
		result <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("active command did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	_, err := m.SearchUIDs(ctx, 1, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	assertCode(t, err, "EMAIL_SYNC_NETWORK_FAILED")
	close(release)
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued cancellation broke active operation")
	}
	m.Close()
	f.assertPeerClosed(t)
	f.assertScript(t)
	select {
	case <-m.client.Closed():
	case <-time.After(time.Second):
		t.Fatal("owned decoder did not exit after consumer and socket release")
	}
}
