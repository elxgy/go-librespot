//go:build test_unit

package audio

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	librespot "github.com/elxgy/go-librespot"
)

func TestParseKeyResponseSeq(t *testing.T) {
	if _, ok := parseKeyResponseSeq(nil); ok {
		t.Fatal("expected nil payload to be rejected")
	}
	if _, ok := parseKeyResponseSeq([]byte{0x00, 0x01, 0x02}); ok {
		t.Fatal("expected short payload to be rejected")
	}
	seq, ok := parseKeyResponseSeq([]byte{0x00, 0x00, 0x00, 0x07, 0x09, 0x09})
	if !ok {
		t.Fatal("expected a 6-byte payload to parse")
	}
	if seq != 7 {
		t.Fatalf("expected sequence 7, got %d", seq)
	}
}

func TestWarnInvalidSeqRateLimits(t *testing.T) {
	log := &warnCounterLogger{}
	p := &KeyProvider{log: log, reqs: make(map[uint32]keyRequest)}

	p.warnInvalidSeq(7)
	if got := log.count(); got != 1 {
		t.Fatalf("first warning must emit, got %d lines", got)
	}

	p.warnInvalidSeq(7)
	p.warnInvalidSeq(7)
	if got := log.count(); got != 1 {
		t.Fatalf("warnings inside the window must be suppressed, got %d lines", got)
	}

	p.expireInvalidSeqWindow()
	p.warnInvalidSeq(9)
	if got := log.count(); got != 2 {
		t.Fatalf("warning after the window must emit, got %d lines", got)
	}
	if line := log.last(); !strings.Contains(line, "(suppressed 2 similar)") {
		t.Fatalf("windowed emit must carry the suppressed count, got %q", line)
	}

	p.expireInvalidSeqWindow()
	p.warnInvalidSeq(9)
	if line := log.last(); strings.Contains(line, "suppressed") {
		t.Fatalf("counter must reset after the summary, got %q", line)
	}
}

func (p *KeyProvider) expireInvalidSeqWindow() {
	p.warnMu.Lock()
	defer p.warnMu.Unlock()
	p.lastInvalidSeqWarn = p.lastInvalidSeqWarn.Add(-invalidSeqWarnInterval - time.Second)
}

type warnCounterLogger struct {
	librespot.NullLogger
	mu    sync.Mutex
	warns []string
}

func (l *warnCounterLogger) Warnf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warns = append(l.warns, fmt.Sprintf(format, args...))
}

func (l *warnCounterLogger) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.warns)
}

func (l *warnCounterLogger) last() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.warns[len(l.warns)-1]
}
