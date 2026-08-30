package cmd

import (
	"context"
	"fmt"
	"log/syslog"
	"strings"

	"log/slog"
)

// syslogHandler is a minimal slog.Handler that forwards records to the local
// syslog daemon (facility daemon), mapping slog levels to syslog severities
// so syslogd's own filtering/rotation/permissions apply to daemon logs
// instead of a file the daemon or supervise-daemon has to manage itself.
type syslogHandler struct {
	writer *syslog.Writer
	level  slog.Leveler
	attrs  []slog.Attr
	group  string
}

// newSyslogHandler dials the local syslog daemon under the given tag. The
// severity passed to syslog.New is only the default for Write(); every
// record is actually emitted via the Writer's per-severity methods, so it's
// irrelevant here.
func newSyslogHandler(tag string, level slog.Leveler) (*syslogHandler, error) {
	w, err := syslog.New(syslog.LOG_DAEMON|syslog.LOG_INFO, tag)
	if err != nil {
		return nil, fmt.Errorf("connecting to syslog: %w", err)
	}
	return &syslogHandler{writer: w, level: level}, nil
}

func (h *syslogHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level.Level()
}

func (h *syslogHandler) Handle(_ context.Context, r slog.Record) error {
	var buf strings.Builder
	buf.WriteString(r.Message)

	for _, a := range h.attrs {
		h.writeAttr(&buf, "", a)
	}
	r.Attrs(func(a slog.Attr) bool {
		h.writeAttr(&buf, h.group, a)
		return true
	})

	msg := buf.String()
	switch {
	case r.Level >= slog.LevelError:
		return h.writer.Err(msg)
	case r.Level >= slog.LevelWarn:
		return h.writer.Warning(msg)
	case r.Level >= slog.LevelInfo:
		return h.writer.Info(msg)
	default:
		return h.writer.Debug(msg)
	}
}

func (h *syslogHandler) writeAttr(buf *strings.Builder, group string, a slog.Attr) {
	if a.Equal(slog.Attr{}) {
		return
	}
	key := a.Key
	if group != "" {
		key = group + "." + key
	}
	fmt.Fprintf(buf, " %s=%v", key, a.Value)
}

func (h *syslogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	nh := *h
	nh.attrs = append(append([]slog.Attr{}, h.attrs...), attrs...)
	return &nh
}

func (h *syslogHandler) WithGroup(name string) slog.Handler {
	nh := *h
	if nh.group != "" {
		nh.group = nh.group + "." + name
	} else {
		nh.group = name
	}
	return &nh
}
