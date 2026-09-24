package cmd

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/cli"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

// multiRegionClusterBody is a cluster whose nodes and networks sit in
// two regions. Shaped after a real one: on a live BYOC tenant,
// 2026-08-22, three `available` clusters had a node in every region
// they declared.
const multiRegionClusterBody = `{"id":"` + testClusterID + `",` +
	`"name":"prod","status":"available",` +
	`"regions":["us-east-2","eu-west-1"],` +
	`"nodes":[` +
	`{"name":"n1","region":"us-east-2","instance_type":"t4g.small"},` +
	`{"name":"n2","region":"eu-west-1","instance_type":"t4g.small"}],` +
	`"networks":[` +
	`{"cidr":"10.1.0.0/16","region":"us-east-2"},` +
	`{"cidr":"10.2.0.0/16","region":"eu-west-1"}],` +
	`"created_at":"2024-03-15T10:30:00Z"}`

func multiRegionServer(t *testing.T) string {
	t.Helper()
	return testsupport.NewAuthedServer(t,
		func(w http.ResponseWriter, r *http.Request) {
			testsupport.JSONHandler(
				http.StatusOK, multiRegionClusterBody)(w, r)
		})
}

// TestClusterUpdateRefusesToStrandNodes covers the case the CLI cannot
// express. `cluster update` sends regions, nodes and networks in one
// body and has no --nodes or --networks flag, so dropping a region
// leaves the nodes and networks in it untouched in the very same
// request. Rather than send a body that contradicts itself, the verb
// refuses.
func TestClusterUpdateRefusesToStrandNodes(t *testing.T) {
	t.Run("dropping a region with nodes in it is refused",
		func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			err := runAuthed(t, rt, out, multiRegionServer(t),
				"cluster", "update", testClusterID,
				"--regions", "us-east-2")
			if err == nil {
				t.Fatal("dropping eu-west-1 was accepted")
			}
			var ue *cli.UsageError
			if !errors.As(err, &ue) {
				t.Fatalf("want a usage error (exit 2), got %v (%T)",
					err, err)
			}
			// The message has to name what blocks it: "invalid" alone
			// leaves the operator guessing which region and why. The
			// label is pinned too: renaming "nodes:" escaped every
			// test until this line.
			for _, want := range []string{"nodes: n2 (eu-west-1)"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("message does not name %q: %v", want, err)
				}
			}
		})

	// Adding is not dropping, so it must still work. Without this the
	// guard could refuse every --regions and still pass the case above.
	t.Run("adding a region is untouched", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		if err := runAuthed(t, rt, out, multiRegionServer(t),
			"cluster", "update", testClusterID,
			"--regions", "us-east-2,eu-west-1,af-south-1"); err != nil {
			t.Fatalf("adding a region was refused: %v", err)
		}
	})

	// Re-sending exactly what the cluster already has drops nothing.
	t.Run("an unchanged region set is untouched", func(t *testing.T) {
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		if err := runAuthed(t, rt, out, multiRegionServer(t),
			"cluster", "update", testClusterID,
			"--regions", "us-east-2,eu-west-1"); err != nil {
			t.Fatalf("an unchanged region set was refused: %v", err)
		}
	})

	// A region with nothing in it can be dropped: that body agrees
	// with itself. This is the case a blanket refusal would break.
	t.Run("dropping an empty region is allowed", func(t *testing.T) {
		const emptyThirdRegion = `{"id":"` + testClusterID + `",` +
			`"name":"prod","status":"available",` +
			`"regions":["us-east-2","eu-west-1","af-south-1"],` +
			`"nodes":[{"name":"n1","region":"us-east-2"}],` +
			`"networks":[{"cidr":"10.1.0.0/16","region":"us-east-2"}],` +
			`"created_at":"2024-03-15T10:30:00Z"}`
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			func(w http.ResponseWriter, r *http.Request) {
				testsupport.JSONHandler(
					http.StatusOK, emptyThirdRegion)(w, r)
			})
		if err := runAuthed(t, rt, out, url, "cluster", "update",
			testClusterID,
			"--regions", "us-east-2,eu-west-1"); err != nil {
			t.Fatalf("dropping an empty region was refused: %v", err)
		}
	})

	// A network can strand without a node stranding, and the message
	// must say so rather than reporting nothing.
	t.Run("a stranded network alone is refused", func(t *testing.T) {
		const networkOnly = `{"id":"` + testClusterID + `",` +
			`"name":"prod","status":"available",` +
			`"regions":["us-east-2","eu-west-1"],` +
			`"nodes":[{"name":"n1","region":"us-east-2"}],` +
			`"networks":[` +
			`{"cidr":"10.1.0.0/16","region":"us-east-2"},` +
			`{"cidr":"10.2.0.0/16","region":"eu-west-1"}],` +
			`"created_at":"2024-03-15T10:30:00Z"}`
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			func(w http.ResponseWriter, r *http.Request) {
				testsupport.JSONHandler(
					http.StatusOK, networkOnly)(w, r)
			})
		err := runAuthed(t, rt, out, url, "cluster", "update",
			testClusterID, "--regions", "us-east-2")
		if err == nil {
			t.Fatal("stranding a network was accepted")
		}
		if !strings.Contains(err.Error(), "networks in: eu-west-1") {
			t.Errorf("message does not name the stranded network: %v",
				err)
		}
	})

	// A node with NO name. Name is optional on ClusterNodeSettings, so
	// a guard that skipped nameless nodes would not be doing its job
	// on a response shape the contract permits — and review proved
	// exactly that mutation survives without this case.
	t.Run("an unnamed node still strands", func(t *testing.T) {
		const unnamed = `{"id":"` + testClusterID + `",` +
			`"name":"prod","status":"available",` +
			`"regions":["us-east-2","eu-west-1"],` +
			`"nodes":[{"region":"us-east-2"},{"region":"eu-west-1"}],` +
			`"networks":[{"cidr":"10.1.0.0/16","region":"us-east-2"}],` +
			`"created_at":"2024-03-15T10:30:00Z"}`
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			func(w http.ResponseWriter, r *http.Request) {
				testsupport.JSONHandler(
					http.StatusOK, unnamed)(w, r)
			})
		err := runAuthed(t, rt, out, url, "cluster", "update",
			testClusterID, "--regions", "us-east-2")
		if err == nil {
			t.Fatal("an unnamed node in a dropped region was ignored")
		}
		if !strings.Contains(err.Error(),
			"an unnamed node in eu-west-1") {
			t.Errorf("message does not describe the unnamed node: %v",
				err)
		}
	})

	// The node's OWN region must appear. The first subtest sees
	// eu-west-1 in the message, but a network supplies it there too —
	// so nothing proved the node clause carried it until this case,
	// where the stranded region holds a node and no network.
	t.Run("a stranded node names its own region", func(t *testing.T) {
		const nodeOnly = `{"id":"` + testClusterID + `",` +
			`"name":"prod","status":"available",` +
			`"regions":["us-east-2","eu-west-1"],` +
			`"nodes":[{"name":"n1","region":"us-east-2"},` +
			`{"name":"n2","region":"eu-west-1"}],` +
			`"networks":[{"cidr":"10.1.0.0/16","region":"us-east-2"}],` +
			`"created_at":"2024-03-15T10:30:00Z"}`
		rt, out, _ := testsupport.NewRuntime(t, "", "text")
		url := testsupport.NewAuthedServer(t,
			func(w http.ResponseWriter, r *http.Request) {
				testsupport.JSONHandler(
					http.StatusOK, nodeOnly)(w, r)
			})
		err := runAuthed(t, rt, out, url, "cluster", "update",
			testClusterID, "--regions", "us-east-2")
		if err == nil {
			t.Fatal("a stranded node with no stranded network was " +
				"accepted")
		}
		if !strings.Contains(err.Error(), "n2 (eu-west-1)") {
			t.Errorf("node clause does not carry the node's region: %v",
				err)
		}
		if strings.Contains(err.Error(), "networks in:") {
			t.Errorf("reported a stranded network where none is: %v",
				err)
		}
	})

	// Whitespace after the comma is pflag's CSV, not the user's
	// mistake: StringSliceVar leaves " eu-west-1" as-is. Untrimmed,
	// the guard refuses and names a region the user plainly passed.
	t.Run("whitespace after a comma is not a stranding",
		func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			if err := runAuthed(t, rt, out, multiRegionServer(t),
				"cluster", "update", testClusterID,
				"--regions", "us-east-2, eu-west-1"); err != nil {
				t.Fatalf("a space after the comma was refused: %v", err)
			}
		})

	// Two networks in one stranded region are one problem, not two.
	t.Run("a repeated stranded region is named once",
		func(t *testing.T) {
			const twoNets = `{"id":"` + testClusterID + `",` +
				`"name":"prod","status":"available",` +
				`"regions":["us-east-2","eu-west-1"],` +
				`"nodes":[{"name":"n1","region":"us-east-2"}],` +
				`"networks":[` +
				`{"cidr":"10.1.0.0/16","region":"us-east-2"},` +
				`{"cidr":"10.2.0.0/16","region":"eu-west-1"},` +
				`{"cidr":"10.3.0.0/16","region":"eu-west-1"}],` +
				`"created_at":"2024-03-15T10:30:00Z"}`
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			url := testsupport.NewAuthedServer(t,
				func(w http.ResponseWriter, r *http.Request) {
					testsupport.JSONHandler(
						http.StatusOK, twoNets)(w, r)
				})
			err := runAuthed(t, rt, out, url, "cluster", "update",
				testClusterID, "--regions", "us-east-2")
			if err == nil {
				t.Fatal("expected a refusal")
			}
			if strings.Count(err.Error(), "eu-west-1") != 1 {
				t.Errorf("stranded region named more than once: %v",
					err)
			}
		})

	// The refusal must not send the operator somewhere that does not
	// exist. byoc has no node-removing endpoint, so "remove the nodes
	// first" was a dead end.
	t.Run("the refusal offers no unreachable remedy",
		func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			err := runAuthed(t, rt, out, multiRegionServer(t),
				"cluster", "update", testClusterID,
				"--regions", "us-east-2")
			if err == nil {
				t.Fatal("expected a refusal")
			}
			if strings.Contains(err.Error(), "remove the nodes") {
				t.Errorf("message tells the operator to do something "+
					"no pgedge command and no byoc endpoint can do: %v",
					err)
			}
		})

	// The other flags must not have picked up the guard: an update
	// that names no regions drops nothing, whatever the cluster holds.
	t.Run("a firewall-rule-only update is untouched",
		func(t *testing.T) {
			rt, out, _ := testsupport.NewRuntime(t, "", "text")
			if err := runAuthed(t, rt, out, multiRegionServer(t),
				"cluster", "update", testClusterID,
				"--firewall-rule",
				"name=https,port=443,sources=0.0.0.0/0"); err != nil {
				t.Fatalf("firewall-rule-only update was refused: %v",
					err)
			}
		})
}
