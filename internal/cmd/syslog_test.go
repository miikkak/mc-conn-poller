package cmd

import (
	"context"
	"errors"
	"log/slog"
	"log/syslog"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteAttrNestsNamedGroup(t *testing.T) {
	h := &syslogHandler{}
	var buf strings.Builder
	h.writeAttr(&buf, "", slog.Group("request", slog.String("id", "abc")))

	if got, want := buf.String(), " request.id=abc"; got != want {
		t.Errorf("writeAttr() = %q, want %q", got, want)
	}
}

func TestWriteAttrInlinesEmptyKeyGroupUnderExistingGroup(t *testing.T) {
	h := &syslogHandler{}
	var buf strings.Builder
	// slog.Group("", ...) inlines its attrs into the surrounding group
	// rather than nesting under an empty key.
	h.writeAttr(&buf, "request", slog.Group("", slog.String("id", "abc")))

	if got, want := buf.String(), " request.id=abc"; got != want {
		t.Errorf("writeAttr() = %q, want %q", got, want)
	}
}

func TestWithGroupEmptyNameIsNoOp(t *testing.T) {
	base := &syslogHandler{}
	withGroup, ok := base.WithGroup("request").(*syslogHandler)
	if !ok {
		t.Fatalf("WithGroup() returned %T, want *syslogHandler", withGroup)
	}
	noOp, ok := withGroup.WithGroup("").(*syslogHandler)
	if !ok {
		t.Fatalf("WithGroup(\"\") returned %T, want *syslogHandler", noOp)
	}
	if noOp != withGroup {
		t.Fatalf("WithGroup(\"\") = %+v, want the same handler unchanged", noOp)
	}

	withAttrs, ok := noOp.WithAttrs([]slog.Attr{slog.String("id", "abc")}).(*syslogHandler)
	if !ok {
		t.Fatalf("WithAttrs() returned %T, want *syslogHandler", withAttrs)
	}
	var buf strings.Builder
	for _, ga := range withAttrs.attrs {
		withAttrs.writeAttr(&buf, ga.group, ga.attr)
	}
	if got, want := buf.String(), " request.id=abc"; got != want {
		t.Errorf("attrs rendered = %q, want %q", got, want)
	}
}

func TestWithAttrsCapturesGroupActiveAtAttachTime(t *testing.T) {
	base := &syslogHandler{}
	withGroup, ok := base.WithGroup("request").(*syslogHandler)
	if !ok {
		t.Fatalf("WithGroup() returned %T, want *syslogHandler", withGroup)
	}
	withAttrs, ok := withGroup.WithAttrs([]slog.Attr{slog.String("id", "abc")}).(*syslogHandler)
	if !ok {
		t.Fatalf("WithAttrs() returned %T, want *syslogHandler", withAttrs)
	}

	var buf strings.Builder
	for _, ga := range withAttrs.attrs {
		withAttrs.writeAttr(&buf, ga.group, ga.attr)
	}

	if got, want := buf.String(), " request.id=abc"; got != want {
		t.Errorf("attrs rendered = %q, want %q", got, want)
	}
}

func TestWriteAttrQuotesAmbiguousValues(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{"plain", "abc", " k=abc"},
		{"empty", "", ` k=""`},
		{"space", "no such host", ` k="no such host"`},
		{"equals", "a=b", ` k="a=b"`},
		{"quote", `say "hi"`, ` k="say \"hi\""`},
		{"newline", "a\nb", ` k="a\nb"`},
		{"non-ASCII printable", "häst", " k=häst"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := &syslogHandler{}
			var buf strings.Builder
			h.writeAttr(&buf, "", slog.String("k", tc.value))
			if got := buf.String(); got != tc.want {
				t.Errorf("writeAttr() = %q, want %q", got, tc.want)
			}
		})
	}
}

// fakeSyslogWriter records which severity method each message went through.
type fakeSyslogWriter struct {
	got []string // "<severity>:<message>"
}

func (f *fakeSyslogWriter) Debug(m string) error   { f.got = append(f.got, "debug:"+m); return nil }
func (f *fakeSyslogWriter) Info(m string) error    { f.got = append(f.got, "info:"+m); return nil }
func (f *fakeSyslogWriter) Warning(m string) error { f.got = append(f.got, "warning:"+m); return nil }
func (f *fakeSyslogWriter) Err(m string) error     { f.got = append(f.got, "err:"+m); return nil }

func TestHandleMapsLevelToSyslogSeverity(t *testing.T) {
	tests := []struct {
		level slog.Level
		want  string
	}{
		{slog.LevelDebug - 4, "debug:m"},
		{slog.LevelDebug, "debug:m"},
		{slog.LevelInfo, "info:m"},
		{slog.LevelInfo + 2, "info:m"},
		{slog.LevelWarn, "warning:m"},
		{slog.LevelError, "err:m"},
		{slog.LevelError + 4, "err:m"},
	}
	for _, tc := range tests {
		t.Run(tc.level.String(), func(t *testing.T) {
			w := &fakeSyslogWriter{}
			h := &syslogHandler{writer: w, level: slog.LevelDebug - 8}
			if err := h.Handle(context.Background(), slog.NewRecord(time.Time{}, tc.level, "m", 0)); err != nil {
				t.Fatalf("Handle() = %v, want nil", err)
			}
			if len(w.got) != 1 || w.got[0] != tc.want {
				t.Errorf("writer got %v, want [%s]", w.got, tc.want)
			}
		})
	}
}

func TestHandleRendersMessageWithAttrsAndGroups(t *testing.T) {
	w := &fakeSyslogWriter{}
	logger := slog.New(&syslogHandler{writer: w, level: slog.LevelInfo}).
		With("a", "1").
		WithGroup("g").
		With("b", "2 3")

	logger.Info("msg", "c", "4")

	want := `info:msg a=1 g.b="2 3" g.c=4`
	if len(w.got) != 1 || w.got[0] != want {
		t.Errorf("writer got %v, want [%s]", w.got, want)
	}
}

func TestEnabledHonoursMinimumLevel(t *testing.T) {
	h := &syslogHandler{level: slog.LevelWarn}
	ctx := context.Background()
	if h.Enabled(ctx, slog.LevelInfo) {
		t.Error("Enabled(info) = true at warn level, want false")
	}
	if !h.Enabled(ctx, slog.LevelWarn) || !h.Enabled(ctx, slog.LevelError) {
		t.Error("Enabled(warn/error) = false at warn level, want true")
	}
}

// stubSyslogDaemon points dialSyslog at a unixgram socket the test owns and
// returns it, so records can be read back without a real syslog daemon.
func stubSyslogDaemon(t *testing.T) net.PacketConn {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "log.sock")
	pc, err := (&net.ListenConfig{}).ListenPacket(context.Background(), "unixgram", sock)
	if err != nil {
		t.Fatalf("listen on %s: %v", sock, err)
	}
	t.Cleanup(func() { _ = pc.Close() })

	orig := dialSyslog
	dialSyslog = func(tag string) (*syslog.Writer, error) {
		return syslog.Dial("unixgram", sock, syslog.LOG_DAEMON|syslog.LOG_INFO, tag)
	}
	t.Cleanup(func() { dialSyslog = orig })
	return pc
}

func TestNewLoggerSyslogDeliversRecords(t *testing.T) {
	pc := stubSyslogDaemon(t)

	logger, err := newLogger("info", true)
	if err != nil {
		t.Fatalf("newLogger(syslog) = %v, want nil", err)
	}
	logger.Warn("hello", "k", "v")

	// The write to the datagram socket completed before Warn returned, so the
	// read does not wait; the deadline is only a safety net.
	_ = pc.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 2048)
	n, _, err := pc.ReadFrom(buf)
	if err != nil {
		t.Fatalf("reading syslog datagram: %v", err)
	}
	got := string(buf[:n])
	for _, want := range []string{"<28>", "mc-conn-poller", "hello k=v"} { // 28 = daemon (24) + warning (4)
		if !strings.Contains(got, want) {
			t.Errorf("syslog datagram %q does not contain %q", got, want)
		}
	}
}

func TestNewLoggerReportsSyslogConnectionFailure(t *testing.T) {
	orig := dialSyslog
	dialSyslog = func(string) (*syslog.Writer, error) { return nil, errors.New("no syslog here") }
	t.Cleanup(func() { dialSyslog = orig })

	_, err := newLogger("info", true)
	if err == nil || !strings.Contains(err.Error(), "initializing syslog logger") || !strings.Contains(err.Error(), "no syslog here") {
		t.Errorf("newLogger(syslog) error = %v, want it to wrap the connection failure", err)
	}
}
