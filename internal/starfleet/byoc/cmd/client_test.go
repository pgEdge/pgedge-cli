package cmd

import (
	"testing"
)

// TestCheckResponse pins byoc's exit-code vocabulary. checkResponse and
// the Exit* constants are now thin aliases over the starfleet module's own
// conn package (which has its own tests for the same behaviour), so this
// table is deliberately kept: it is what fails if byoc is ever pointed
// at a package with a different code for the same status.
func TestCheckResponse(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     string
		wantCode int
		wantNil  bool
	}{
		{"200 OK", 200, "", 0, true},
		{"201 Created", 201, "", 0, true},
		{"204 No Content", 204, "", 0, true},
		{"401 Unauthorized", 401, "unauthorized", ExitAuth, false},
		{"403 Forbidden", 403, "forbidden", ExitAuth, false},
		{"404 Not Found", 404, `{"code":404,"message":"not found"}`, ExitNotFound, false},
		{"408 Timeout", 408, "timeout", ExitTimeout, false},
		{"500 Internal", 500, "internal error", ExitGeneral, false},
		{"504 Gateway Timeout", 504, "gateway timeout", ExitTimeout, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkResponse(tt.status, tt.body)
			if tt.wantNil {
				if err != nil {
					t.Errorf("checkResponse(%d) = %v, want nil", tt.status, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("checkResponse(%d) = nil, want error", tt.status)
			}
			ee, ok := err.(*ExitError)
			if !ok {
				t.Fatalf("checkResponse(%d) returned %T, want *ExitError", tt.status, err)
			}
			if ee.Code() != tt.wantCode {
				t.Errorf("exit code = %d, want %d", ee.Code(), tt.wantCode)
			}
		})
	}
}
