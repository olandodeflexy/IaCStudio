package registry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubServer wires one handler per expected path and returns a Client
// pointing at the httptest server. Keeps each test self-contained.
func stubServer(t *testing.T, handlers map[string]http.HandlerFunc) *Client {
	t.Helper()
	mux := http.NewServeMux()
	for path, handler := range handlers {
		mux.HandleFunc(path, handler)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return New(Config{BaseURL: srv.URL})
}

// TestGetDecodesModuleMetadata — the golden path: Registry returns a full
// module payload, the client decodes it into a structured Module including
// the Root.Inputs / Root.Outputs surface the UI needs.
func TestGetDecodesModuleMetadata(t *testing.T) {
	c := stubServer(t, map[string]http.HandlerFunc{
		"/modules/terraform-aws-modules/vpc/aws": func(w http.ResponseWriter, r *http.Request) {
			if ua := r.Header.Get("User-Agent"); ua == "" {
				t.Errorf("User-Agent should be set, got %q", ua)
			}
			_, _ = w.Write([]byte(`{
                "id":"terraform-aws-modules/vpc/aws/5.1.0",
                "namespace":"terraform-aws-modules",
                "name":"vpc",
                "provider":"aws",
                "version":"5.1.0",
                "description":"VPC Terraform module",
                "source":"https://github.com/terraform-aws-modules/terraform-aws-vpc",
                "verified":true,
                "downloads":12345678,
                "root":{
                    "inputs":[
                        {"name":"cidr","type":"string","default":"\"10.0.0.0/16\"","required":false},
                        {"name":"name","type":"string","description":"Name of the VPC","required":true}
                    ],
                    "outputs":[
                        {"name":"vpc_id","description":"The VPC ID"}
                    ]
                }
            }`))
		},
	})

	got, err := c.Get(context.Background(), "terraform-aws-modules", "vpc", "aws")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Version != "5.1.0" || !got.Verified || got.Downloads != 12345678 {
		t.Errorf("top-level metadata wrong: %+v", got)
	}
	if len(got.Root.Inputs) != 2 || got.Root.Inputs[1].Required != true {
		t.Errorf("inputs decode wrong: %+v", got.Root.Inputs)
	}
	if len(got.Root.Outputs) != 1 || got.Root.Outputs[0].Name != "vpc_id" {
		t.Errorf("outputs decode wrong: %+v", got.Root.Outputs)
	}
}

// TestGetRequiresAllCoordinates — namespace/name/provider are mandatory;
// missing any should surface a clear error rather than making a malformed
// request to the registry.
func TestGetRequiresAllCoordinates(t *testing.T) {
	c := New(Config{BaseURL: "http://unused"})
	cases := []struct{ ns, name, prov string }{
		{"", "vpc", "aws"},
		{"terraform-aws-modules", "", "aws"},
		{"terraform-aws-modules", "vpc", ""},
	}
	for _, tc := range cases {
		if _, err := c.Get(context.Background(), tc.ns, tc.name, tc.prov); err == nil {
			t.Errorf("expected error for coordinates (%q, %q, %q)", tc.ns, tc.name, tc.prov)
		}
	}
}

// TestVersionsPreservesRegistryOrder — the registry returns versions
// newest-first; the client surfaces that ordering unchanged so the UI's
// "pin to version" list matches what terraform registry users already
// expect to see.
func TestVersionsPreservesRegistryOrder(t *testing.T) {
	c := stubServer(t, map[string]http.HandlerFunc{
		"/modules/terraform-aws-modules/vpc/aws/versions": func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{
                "modules":[{
                    "versions":[
                        {"version":"5.1.0"},
                        {"version":"5.0.0"},
                        {"version":"4.0.1"}
                    ]
                }]
            }`))
		},
	})

	got, err := c.Versions(context.Background(), "terraform-aws-modules", "vpc", "aws")
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	want := []string{"5.1.0", "5.0.0", "4.0.1"}
	if len(got) != len(want) {
		t.Fatalf("len=%d want %d", len(got), len(want))
	}
	for i, v := range want {
		if got[i] != v {
			t.Errorf("Versions[%d] = %q, want %q", i, got[i], v)
		}
	}
}

// TestSearchSendsQueryAndLimit covers the search parameters end-to-end.
func TestSearchSendsQueryAndLimit(t *testing.T) {
	var seenQuery, seenLimit string
	c := stubServer(t, map[string]http.HandlerFunc{
		"/modules/search": func(w http.ResponseWriter, r *http.Request) {
			seenQuery = r.URL.Query().Get("q")
			seenLimit = r.URL.Query().Get("limit")
			_, _ = w.Write([]byte(`{"modules":[{"name":"vpc","namespace":"terraform-aws-modules","provider":"aws"}]}`))
		},
	})
	got, err := c.Search(context.Background(), "vpc", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if seenQuery != "vpc" || seenLimit != "10" {
		t.Errorf("query params wrong: q=%q limit=%q", seenQuery, seenLimit)
	}
	if len(got.Modules) != 1 || got.Modules[0].Name != "vpc" {
		t.Errorf("search decode wrong: %+v", got)
	}
}

// TestSearchEmptyQueryIsQuiet — a blank query shouldn't hit the network;
// returns an empty result so the UI's "type to search" flow doesn't spam
// the registry with every keystroke.
func TestSearchEmptyQueryIsQuiet(t *testing.T) {
	// No server — if Search hits the network, the test fails with a
	// connection error rather than passing.
	c := New(Config{BaseURL: "http://127.0.0.1:1"})
	got, err := c.Search(context.Background(), "   ", 10)
	if err != nil {
		t.Fatalf("empty query shouldn't network: %v", err)
	}
	if len(got.Modules) != 0 {
		t.Errorf("blank query should yield empty result, got %+v", got.Modules)
	}
}

// TestNon2xxSurfacesRegistryMessage — a 404 from the registry should carry
// through as an error with enough context for the caller to log it.
func TestNon2xxSurfacesRegistryMessage(t *testing.T) {
	c := stubServer(t, map[string]http.HandlerFunc{
		"/modules/nobody/wrong/aws": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"errors":["module not found"]}`))
		},
	})
	_, err := c.Get(context.Background(), "nobody", "wrong", "aws")
	if err == nil {
		t.Fatal("expected an error on 404")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("error should include status code, got %v", err)
	}
}

// TestDefaultsFillEmptyConfig — New with a zero Config should still work.
func TestDefaultsFillEmptyConfig(t *testing.T) {
	c := New(Config{})
	if c.baseURL != DefaultBaseURL {
		t.Errorf("baseURL default wrong: %q", c.baseURL)
	}
	if c.httpClient == nil {
		t.Error("httpClient default missing")
	}
	if c.userAgent == "" {
		t.Error("userAgent default missing")
	}
}
