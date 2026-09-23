package logrusx

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

func newTestLogger(t *testing.T, opts ...Option) (*logger, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	l, err := New("test-service", append([]Option{WithOutput(buf)}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	return l.(*logger), buf
}

// Must be called only after Close, when no goroutine writes to buf
func readEntries(t *testing.T, buf *bytes.Buffer) []map[string]interface{} {
	t.Helper()
	var entries []map[string]interface{}
	scanner := bufio.NewScanner(buf)
	for scanner.Scan() {
		entry := map[string]interface{}{}
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			t.Fatalf("invalid JSON line %q: %v", scanner.Text(), err)
		}
		entries = append(entries, entry)
	}
	return entries
}

func TestNewInvalidServiceName(t *testing.T) {
	if _, err := New(""); err == nil {
		t.Fatal("expected error for empty service name")
	}
}

func TestNewIgnoresNilOption(t *testing.T) {
	l, buf := newTestLogger(t, nil)
	l.Info("msg")
	l.Close()
	if got := len(readEntries(t, buf)); got != 1 {
		t.Fatalf("expected 1 entry, got %d", got)
	}
}

func TestWarning(t *testing.T) {
	l, buf := newTestLogger(t)

	l.Warning("warning message", LogField{Key: "request_id", Value: "123"})
	l.Close()

	entries := readEntries(t, buf)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e["level"] != "warning" || e["message"] != "warning message" ||
		e["service"] != "test-service" || e["request_id"] != "123" {
		t.Fatalf("unexpected entry: %v", e)
	}
}

func TestCloseFlushesQueue(t *testing.T) {
	l, buf := newTestLogger(t)

	const n = defaultBufferSize * 10
	for i := 0; i < n; i++ {
		l.Info(fmt.Sprintf("msg %d", i))
	}
	l.Close()

	entries := readEntries(t, buf)
	if len(entries) != n {
		t.Fatalf("expected %d entries, got %d", n, len(entries))
	}
	for i, e := range entries {
		if e["message"] != fmt.Sprintf("msg %d", i) {
			t.Fatalf("entry %d out of order: %v", i, e["message"])
		}
	}
}

func TestConcurrentLoggingAndClose(t *testing.T) {
	l, buf := newTestLogger(t)

	var wg sync.WaitGroup
	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				l.Info("msg")
			}
		}()
	}
	go l.Close()
	wg.Wait()
	l.Close()

	if got := len(readEntries(t, buf)); got != 1000 {
		t.Fatalf("expected 1000 entries, got %d", got)
	}
}

func TestLogAfterCloseIsWritten(t *testing.T) {
	l, buf := newTestLogger(t)

	l.Close()
	l.Close()
	l.Error("after close")

	entries := readEntries(t, buf)
	if len(entries) != 1 || entries[0]["message"] != "after close" {
		t.Fatalf("unexpected entries: %v", entries)
	}
}

func TestDebugLevel(t *testing.T) {
	l, buf := newTestLogger(t)
	l.Debug("hidden")
	l.Close()
	if got := len(readEntries(t, buf)); got != 0 {
		t.Fatalf("expected debug to be filtered by default, got %d entries", got)
	}

	l, buf = newTestLogger(t, WithLevel(logrus.DebugLevel))
	l.Debug("visible")
	l.Close()
	entries := readEntries(t, buf)
	if len(entries) != 1 || entries[0]["level"] != "debug" {
		t.Fatalf("unexpected entries: %v", entries)
	}
}

func TestFatalWaitsForWrite(t *testing.T) {
	l, buf := newTestLogger(t)
	exitCode, exitCalls := -1, 0
	l.logrusLogging.ExitFunc = func(code int) { exitCode, exitCalls = code, exitCalls+1 }

	l.Info("before")
	l.Fatal("fatal message")

	// Fatal must return only after the message is written and Exit is called once
	if exitCode != 1 || exitCalls != 1 {
		t.Fatalf("expected one exit with code 1, got %d calls with code %d", exitCalls, exitCode)
	}
	l.Close()

	entries := readEntries(t, buf)
	if len(entries) != 2 || entries[1]["level"] != "fatal" || entries[1]["message"] != "fatal message" {
		t.Fatalf("unexpected entries: %v", entries)
	}
}

func TestFatalExitsWhenLevelDisabled(t *testing.T) {
	l, buf := newTestLogger(t, WithLevel(logrus.PanicLevel))
	exitCode, exitCalls := -1, 0
	l.logrusLogging.ExitFunc = func(code int) { exitCode, exitCalls = code, exitCalls+1 }

	l.Fatal("fatal message")

	if exitCode != 1 || exitCalls != 1 {
		t.Fatalf("expected one exit with code 1, got %d calls with code %d", exitCalls, exitCode)
	}
	l.Close()
	if got := len(readEntries(t, buf)); got != 0 {
		t.Fatalf("expected fatal entry to be filtered, got %d entries", got)
	}
}

// Blocks the first Write until release is closed
type blockingWriter struct {
	bytes.Buffer
	once    sync.Once
	release chan struct{}
}

func (w *blockingWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { <-w.release })
	return w.Buffer.Write(p)
}

func TestLogAfterClosePreservesOrder(t *testing.T) {
	w := &blockingWriter{release: make(chan struct{})}
	lg, err := New("test-service", WithOutput(w))
	if err != nil {
		t.Fatal(err)
	}
	l := lg.(*logger)

	const n = defaultBufferSize
	for i := 0; i < n; i++ {
		l.Info(fmt.Sprintf("msg %d", i))
	}

	closeDone := make(chan struct{})
	go func() {
		defer close(closeDone)
		l.Close()
	}()
	for {
		l.mu.RLock()
		closed := l.closed
		l.mu.RUnlock()
		if closed {
			break
		}
		runtime.Gosched()
	}

	// The queue is still being written while this message is logged
	lastDone := make(chan struct{})
	go func() {
		defer close(lastDone)
		l.Info("last")
	}()
	time.Sleep(10 * time.Millisecond)
	close(w.release)
	<-closeDone
	<-lastDone

	entries := readEntries(t, &w.Buffer)
	if len(entries) != n+1 {
		t.Fatalf("expected %d entries, got %d", n+1, len(entries))
	}
	if entries[n]["message"] != "last" {
		t.Fatalf("expected \"last\" at the end, got %v", entries[n]["message"])
	}
}

func TestFieldsCapturedAtCallTime(t *testing.T) {
	l, buf := newTestLogger(t)

	fields := []LogField{{Key: "k", Value: "original"}}
	l.Info("msg", fields...)
	fields[0].Value = "changed"
	l.Close()

	entries := readEntries(t, buf)
	if len(entries) != 1 || entries[0]["k"] != "original" {
		t.Fatalf("unexpected entries: %v", entries)
	}
}
