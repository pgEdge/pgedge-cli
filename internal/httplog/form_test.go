package httplog

import (
	"strings"
	"testing"
)

func TestRedactFormMasksTheCodeExchange(t *testing.T) {
	got := RedactForm([]byte("grant_type=authorization_code&client_id=cid"+
		"&code=SECRETCODE&redirect_uri=http%3A%2F%2F127.0.0.1&"+
		"code_verifier=SECRETVERIFIER&client_secret=SECRETSECRET"), 4096)
	for _, leak := range []string{"SECRETCODE", "SECRETVERIFIER",
		"SECRETSECRET"} {
		if strings.Contains(got, leak) {
			t.Errorf("form dump leaks %s: %s", leak, got)
		}
	}
	if !strings.HasPrefix(got, "grant_type=authorization_code&"+
		"client_id=cid&code="+mask) {
		t.Errorf("form dump = %s", got)
	}
	if got := RedactForm([]byte("%zz=1"), 100); !strings.Contains(got,
		"redacted") {
		t.Errorf("an unreadable form should be redacted whole: %s", got)
	}
}

func TestRedactBodyMasksAnIDToken(t *testing.T) {
	got := RedactBody([]byte(`{"access_token":"AT","id_token":"IDT",`+
		`"expires_in":1}`), 4096)
	if strings.Contains(got, "IDT") || strings.Contains(got, `"AT"`) {
		t.Errorf("token response dump leaks: %s", got)
	}
}
