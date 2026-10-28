package pkg

import (
	"bytes"
	"strings"
	"testing"
)

func TestMaskingWriterAcrossWrites(t *testing.T) {
	var buf bytes.Buffer
	w := NewMaskingWriter(&buf, []string{"s3cr3t-token"})
	if _, err := w.Write([]byte("first line contains s3cr")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("3t-token\nsecond line s3cr3t-token")); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if strings.Contains(got, "s3cr3t-token") {
		t.Fatalf("secret leaked: %q", got)
	}
	if strings.Count(got, "***") != 2 {
		t.Fatalf("masked count = %d, want 2: %q", strings.Count(got, "***"), got)
	}
	if !strings.Contains(got, "first line contains") || !strings.Contains(got, "second line") {
		t.Fatalf("log content lost: %q", got)
	}
}

func TestMaskingWriterIgnoresShortSecrets(t *testing.T) {
	var buf bytes.Buffer
	w := NewMaskingWriter(&buf, []string{"CI", ""})
	if _, err := w.Write([]byte("CI=true\n")); err != nil {
		t.Fatal(err)
	}
	_ = w.Flush()
	if got := buf.String(); got != "CI=true\n" {
		t.Fatalf("short secret should not be masked: %q", got)
	}
}

func TestMaskSecretsFullText(t *testing.T) {
	text := "token=abc123 \n second abc123"
	got := MaskSecrets(text, []string{"abc123"})
	if strings.Contains(got, "abc123") || strings.Count(got, "***") != 2 {
		t.Fatalf("MaskSecrets = %q", got)
	}
}
