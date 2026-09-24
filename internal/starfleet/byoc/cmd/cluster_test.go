package cmd

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/byoc/api"
	"github.com/pgEdge/pgedge-cli/internal/testsupport"
)

func TestNewClusterCmd(t *testing.T) {
	rt, _, _ := testsupport.NewRuntime(t, "", "table")
	cmd := NewClusterCmd(rt)

	if cmd.Use != "cluster" {
		t.Errorf("Use = %q, want \"cluster\"", cmd.Use)
	}
	found := false
	for _, a := range cmd.Aliases {
		if a == "clusters" {
			found = true
		}
	}
	if !found {
		t.Errorf("Aliases = %v, want it to contain \"clusters\"", cmd.Aliases)
	}

	want := map[string]bool{
		"list": false, "get": false, "create": false,
		"delete": false, "update": false, "share": false,
	}
	for _, sub := range cmd.Commands() {
		name := strings.Fields(sub.Use)[0]
		if _, ok := want[name]; ok {
			want[name] = true
		}
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("cluster command missing subcommand %q", name)
		}
	}
}

func TestBackupStoreWarning(t *testing.T) {
	if w := backupStoreWarning([]string{"bs-abc"}); w != "" {
		t.Errorf("with a store, want empty warning, got %q", w)
	}
	if w := backupStoreWarning(nil); w == "" {
		t.Error("without a store, want a non-empty warning, got empty")
	}
}

func strSlice(p *[]string) []string {
	if p == nil {
		return nil
	}
	return *p
}

func TestParseFirewallRule(t *testing.T) {
	name := func(p *api.ClusterFirewallRuleSettingsName) string {
		if p == nil {
			return ""
		}
		return string(*p)
	}

	t.Run("happy path with one source", func(t *testing.T) {
		r, err := parseFirewallRule("name=https,port=443,sources=0.0.0.0/0")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if name(r.Name) != "https" {
			t.Errorf("name = %q, want https", name(r.Name))
		}
		if r.Port != 443 {
			t.Errorf("port = %d, want 443", r.Port)
		}
		if got := strSlice(r.Sources); len(got) != 1 || got[0] != "0.0.0.0/0" {
			t.Errorf("sources = %v, want [0.0.0.0/0]", got)
		}
	})

	t.Run("repeated key accumulates sources", func(t *testing.T) {
		r, err := parseFirewallRule("name=ssh,port=22,sources=10.0.0.0/8,sources=192.168.0.0/16")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := strSlice(r.Sources); len(got) != 2 {
			t.Errorf("sources = %v, want 2 elements", got)
		}
	})

	errCases := []struct {
		name string
		in   string
	}{
		{"missing port", "name=https,sources=0.0.0.0/0"},
		{"non-int port", "name=https,port=https"},
		{"unknown key", "name=https,port=443,colour=red"},
		{"not key=value", "name=https,port=443,bogus"},
		{"missing name", "port=443,sources=0.0.0.0/0"},
		{"invalid name", "name=pg,port=5432,sources=0.0.0.0/0"},
	}
	for _, tc := range errCases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseFirewallRule(tc.in); err == nil {
				t.Errorf("parseFirewallRule(%q) = nil error, want error", tc.in)
			}
		})
	}
}

func TestBuildClusterUpdate(t *testing.T) {
	existingRule := api.ClusterFirewallRuleSettings{Port: 22}
	c := &api.Cluster{
		Regions:        []string{"us-east-1"},
		FirewallRules:  &[]api.ClusterFirewallRuleSettings{existingRule},
		BackupStoreIds: &[]string{"bs-existing"},
	}
	newRule := api.ClusterFirewallRuleSettings{Port: 443}

	t.Run("appends rules and stores, keeps regions", func(t *testing.T) {
		in := buildClusterUpdate(c,
			[]api.ClusterFirewallRuleSettings{newRule},
			[]string{"bs-new"}, nil)
		if in.FirewallRules == nil || len(*in.FirewallRules) != 2 {
			t.Fatalf("firewall rules = %v, want 2", in.FirewallRules)
		}
		if in.BackupStoreIds == nil || len(*in.BackupStoreIds) != 2 {
			t.Fatalf("backup stores = %v, want 2", in.BackupStoreIds)
		}
		if len(in.Regions) != 1 || in.Regions[0] != "us-east-1" {
			t.Errorf("regions = %v, want [us-east-1]", in.Regions)
		}
	})

	t.Run("regions override when supplied", func(t *testing.T) {
		in := buildClusterUpdate(c, nil, nil, []string{"eu-west-1"})
		if len(in.Regions) != 1 || in.Regions[0] != "eu-west-1" {
			t.Errorf("regions = %v, want [eu-west-1]", in.Regions)
		}
	})
}

func TestParseClusterNetwork(t *testing.T) {
	str := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}

	boolPtr := func(b bool) *bool { return &b }

	tests := []struct {
		name          string
		in            string
		defaultRegion string
		wantErr       bool
		wantRegion    string
		wantCidr      string
		wantPublic    []string
		wantPrivate   []string
		wantSubnets   []string
		wantExternal  *bool
		wantExtID     string
		wantName      string
	}{
		{
			name:       "public cluster network",
			in:         "region=us-east-1,cidr=10.4.0.0/16,public-subnets=10.4.1.0/24",
			wantRegion: "us-east-1",
			wantCidr:   "10.4.0.0/16",
			wantPublic: []string{"10.4.1.0/24"},
		},
		// --- #134: the GCP shape, and the keys that were unreachable ---
		{
			// The case the flag could not express before #134. saas's
			// Google validator rejects both public_subnets and
			// private_subnets ("use subnets instead"), so this is the
			// ONLY way to name GCP subnets from the CLI.
			name:        "GCP network uses subnets",
			in:          "region=us-central1,cidr=10.6.0.0/16,subnets=10.6.1.0/24",
			wantRegion:  "us-central1",
			wantCidr:    "10.6.0.0/16",
			wantSubnets: []string{"10.6.1.0/24"},
		},
		{
			name:        "repeated subnets accumulate",
			in:          "region=us-central1,subnets=10.6.1.0/24,subnets=10.6.2.0/24",
			wantRegion:  "us-central1",
			wantSubnets: []string{"10.6.1.0/24", "10.6.2.0/24"},
		},
		{
			name:         "external network with external-id and name",
			in:           "region=us-east-1,external=true,external-id=vpc-0abc,name=shared-vpc",
			wantRegion:   "us-east-1",
			wantExternal: boolPtr(true),
			wantExtID:    "vpc-0abc",
			wantName:     "shared-vpc",
		},
		{
			// external=false must reach the wire as an explicit false,
			// not vanish into an unset pointer: "not external" and "did
			// not say" are different requests.
			name:         "external=false is carried, not dropped",
			in:           "region=us-east-1,external=false",
			wantRegion:   "us-east-1",
			wantExternal: boolPtr(false),
		},
		{
			name:         "external accepts strconv.ParseBool spellings",
			in:           "region=us-east-1,external=1",
			wantRegion:   "us-east-1",
			wantExternal: boolPtr(true),
		},
		{
			name:    "external rejects a non-boolean",
			in:      "region=us-east-1,external=yes-please",
			wantErr: true,
		},
		{
			name: "private cluster network with both subnet kinds",
			in: "region=us-east-1,cidr=10.3.0.0/16," +
				"public-subnets=10.3.1.0/24,private-subnets=10.3.128.0/24",
			wantRegion:  "us-east-1",
			wantCidr:    "10.3.0.0/16",
			wantPublic:  []string{"10.3.1.0/24"},
			wantPrivate: []string{"10.3.128.0/24"},
		},
		{
			name:       "repeated subnets accumulate",
			in:         "region=us-east-1,public-subnets=10.4.1.0/24,public-subnets=10.4.2.0/24",
			wantRegion: "us-east-1",
			wantPublic: []string{"10.4.1.0/24", "10.4.2.0/24"},
		},
		{
			name:          "region defaults on single-region clusters",
			in:            "cidr=10.4.0.0/16",
			defaultRegion: "us-east-1",
			wantRegion:    "us-east-1",
			wantCidr:      "10.4.0.0/16",
		},
		{
			name:    "region required on multi-region clusters",
			in:      "cidr=10.4.0.0/16",
			wantErr: true,
		},
		{
			name:    "unknown key",
			in:      "region=us-east-1,subnet=10.4.1.0/24",
			wantErr: true,
		},
		{
			name:    "not key=value",
			in:      "region=us-east-1,bogus",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, err := parseClusterNetwork(tt.in, tt.defaultRegion)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseClusterNetwork(%q) = nil error, want error", tt.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if n.Region != tt.wantRegion {
				t.Errorf("region = %q, want %q", n.Region, tt.wantRegion)
			}
			if str(n.Cidr) != tt.wantCidr {
				t.Errorf("cidr = %q, want %q", str(n.Cidr), tt.wantCidr)
			}
			if got := strSlice(n.PublicSubnets); !equalStrings(got, tt.wantPublic) {
				t.Errorf("public subnets = %v, want %v", got, tt.wantPublic)
			}
			if got := strSlice(n.PrivateSubnets); !equalStrings(got, tt.wantPrivate) {
				t.Errorf("private subnets = %v, want %v", got, tt.wantPrivate)
			}
			if got := strSlice(n.Subnets); !equalStrings(got, tt.wantSubnets) {
				t.Errorf("subnets = %v, want %v", got, tt.wantSubnets)
			}
			switch {
			case tt.wantExternal == nil && n.External != nil:
				t.Errorf("external = %v, want unset", *n.External)
			case tt.wantExternal != nil && n.External == nil:
				t.Errorf("external = unset, want %v", *tt.wantExternal)
			case tt.wantExternal != nil && *n.External != *tt.wantExternal:
				t.Errorf("external = %v, want %v",
					*n.External, *tt.wantExternal)
			}
			if str(n.ExternalId) != tt.wantExtID {
				t.Errorf("external-id = %q, want %q",
					str(n.ExternalId), tt.wantExtID)
			}
			if str(n.Name) != tt.wantName {
				t.Errorf("name = %q, want %q", str(n.Name), tt.wantName)
			}
		})
	}
}

// TestParseClusterNetworkCoversEveryAPIKey fails when the vendored
// ClusterNetworkSettings grows a field that --network cannot set.
//
// #134 was exactly this drift going unnoticed: the spec carried
// subnets, external, external_id and name for as long as the flag had
// existed, and because nothing compared the two, a GCP network stayed
// inexpressible until a user hit it. A re-vendor that adds a field now
// fails here instead.
//
// It reflects over the generated struct rather than listing the fields,
// so the test cannot drift the way the flag did.
func TestParseClusterNetworkCoversEveryAPIKey(t *testing.T) {
	// jsonName -> a --network fragment that sets it. The value is a
	// legal one for the field's type, so the parse below is a real
	// positive control rather than a lookup.
	fragmentFor := map[string]string{
		"region":          "region=us-east-1",
		"cidr":            "cidr=10.4.0.0/16",
		"public_subnets":  "public-subnets=10.4.1.0/24",
		"private_subnets": "private-subnets=10.4.128.0/24",
		"subnets":         "subnets=10.6.1.0/24",
		"external":        "external=true",
		"external_id":     "external-id=vpc-0abc",
		"name":            "name=shared-vpc",
	}

	typ := reflect.TypeOf(api.ClusterNetworkSettings{})
	for i := range typ.NumField() {
		field := typ.Field(i)
		jsonName, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if jsonName == "" || jsonName == "-" {
			continue
		}
		fragment, ok := fragmentFor[jsonName]
		if !ok {
			t.Errorf("ClusterNetworkSettings.%s (json %q) has no "+
				"--network key; add one to parseClusterNetwork and to "+
				"this map, or the field is unreachable from the CLI",
				field.Name, jsonName)
			continue
		}
		// The key must actually be ACCEPTED and must actually SET this
		// field. Checking acceptance alone would pass for a fragment
		// that parses into some other field entirely.
		n, err := parseClusterNetwork("region=us-east-1,"+fragment, "")
		if err != nil {
			t.Errorf("--network %q (field %s) is rejected: %v",
				fragment, field.Name, err)
			continue
		}
		if reflect.ValueOf(n).Field(i).IsZero() {
			t.Errorf("--network %q parsed, but left %s at its zero "+
				"value — the key does not reach the field",
				fragment, field.Name)
		}
	}
}

// TestStructuredFlagRejectionsAreUsageErrors pins every rejection from
// the three structured-flag parsers to ExitUsage (2).
//
// Before #134 these returned bare fmt.Errorf values, which surface as
// ExitGeneral (1) — the code a script reads as "the API or the network
// failed" — while validatePrivateSubnets, a sibling check on the same
// --network flag, already returned ExitUsage. Nothing was sent in
// either case; both are the user mistyping the command.
//
// The cases below are the parsers' rejection paths, not a sample: each
// arm that can return an error is represented, so removing an
// ExitUsage wrapping from any one of them fails here.
func TestStructuredFlagRejectionsAreUsageErrors(t *testing.T) {
	tests := []struct {
		name string
		call func() error
	}{
		{"network: not key=value", func() error {
			_, err := parseClusterNetwork("region=us-east-1,bogus", "")
			return err
		}},
		{"network: unknown key", func() error {
			_, err := parseClusterNetwork("region=us-east-1,nope=1", "")
			return err
		}},
		{"network: external not a boolean", func() error {
			_, err := parseClusterNetwork("region=us-east-1,external=x", "")
			return err
		}},
		{"network: region required", func() error {
			_, err := parseClusterNetwork("cidr=10.4.0.0/16", "")
			return err
		}},
		{"node: not key=value", func() error {
			_, err := parseClusterNode("region=us-east-1,bogus", "")
			return err
		}},
		{"node: unknown key", func() error {
			_, err := parseClusterNode("region=us-east-1,nope=1", "")
			return err
		}},
		{"node: volume-size not an integer", func() error {
			_, err := parseClusterNode("region=us-east-1,volume-size=big", "")
			return err
		}},
		{"node: volume-iops not an integer", func() error {
			_, err := parseClusterNode("region=us-east-1,volume-iops=lots", "")
			return err
		}},
		{"node: region required", func() error {
			_, err := parseClusterNode("name=n1", "")
			return err
		}},
		{"firewall-rule: not key=value", func() error {
			_, err := parseFirewallRule("name=https,port=443,bogus")
			return err
		}},
		{"firewall-rule: unknown key", func() error {
			_, err := parseFirewallRule("name=https,port=443,nope=1")
			return err
		}},
		{"firewall-rule: port not an integer", func() error {
			_, err := parseFirewallRule("name=https,port=web")
			return err
		}},
		{"firewall-rule: port required", func() error {
			_, err := parseFirewallRule("name=https")
			return err
		}},
		{"firewall-rule: name required", func() error {
			_, err := parseFirewallRule("port=443")
			return err
		}},
		{"firewall-rule: name not a valid rule type", func() error {
			_, err := parseFirewallRule("name=gopher,port=70")
			return err
		}},
		// Not a parser, but the --node flag's other rejection path: two
		// mutually exclusive ways to describe the same nodes. It shares
		// the flag, so it must share the code.
		{"nodes: --node and the shorthand together", func() error {
			_, err := buildCreateNodes(
				[]string{"name=n1,region=us-east-1"}, "r7g.medium", 30,
				true, []string{"us-east-1"})
			return err
		}},
		// The third rejection path on the same flag pair: a size that is
		// not a size. It used to be dropped, not refused (#256), so it
		// belongs on the same exit code as the two above.
		{"nodes: a non-positive --volume-size", func() error {
			_, err := buildCreateNodes(
				nil, "", 0, true, []string{"us-east-1"})
			return err
		}},
		// The same floor in the structured spelling. This one really is
		// a parser rejection, and it belongs beside the others because
		// the two spellings of volume-size must agree (#282).
		{"node: a non-positive volume-size", func() error {
			_, err := parseClusterNode("region=us-east-1,volume-size=-5",
				"")
			return err
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call()
			if err == nil {
				t.Fatal("want an error, got nil")
			}
			var ee *ExitError
			if !errors.As(err, &ee) {
				t.Fatalf("want *ExitError, got %T: %v", err, err)
			}
			if ee.Code() != ExitUsage {
				t.Errorf("exit code = %d, want ExitUsage (%d): %v",
					ee.Code(), ExitUsage, err)
			}
		})
	}
}

func TestParseClusterNode(t *testing.T) {
	str := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	num := func(p *int) int {
		if p == nil {
			return 0
		}
		return *p
	}

	tests := []struct {
		name          string
		in            string
		defaultRegion string
		wantErr       string
		wantName      string
		wantRegion    string
		wantInstance  string
		wantSize      int
		wantIops      int
		wantType      string
		wantAZ        string
	}{
		{
			name:         "spec payload node",
			in:           "name=n1,region=us-east-1,instance-type=r7g.medium,volume-size=30",
			wantName:     "n1",
			wantRegion:   "us-east-1",
			wantInstance: "r7g.medium",
			wantSize:     30,
		},
		{
			name: "all keys",
			in: "name=n1,region=us-east-1,instance-type=r7g.large," +
				"volume-size=100,volume-iops=3000,volume-type=gp2," +
				"availability-zone=us-east-1a",
			wantName:     "n1",
			wantRegion:   "us-east-1",
			wantInstance: "r7g.large",
			wantSize:     100,
			wantIops:     3000,
			wantType:     "gp2",
			wantAZ:       "us-east-1a",
		},
		{
			name:          "region defaults on single-region clusters",
			in:            "name=n1,instance-type=r7g.medium",
			defaultRegion: "us-east-1",
			wantName:      "n1",
			wantRegion:    "us-east-1",
			wantInstance:  "r7g.medium",
		},
		{
			name:       "gp3 accepted",
			in:         "name=n1,region=us-east-1,volume-type=gp3",
			wantName:   "n1",
			wantRegion: "us-east-1",
			wantType:   "gp3",
		},
		{
			name:    "region required on multi-region clusters",
			in:      "name=n1,instance-type=r7g.medium",
			wantErr: "region is required",
		},
		{
			name:    "non-int volume-size",
			in:      "name=n1,region=us-east-1,volume-size=big",
			wantErr: "not an integer",
		},
		{
			name:    "non-int volume-iops",
			in:      "name=n1,region=us-east-1,volume-iops=fast",
			wantErr: "not an integer",
		},
		{
			name:    "unknown key",
			in:      "name=n1,region=us-east-1,colour=red",
			wantErr: "unknown key",
		},
		{
			name:    "not key=value",
			in:      "name=n1,region=us-east-1,bogus",
			wantErr: "not key=value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, err := parseClusterNode(tt.in, tt.defaultRegion)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("parseClusterNode(%q) = nil error, want error containing %q",
						tt.in, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %q, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if str(n.Name) != tt.wantName {
				t.Errorf("name = %q, want %q", str(n.Name), tt.wantName)
			}
			if n.Region != tt.wantRegion {
				t.Errorf("region = %q, want %q", n.Region, tt.wantRegion)
			}
			if str(n.InstanceType) != tt.wantInstance {
				t.Errorf("instance type = %q, want %q", str(n.InstanceType), tt.wantInstance)
			}
			if num(n.VolumeSize) != tt.wantSize {
				t.Errorf("volume size = %d, want %d", num(n.VolumeSize), tt.wantSize)
			}
			if num(n.VolumeIops) != tt.wantIops {
				t.Errorf("volume iops = %d, want %d", num(n.VolumeIops), tt.wantIops)
			}
			if str(n.VolumeType) != tt.wantType {
				t.Errorf("volume type = %q, want %q", str(n.VolumeType), tt.wantType)
			}
			if str(n.AvailabilityZone) != tt.wantAZ {
				t.Errorf("availability zone = %q, want %q", str(n.AvailabilityZone), tt.wantAZ)
			}
		})
	}
}

func TestBuildCreateNodes(t *testing.T) {
	str := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}

	type nodeExp struct {
		name          string
		region        string
		instanceType  string
		volumeSize    int
		volumeSizeNil bool
		volumeTypeNil bool
	}
	tests := []struct {
		name          string
		nodeFlags     []string
		instanceType  string
		volumeSize    int
		volumeSizeSet bool
		regions       []string
		wantErr       bool
		wantNil       bool
		want          []nodeExp
	}{
		{
			name:    "nil without node flags or shorthand",
			regions: []string{"us-east-1"},
			wantNil: true,
		},
		{
			name:          "shorthand synthesizes one node per region",
			instanceType:  "r7g.medium",
			volumeSize:    30,
			volumeSizeSet: true,
			regions:       []string{"us-east-1", "eu-west-1"},
			want: []nodeExp{
				{
					name: "n1", region: "us-east-1",
					instanceType: "r7g.medium", volumeSize: 30,
					volumeTypeNil: true,
				},
				{
					name: "n2", region: "eu-west-1",
					instanceType: "r7g.medium", volumeSize: 30,
					volumeTypeNil: true,
				},
			},
		},
		{
			name:         "instance-type only shorthand",
			instanceType: "r7g.medium",
			regions:      []string{"us-east-1"},
			want: []nodeExp{
				{
					name: "n1", region: "us-east-1",
					instanceType: "r7g.medium", volumeSizeNil: true,
				},
			},
		},
		{
			name: "explicit --node values are parsed",
			nodeFlags: []string{
				"name=n1,instance-type=r7g.medium,volume-size=30",
			},
			regions: []string{"us-east-1"},
			want: []nodeExp{
				{
					name: "n1", region: "us-east-1",
					instanceType: "r7g.medium", volumeSize: 30,
				},
			},
		},
		{
			name:         "--node and shorthand conflict",
			nodeFlags:    []string{"name=n1"},
			instanceType: "r7g.medium",
			regions:      []string{"us-east-1"},
			wantErr:      true,
		},
		{
			name:      "parse errors propagate",
			nodeFlags: []string{"volume-size=big"},
			regions:   []string{"us-east-1"},
			wantErr:   true,
		},
		// The #256 boundary, pinned in both directions. 1 is the
		// smallest size that must still be ACCEPTED, so a later change
		// cannot quietly raise the floor; 0 and -5 must be refused
		// rather than dropped, which is what the old `volumeSize > 0`
		// guard did.
		{
			name:          "the minimum size is accepted",
			volumeSize:    volumeSizeMin,
			volumeSizeSet: true,
			regions:       []string{"us-east-1"},
			want: []nodeExp{
				{
					name: "n1", region: "us-east-1",
					volumeSize: volumeSizeMin, volumeTypeNil: true,
				},
			},
		},
		{
			name:          "an explicit zero is refused, not dropped",
			volumeSize:    0,
			volumeSizeSet: true,
			regions:       []string{"us-east-1"},
			wantErr:       true,
		},
		{
			name:          "a negative size is refused, not dropped",
			volumeSize:    -5,
			volumeSizeSet: true,
			regions:       []string{"us-east-1"},
			wantErr:       true,
		},
		{
			// The counterpart the refusals must not swallow: an
			// unset flag whose value happens to be 0 is not a
			// request for 0 GB, and must still synthesize nothing.
			name:    "an unset flag is not a zero",
			regions: []string{"us-east-1"},
			wantNil: true,
		},
		// The structured spelling of the same field carries the same
		// floor (#282). It is the spelling every worked example in
		// llms.txt uses, so it is the one an agent reaches for first.
		{
			name:      "a non-positive --node volume-size is refused",
			nodeFlags: []string{"name=n1,volume-size=-5"},
			regions:   []string{"us-east-1"},
			wantErr:   true,
		},
		{
			// Hardcoded 1, not volumeSizeMin, for the reason the
			// run-level twin gives: a case written against the constant
			// moves with it and would survive a raised floor.
			name:      "the minimum --node volume-size is accepted",
			nodeFlags: []string{"name=n1,volume-size=1"},
			regions:   []string{"us-east-1"},
			want: []nodeExp{
				{name: "n1", region: "us-east-1", volumeSize: 1},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, err := buildCreateNodes(tt.nodeFlags,
				tt.instanceType, tt.volumeSize, tt.volumeSizeSet,
				tt.regions)
			if tt.wantErr {
				if err == nil {
					t.Fatal("want error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantNil {
				if nodes != nil {
					t.Errorf("nodes = %v, want nil", nodes)
				}
				return
			}
			if len(nodes) != len(tt.want) {
				t.Fatalf("got %d nodes, want %d", len(nodes), len(tt.want))
			}
			for i, w := range tt.want {
				n := nodes[i]
				if str(n.Name) != w.name {
					t.Errorf("node[%d] name = %q, want %q",
						i, str(n.Name), w.name)
				}
				if n.Region != w.region {
					t.Errorf("node[%d] region = %q, want %q",
						i, n.Region, w.region)
				}
				if w.instanceType != "" &&
					str(n.InstanceType) != w.instanceType {
					t.Errorf("node[%d] instance type = %q, want %q",
						i, str(n.InstanceType), w.instanceType)
				}
				if w.volumeSizeNil {
					if n.VolumeSize != nil {
						t.Errorf("node[%d] volume size = %v, want nil",
							i, *n.VolumeSize)
					}
				} else if n.VolumeSize == nil ||
					*n.VolumeSize != w.volumeSize {
					t.Errorf("node[%d] volume size = %v, want %d",
						i, n.VolumeSize, w.volumeSize)
				}
				if w.volumeTypeNil && n.VolumeType != nil {
					t.Errorf("node[%d] volume type = %q, want unset "+
						"(shorthand leaves it to the server default)",
						i, str(n.VolumeType))
				}
			}
		})
	}
}

func TestBuildCreateNetworks(t *testing.T) {
	tests := []struct {
		name        string
		flags       []string
		regions     []string
		wantErr     bool
		wantNil     bool
		wantRegions []string
	}{
		{
			name:    "nil without network flags",
			regions: []string{"us-east-1"},
			wantNil: true,
		},
		{
			name: "multiple networks parse in order",
			flags: []string{
				"region=us-east-1,cidr=10.4.0.0/16",
				"region=eu-west-1,cidr=10.5.0.0/16",
			},
			regions:     []string{"us-east-1", "eu-west-1"},
			wantRegions: []string{"us-east-1", "eu-west-1"},
		},
		{
			name:    "parse errors propagate",
			flags:   []string{"cidr=10.4.0.0/16"},
			regions: []string{"us-east-1", "eu-west-1"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			networks, err := buildCreateNetworks(tt.flags, tt.regions)
			if tt.wantErr {
				if err == nil {
					t.Fatal("want error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantNil {
				if networks != nil {
					t.Errorf("networks = %v, want nil", networks)
				}
				return
			}
			if len(networks) != len(tt.wantRegions) {
				t.Fatalf("got %d networks, want %d",
					len(networks), len(tt.wantRegions))
			}
			for i, want := range tt.wantRegions {
				if networks[i].Region != want {
					t.Errorf("network[%d] region = %q, want %q",
						i, networks[i].Region, want)
				}
			}
		})
	}
}

// TestClusterCreateBodyMatchesServiceSpecs verifies that the CLI can
// reproduce, without raw curl, the reference cluster payloads from the
// pgEdge BYOC test-environment service specs (rulemaster and
// postgrest-cloud-test docs/service-specs.md §1).
func TestClusterCreateBodyMatchesServiceSpecs(t *testing.T) {
	tests := []struct {
		name          string
		clusterName   string
		nodeLocation  string
		firewallRules []string
		networks      []string
		nodes         []string
		wantJSON      string
	}{
		{
			name:         "public cluster (postgrest-pub)",
			clusterName:  "postgrest-pub",
			nodeLocation: "public",
			firewallRules: []string{
				"name=postgres,port=5432,sources=0.0.0.0/0",
				"name=https,port=443,sources=0.0.0.0/0",
			},
			networks: []string{
				"region=us-east-1,cidr=10.4.0.0/16," +
					"public-subnets=10.4.1.0/24",
			},
			nodes: []string{
				"name=n1,region=us-east-1," +
					"instance-type=r7g.medium,volume-size=30",
			},
			wantJSON: `{
				"name": "postgrest-pub",
				"cloud_account_id": "5be8beea-321f-418f-b33f-bea07c89d4ee",
				"backup_store_ids": ["dbd5e3d9-2364-4066-86d5-5b13cc8deaba"],
				"regions": ["us-east-1"],
				"node_location": "public",
				"nodes": [{
					"name": "n1",
					"region": "us-east-1",
					"instance_type": "r7g.medium",
					"volume_size": 30
				}],
				"networks": [{
					"region": "us-east-1",
					"cidr": "10.4.0.0/16",
					"public_subnets": ["10.4.1.0/24"]
				}],
				"firewall_rules": [
					{"name": "postgres", "port": 5432, "sources": ["0.0.0.0/0"]},
					{"name": "https", "port": 443, "sources": ["0.0.0.0/0"]}
				]
			}`,
		},
		{
			name:         "private cluster (postgrest-priv)",
			clusterName:  "postgrest-priv",
			nodeLocation: "private",
			firewallRules: []string{
				"name=postgres,port=5432,sources=0.0.0.0/0",
			},
			networks: []string{
				"region=us-east-1,cidr=10.3.0.0/16," +
					"public-subnets=10.3.1.0/24," +
					"private-subnets=10.3.128.0/24",
			},
			nodes: []string{
				"name=n1,region=us-east-1," +
					"instance-type=r7g.medium,volume-size=30",
			},
			wantJSON: `{
				"name": "postgrest-priv",
				"cloud_account_id": "5be8beea-321f-418f-b33f-bea07c89d4ee",
				"backup_store_ids": ["dbd5e3d9-2364-4066-86d5-5b13cc8deaba"],
				"regions": ["us-east-1"],
				"node_location": "private",
				"nodes": [{
					"name": "n1",
					"region": "us-east-1",
					"instance_type": "r7g.medium",
					"volume_size": 30
				}],
				"networks": [{
					"region": "us-east-1",
					"cidr": "10.3.0.0/16",
					"public_subnets": ["10.3.1.0/24"],
					"private_subnets": ["10.3.128.0/24"]
				}],
				"firewall_rules": [{
					"name": "postgres",
					"port": 5432,
					"sources": ["0.0.0.0/0"]
				}]
			}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &clusterCreateOpts{
				name:           tt.clusterName,
				cloudAccountID: "5be8beea-321f-418f-b33f-bea07c89d4ee",
				regions:        []string{"us-east-1"},
				nodeLocation:   tt.nodeLocation,
				backupStoreIDs: []string{
					"dbd5e3d9-2364-4066-86d5-5b13cc8deaba"},
				firewallRules: tt.firewallRules,
				networks:      tt.networks,
				nodes:         tt.nodes,
			}

			body, err := buildClusterCreateBody(&module.Runtime{}, opts)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			got, err := json.Marshal(body)
			if err != nil {
				t.Fatalf("marshal body: %v", err)
			}

			var gotAny, wantAny any
			if err := json.Unmarshal(got, &gotAny); err != nil {
				t.Fatalf("unmarshal got: %v", err)
			}
			if err := json.Unmarshal([]byte(tt.wantJSON), &wantAny); err != nil {
				t.Fatalf("unmarshal want: %v", err)
			}
			if !reflect.DeepEqual(gotAny, wantAny) {
				t.Errorf("payload mismatch\ngot:  %s\nwant: %s",
					got, tt.wantJSON)
			}
		})
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestValidatePrivateSubnets pins the client-side --node-location
// private / --network private-subnets dependency (CLI-24), documented
// in skills/pgedge-byoc/SKILL.md ("Private clusters on AWS/Azure: add
// private-subnets=... to --network.").
//
// The check only fires on a network entry that is unambiguously
// AWS/Azure-shaped (it carries public-subnets): a private GCP cluster
// has no --network entry at all, or one with neither public-subnets
// nor private-subnets (GCP uses --network's subnets key instead, and
// the server rejects private_subnets there outright), and both those
// shapes must pass through untouched.
func TestValidatePrivateSubnets(t *testing.T) {
	strs := func(ss ...string) *[]string { return &ss }
	cidr := "10.4.0.0/16"
	tests := []struct {
		name         string
		nodeLocation string
		networks     []api.ClusterNetworkSettings
		wantErr      bool
	}{
		{
			name:         "public without private-subnets is fine",
			nodeLocation: "public",
			networks: []api.ClusterNetworkSettings{
				{Region: "us-east-1", PublicSubnets: strs("10.4.1.0/24")},
			},
		},
		{
			name:         "private with private-subnets is fine",
			nodeLocation: "private",
			networks: []api.ClusterNetworkSettings{
				{
					Region:         "us-east-1",
					PublicSubnets:  strs("10.4.1.0/24"),
					PrivateSubnets: strs("10.4.128.0/24"),
				},
			},
		},
		{
			name:         "private, AWS-shaped without private-subnets rejected",
			nodeLocation: "private",
			networks: []api.ClusterNetworkSettings{
				{Region: "us-east-1", PublicSubnets: strs("10.4.1.0/24")},
			},
			wantErr: true,
		},
		{
			name:         "private, GCP-shaped: no networks passes through",
			nodeLocation: "private",
			networks:     nil,
		},
		{
			name:         "private, GCP-shaped entry passes through",
			nodeLocation: "private",
			networks: []api.ClusterNetworkSettings{
				// Neither public- nor private-subnets set: a GCP entry
				// uses the subnets key instead, so this must not be
				// flagged as missing private-subnets.
				{Region: "us-east-1", Cidr: &cidr},
			},
		},
		{
			// The real GCP shape, expressible from --network as of
			// #134. It carries subnets and no private-subnets, which is
			// exactly the combination this check must NOT reject: saas's
			// Google validator refuses private_subnets outright, so
			// demanding it here would make a private GCP cluster
			// impossible to create rather than merely awkward.
			name:         "private, GCP entry with subnets passes through",
			nodeLocation: "private",
			networks: []api.ClusterNetworkSettings{
				{
					Region:  "us-central1",
					Cidr:    &cidr,
					Subnets: strs("10.6.1.0/24"),
				},
			},
		},
		{
			name:         "private, empty private-subnets on a GCP-shaped entry passes through",
			nodeLocation: "private",
			networks: []api.ClusterNetworkSettings{
				{Region: "us-east-1", PrivateSubnets: &[]string{}},
			},
		},
		{
			name:         "private multi-region: one AWS-shaped region missing rejected",
			nodeLocation: "private",
			networks: []api.ClusterNetworkSettings{
				{Region: "us-east-1", PrivateSubnets: strs("10.4.128.0/24")},
				{Region: "eu-west-1", PublicSubnets: strs("10.5.1.0/24")},
			},
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validatePrivateSubnets(tc.nodeLocation, tc.networks)
			if tc.wantErr {
				if err == nil {
					t.Fatal("want error, got nil")
				}
				exitErr, ok := err.(*ExitError)
				if !ok || exitErr.Code() != ExitUsage {
					t.Fatalf("want ExitUsage, got %T %v", err, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestClusterCreateFirewallParsing(t *testing.T) {
	// Mirrors the loop in runClusterCreate.
	raw := []string{"name=https,port=443,sources=0.0.0.0/0"}
	var rules []api.ClusterFirewallRuleSettings
	for _, s := range raw {
		r, err := parseFirewallRule(s)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		rules = append(rules, r)
	}
	if len(rules) != 1 || rules[0].Port != 443 {
		t.Fatalf("rules = %v, want one rule on port 443", rules)
	}
}
