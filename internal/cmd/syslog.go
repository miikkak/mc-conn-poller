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
	attrs  []groupedAttr
	group  string
}

// groupedAttr pairs an attr attached via With with the group it was under
// at the time, since a later Handle needs that prefix even though the
// record's own group may have changed since.
type groupedAttr struct {
	group string
	attr  slog.Attr
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

	for _, ga := range h.attrs {
		h.writeAttr(&buf, ga.group, ga.attr)
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
	value := a.Value.Resolve()
	if value.Kind() == slog.KindGroup {
		// An empty-keyed group inlines its attrs under the current group
		// rather than nesting, matching slog's own WithGroup("") contract.
		subGroup := joinKey(group, a.Key)
		for _, ga := range value.Group() {
			h.writeAttr(buf, subGroup, ga)
		}
		return
	}
	fmt.Fprintf(buf, " %s=%v", joinKey(group, a.Key), value)
}

func joinKey(group, key string) string {
	switch {
	case group == "":
		return key
	case key == "":
		return group
	default:
		return group + "." + key
	}
}

func (h *syslogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	nh := *h
	nh.attrs = append(append([]groupedAttr{}, h.attrs...), toGroupedAttrs(h.group, attrs)...)
	return &nh
}

func toGroupedAttrs(group string, attrs []slog.Attr) []groupedAttr {
	ga := make([]groupedAttr, len(attrs))
	for i, a := range attrs {
		ga[i] = groupedAttr{group: group, attr: a}
	}
	return ga
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
