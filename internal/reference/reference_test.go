package reference

import (
	"strings"
	"testing"
	"testing/fstest"
)

func fixture() fstest.MapFS {
	return fstest.MapFS{
		"llms.txt":                  {Data: []byte("# index\n")},
		"llms/cluster.txt":          {Data: []byte("# cluster\n")},
		"llms/database.txt":         {Data: []byte("# database\n")},
		"llms/database/mcp.txt":     {Data: []byte("# mcp\n")},
		"llms/database/service.txt": {Data: []byte("# service\n")},
	}
}

func TestFromFSDerivesScopesFromPaths(t *testing.T) {
	docs, err := FromFS(fixture(), "internal/x/byoc", "starfleet byoc")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"starfleet byoc",
		"starfleet byoc cluster",
		"starfleet byoc database",
		"starfleet byoc database mcp",
		"starfleet byoc database service",
	}
	got := Scopes(docs)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("scopes = %v, want %v", got, want)
	}
	mcp, ok := Find(docs, "starfleet byoc database mcp")
	if !ok || mcp.Path != "internal/x/byoc/llms/database/mcp.txt" ||
		string(mcp.Body) != "# mcp\n" {
		t.Errorf("mcp page = %+v", mcp)
	}
	if idx, _ := Find(docs, "starfleet byoc"); idx.Path !=
		"internal/x/byoc/llms.txt" {
		t.Errorf("index path = %q", idx.Path)
	}
}

func TestFromFSWithoutPagesDirIsJustTheIndex(t *testing.T) {
	docs, err := FromFS(fstest.MapFS{
		"llms.txt": {Data: []byte("# only\n")},
	}, "d", "m")
	if err != nil || len(docs) != 1 || docs[0].Scope != "m" {
		t.Fatalf("docs = %+v, err = %v", docs, err)
	}
}

func TestFromFSRejectsAStrayFile(t *testing.T) {
	fsys := fixture()
	fsys["llms/notes.md"] = &fstest.MapFile{Data: []byte("x")}
	if _, err := FromFS(fsys, "d", "m"); err == nil {
		t.Fatal("a non-.txt file under llms/ must be an error, not a " +
			"silently unserved page")
	}
}

func TestFromFSMissingIndexIsAnError(t *testing.T) {
	if _, err := FromFS(fstest.MapFS{}, "d", "m"); err == nil {
		t.Fatal("no llms.txt must be an error")
	}
}

func TestChildrenAndLongestPrefix(t *testing.T) {
	docs, _ := FromFS(fixture(), "d", "starfleet byoc")

	kids := Scopes(Children(docs, "starfleet byoc"))
	if strings.Join(kids, "|") !=
		"starfleet byoc cluster|starfleet byoc database" {
		t.Errorf("index children = %v", kids)
	}
	kids = Scopes(Children(docs, "starfleet byoc database"))
	if strings.Join(kids, "|") !=
		"starfleet byoc database mcp|starfleet byoc database service" {
		t.Errorf("database children = %v", kids)
	}
	if got := Children(docs, "starfleet byoc cluster"); len(got) != 0 {
		t.Errorf("leaf has children: %v", Scopes(got))
	}

	best, ok := LongestPrefix(docs, "starfleet byoc database nope")
	if !ok || best.Scope != "starfleet byoc database" {
		t.Errorf("longest prefix = %+v, %v", best.Scope, ok)
	}
	// A word-wise prefix: "starfleet byoc databases" is not under
	// "starfleet byoc database".
	best, _ = LongestPrefix(docs, "starfleet byoc databases")
	if best.Scope != "starfleet byoc" {
		t.Errorf("longest prefix of a near-miss = %q", best.Scope)
	}
	if _, ok := LongestPrefix(docs, "other"); ok {
		t.Error("an unrelated scope has no prefix page")
	}
}

func TestTopicSummary(t *testing.T) {
	cases := []struct {
		name, body, want string
		ok               bool
	}{
		{"topic", "<!-- topic: Load a schema -->\n# x\n", "Load a schema", true},
		{"resource page", "# pgedge starfleet managed backup\n", "", false},
		{"marker not first", "# x\n<!-- topic: Load -->\n", "", false},
		{"empty summary", "<!-- topic:  -->\n", "", false},
		{"unclosed", "<!-- topic: Load\n", "", false},
		{"no trailing newline", "<!-- topic: Load -->", "Load", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := TopicSummary([]byte(c.body))
			if got != c.want || ok != c.ok {
				t.Errorf("TopicSummary(%q) = %q, %v; want %q, %v",
					c.body, got, ok, c.want, c.ok)
			}
		})
	}
}

func TestFromFSAtTheRootScope(t *testing.T) {
	docs, err := FromFS(fixture(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(Scopes(docs), "|")
	if got != "|cluster|database|database mcp|database service" {
		t.Fatalf("scopes = %q", got)
	}
	if d, _ := Find(docs, "cluster"); d.Path != "llms/cluster.txt" {
		t.Errorf("cluster path = %q", d.Path)
	}
	kids := Scopes(Children(docs, ""))
	if strings.Join(kids, "|") != "cluster|database" {
		t.Errorf("root children = %v", kids)
	}
}
