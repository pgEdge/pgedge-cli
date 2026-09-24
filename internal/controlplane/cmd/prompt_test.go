package cmd

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func rdr(s string) *bufio.Reader { return bufio.NewReader(strings.NewReader(s)) }

func TestPromptStringDefaultOnEmpty(t *testing.T) {
	var errBuf bytes.Buffer
	got, err := promptString(rdr("\n"), &errBuf, "Name", "my-database")
	if err != nil {
		t.Fatal(err)
	}
	if got != "my-database" {
		t.Fatalf("got %q, want default my-database", got)
	}
	if !strings.Contains(errBuf.String(), "[my-database]") {
		t.Fatalf("prompt should show default: %q", errBuf.String())
	}
}

func TestPromptStringValue(t *testing.T) {
	var errBuf bytes.Buffer
	got, _ := promptString(rdr("storefront\n"), &errBuf, "Name", "x")
	if got != "storefront" {
		t.Fatalf("got %q, want storefront", got)
	}
}

func TestPromptIntValueAndDefault(t *testing.T) {
	var errBuf bytes.Buffer
	got, err := promptInt(rdr("5\n"), &errBuf, "Nodes", 3)
	if err != nil || got != 5 {
		t.Fatalf("got %d err %v, want 5", got, err)
	}
	got, _ = promptInt(rdr("\n"), &errBuf, "Nodes", 3)
	if got != 3 {
		t.Fatalf("empty should yield default 3, got %d", got)
	}
}

func TestPromptIntRejectsNonNumber(t *testing.T) {
	var errBuf bytes.Buffer
	// "abc" invalid, then "4" accepted after re-prompt.
	got, err := promptInt(rdr("abc\n4\n"), &errBuf, "Nodes", 3)
	if err != nil || got != 4 {
		t.Fatalf("got %d err %v, want 4 after reprompt", got, err)
	}
}

func TestPromptYesNo(t *testing.T) {
	var errBuf bytes.Buffer
	if v, _ := promptYesNo(rdr("y\n"), &errBuf, "OK?", false); !v {
		t.Fatal("y should be true")
	}
	if v, _ := promptYesNo(rdr("\n"), &errBuf, "OK?", false); v {
		t.Fatal("empty should take default false")
	}
}

func TestPromptOptionalInt(t *testing.T) {
	var errBuf bytes.Buffer
	// blank -> "" (omit)
	got, err := promptOptionalInt(rdr("\n"), &errBuf, "Port")
	if err != nil || got != "" {
		t.Fatalf("blank: got %q err %v, want \"\"", got, err)
	}
	// non-numeric then valid -> re-prompt then canonical int
	got, err = promptOptionalInt(rdr("abc\n007\n"), &errBuf, "Port")
	if err != nil || got != "7" {
		t.Fatalf("reprompt: got %q err %v, want 7", got, err)
	}
	if !strings.Contains(errBuf.String(), `not a number: "abc"`) {
		t.Fatalf("expected re-prompt message: %q", errBuf.String())
	}
}

func TestPromptRequired(t *testing.T) {
	var errBuf bytes.Buffer
	// blank then value -> re-prompt then accept
	got, err := promptRequired(rdr("\nstorefront\n"), &errBuf, "Name")
	if err != nil || got != "storefront" {
		t.Fatalf("got %q err %v, want storefront", got, err)
	}
	if !strings.Contains(errBuf.String(), "a value is required") {
		t.Fatalf("expected required message: %q", errBuf.String())
	}
	// end-of-input with only blanks -> error, not an infinite loop
	if _, err := promptRequired(rdr(""), &errBuf, "Name"); err == nil {
		t.Fatal("EOF on required field should return an error")
	}
}

func TestPromptEnumRepromptsUntilValid(t *testing.T) {
	var errBuf bytes.Buffer
	got, err := promptEnum(rdr("bogus\ns3\n"), &errBuf, "Type",
		[]string{"s3", "gcs", "azure", "posix", "cifs"}, "s3")
	if err != nil || got != "s3" {
		t.Fatalf("got %q err %v, want s3", got, err)
	}
	got, _ = promptEnum(rdr("\n"), &errBuf, "Type",
		[]string{"s3", "gcs"}, "gcs")
	if got != "gcs" {
		t.Fatalf("empty should take default gcs, got %q", got)
	}
}
