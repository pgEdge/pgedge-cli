package settle

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/pgEdge/pgedge-cli/internal/starfleet/conn"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// TestAwait: a transitional status is polled through, any other status
// ends the wait, and a 404 is the end only for a delete.
func TestAwait(t *testing.T) {
	notFound := conn.NewExitError("not found", conn.ExitNotFound)
	boom := errors.New("boom")
	cases := []struct {
		name     string
		want     string
		reads    []any // a status string or an error
		timeout  time.Duration
		wantCode int
		wantErr  error
		wantText string
	}{
		{"reaches want", "available",
			[]any{"modifying", "available"}, time.Minute, 0, nil,
			"database d: available."},
		{"rests elsewhere", "available",
			[]any{"degraded"}, time.Minute, conn.ExitGeneral, nil,
			`is "degraded", not available`},
		{"404 ends a delete", Gone,
			[]any{"deleting", notFound}, time.Minute, 0, nil,
			"database d: deleted."},
		{"404 fails anything else", "available",
			[]any{notFound}, time.Minute, conn.ExitNotFound, nil, ""},
		{"read error passes through", "available",
			[]any{boom}, time.Minute, 0, boom, ""},
		{"never settles", "available",
			[]any{"modifying"}, 0, conn.ExitTimeout, nil,
			"last status: modifying"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rt, _, errb := testsupport.NewRuntime(t, "", "text")
			n := 0
			target := Target{Noun: "database", ID: "d", Want: tc.want,
				Transitional: []string{"modifying", "deleting"},
				Read: func(context.Context) (string, error) {
					r := tc.reads[min(n, len(tc.reads)-1)]
					n++
					if err, ok := r.(error); ok {
						return "", err
					}
					return r.(string), nil
				}}
			err := Await(rt, target, time.Now().Add(tc.timeout),
				int(tc.timeout.Seconds()), time.Millisecond, time.Second)
			got := errb.String()
			if err != nil {
				got = err.Error()
			}
			switch {
			case tc.wantErr != nil:
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("want %v, got %v", tc.wantErr, err)
				}
			case tc.wantCode == 0:
				if err != nil {
					t.Fatalf("want success, got %v", err)
				}
			default:
				var ee *conn.ExitError
				if !errors.As(err, &ee) || ee.Code() != tc.wantCode {
					t.Fatalf("want exit %d, got %v", tc.wantCode, err)
				}
			}
			if !strings.Contains(got, tc.wantText) {
				t.Errorf("want %q in %q", tc.wantText, got)
			}
		})
	}
}
