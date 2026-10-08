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
