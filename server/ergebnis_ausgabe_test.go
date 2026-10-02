package main

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"
)

// 60 Schuesse im ungünstigsten Format muessen in einen QR-Code passen.
func TestQRSVG60Shots(t *testing.T) {
	parts := make([]string, qrMaxShots)
	for i := range parts {
		parts[i] = "-999,-999,109"
	}
	text := "MS1|2147483647|202610011200|Mustermann-Langname, Maximilian-Alexander|Herren II|Luftgewehr Auflage 60 Schuss|lg|z|10|60|1234|" +
		strings.Join(parts, ";")
	svg, err := qrSVG(text)
	if err != nil {
		t.Fatalf("qrSVG (%d Zeichen): %v", len(text), err)
	}
	if !strings.HasPrefix(string(svg), "<svg") {
		t.Fatal("kein SVG")
	}
}

func TestMaskEmail(t *testing.T) {
	for in, want := range map[string]string{
		"max.mustermann@example.de": "ma…@example.de",
		"a@b.de":                    "a…@b.de",
		"kaputt":                    "…",
	} {
		if got := maskEmail(in); got != want {
			t.Errorf("maskEmail(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildMailMessageAttachment(t *testing.T) {
	raw := buildMailMessage(mail.Address{Name: "Schützenverein", Address: "a@b.de"},
		mail.Address{Address: "c@d.de"}, "Dein Ergebnis – LG", "Hallo Jürgen\n", "e.pdf", []byte("%PDF-1.4 test"))
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	subj, _ := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if subj != "Dein Ergebnis – LG" {
		t.Errorf("Subject = %q", subj)
	}
	_, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	mr := multipart.NewReader(msg.Body, params["boundary"])
	var parts []string
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(base64.NewDecoder(base64.StdEncoding, p))
		parts = append(parts, string(data))
	}
	if len(parts) != 2 || parts[0] != "Hallo Jürgen\n" || parts[1] != "%PDF-1.4 test" {
		t.Errorf("Teile = %q", parts)
	}
}
