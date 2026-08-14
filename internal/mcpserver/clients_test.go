package mcpserver

import (
	"testing"
	"unicode/utf8"
)

func TestTruncateUTF8_ASCIIShorter(t *testing.T) {
	got := truncateUTF8("hello", maxClientFieldLen)
	if got != "hello" {
		t.Errorf("truncateUTF8(\"hello\") = %q, want \"hello\"", got)
	}
}

func TestTruncateUTF8_ASCLongExact(t *testing.T) {
	long := ""
	for i := 0; i < 64; i++ {
		long += string(rune('a' + i%26))
	}
	if len(long) != 64 {
		t.Fatalf("test string length = %d, want 64", len(long))
	}
	got := truncateUTF8(long, maxClientFieldLen)
	if got != long {
		t.Errorf("truncateUTF8(64-byte ASCII) = %q (len=%d), want unchanged", got, len(got))
	}
}

func TestTruncateUTF8_ASCLonger(t *testing.T) {
	long := ""
	for i := 0; i < 66; i++ {
		long += string(rune('a' + i%26))
	}
	if len(long) != 66 {
		t.Fatalf("test string length = %d, want 66", len(long))
	}
	got := truncateUTF8(long, maxClientFieldLen)
	if len(got) != 64 {
		t.Errorf("truncateUTF8(66-byte ASCII) byte length = %d, want 64", len(got))
	}
	if got != long[:64] {
		t.Errorf("truncateUTF8(66-byte ASCII) = %q, want first 64 bytes", got)
	}
}

func TestTruncateUTF8_3ByteRunes(t *testing.T) {
	// Each rune is 3 bytes in UTF-8 (U+4E00 = 一).
	// 21 runes = 63 bytes, 22 runes = 66 bytes > 64.
	s := ""
	for i := 0; i < 100; i++ {
		s += "\u4e00" // CJK ideograph = 3 bytes in UTF-8
	}
	got := truncateUTF8(s, maxClientFieldLen)
	wantLen := 63
	if len(got) != wantLen {
		t.Errorf("truncateUTF8(100x3-byte runes) byte length = %d, want %d", len(got), wantLen)
	}
	if !utf8.ValidString(got) {
		t.Errorf("truncateUTF8(100x3-byte runes) result is not valid UTF-8")
	}
	wantRunes := 21
	gotRunes := utf8.RuneCountInString(got)
	if gotRunes != wantRunes {
		t.Errorf("truncateUTF8(100x3-byte runes) rune count = %d, want %d", gotRunes, wantRunes)
	}
}

func TestTruncateUTF8_Empty(t *testing.T) {
	got := truncateUTF8("", maxClientFieldLen)
	if got != "" {
		t.Errorf("truncateUTF8(\"\") = %q, want \"\"", got)
	}
}

func TestTruncateUTF8_MaxZero(t *testing.T) {
	got := truncateUTF8("hello", 0)
	if got != "" {
		t.Errorf("truncateUTF8(\"hello\", 0) = %q, want \"\"", got)
	}
}
