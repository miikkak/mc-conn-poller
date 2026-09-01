package cmd

import (
	"log/slog"
	"strings"
	"testing"
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
