package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/iac-studio/iac-studio/internal/parser"
	"github.com/iac-studio/iac-studio/internal/refactor"
	"github.com/iac-studio/iac-studio/internal/registry"
)

// projectModulesResponse is the payload for GET /api/projects/{name}/modules.
// Each ModuleView bundles the parsed Module block with whatever interface
// metadata is available — local modules get their declared variables and
// outputs introspected from disk; registry modules carry their coordinates
// so the frontend can follow up with /api/registry/modules/... to fetch
// registry metadata asynchronously.
type projectModulesResponse struct {
	Modules []moduleView `json:"modules"`
}

type moduleView struct {
	parser.Module
	// SourceKind tells the UI how to render the module — "local" gets a
	// double-click-to-descend affordance; "registry" gets an info bubble;
	// "other" covers git/https/s3 sources we don't deeply introspect.
	SourceKind string `json:"source_kind"`

	// Interface is populated for SourceKind="local" with the module's
	// declared variables + outputs. nil for remote modules (the frontend
	// fetches their interface from the registry endpoint separately).
	Interface *parser.ModuleInterface `json:"interface,omitempty"`

	// RegistryCoords is populated for SourceKind="registry" so the UI can
	// fetch registry metadata without re-parsing the source string.
	RegistryCoords *registryCoords `json:"registry,omitempty"`
}

type registryCoords struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Provider  string `json:"provider"`
}

// classifyModuleSource decides how to treat a module's source string.
// Terraform's registry address form is "<ns>/<name>/<provider>"
// (optionally "//<submodule>"), which we detect by slash count + shape.
// Anything starting with "./", "../", or "/" is local. Everything else
// (git::, https://, s3::, hashicorp/…/aws on a private registry with
// hostname prefix) is "other" — we surface the source string but don't
// auto-introspect.
func classifyModuleSource(src string) (kind string, coords *registryCoords) {
	if src == "" {
		return "unknown", nil
	}
	if strings.HasPrefix(src, "./") || strings.HasPrefix(src, "../") || strings.HasPrefix(src, "/") {
		return "local", nil
	}
	// Registry shorthand: exactly two slashes (ns/name/provider) and
	// no scheme-like prefix. Ignore submodule suffix ("//submodule").
	core := src
	if i := strings.Index(src, "//"); i >= 0 {
		core = src[:i]
	}
	parts := strings.Split(core, "/")
	if len(parts) == 3 && !strings.Contains(parts[0], ":") && !strings.Contains(parts[0], ".") {
		return "registry", &registryCoords{
			Namespace: parts[0],
			Name:      parts[1],
			Provider:  parts[2],
		}
	}
	return "other", nil
}

// resolveLocalModulePath turns a "./modules/networking" style source into
// an absolute path rooted at the project. Relative paths escape up with
// "../" — filepath.Clean collapses that, and we refuse any result that
// walks outside the project root so a crafted source string can't trigger
// a traversal read.
func resolveLocalModulePath(projectPath, source string) (string, bool) {
	abs := filepath.Clean(filepath.Join(projectPath, source))
	rel, err := filepath.Rel(projectPath, abs)
	if err != nil {
		return "", false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return abs, true
}

// registerModuleRoutes wires the module-discovery endpoints. Split out into
// its own file for discoverability; called from NewRouter.
func registerModuleRoutes(mux *http.ServeMux, projectsDir string, reg *registry.Client) {
	// GET /api/projects/{name}/modules
	// Returns every module block in the project together with each one's
	// introspected interface (local) or registry coordinates (registry).
	mux.HandleFunc("GET /api/projects/{name}/modules", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		projectPath, err := safeProjectPath(projectsDir, name)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}

		// Walk every .tf file in the project root (ParseDir already does
		// this and recurses) and collect the Modules slice from each.
		p := &parser.HCLParser{}
		entries, err := filepath.Glob(filepath.Join(projectPath, "**/*.tf"))
		// Glob doesn't support recursive "**" on all Go versions; fall
		// back to a walk that mirrors HCLParser.ParseDir.
		if err != nil || len(entries) == 0 {
			entries, _ = listTFFiles(projectPath)
		}

		var views []moduleView
		for _, file := range entries {
			result, err := p.ParseFileFull(file)
			if err != nil || result == nil {
				continue
			}
			for _, mod := range result.Modules {
				view := moduleView{Module: mod}
				kind, coords := classifyModuleSource(mod.Source)
				view.SourceKind = kind
				view.RegistryCoords = coords

				if kind == "local" {
					if abs, ok := resolveLocalModulePath(projectPath, mod.Source); ok {
						if iface, err := parser.InspectLocalModule(abs); err == nil {
							view.Interface = iface
						}
					}
				}
				views = append(views, view)
			}
		}
		_ = json.NewEncoder(w).Encode(projectModulesResponse{Modules: views})
	})

	// GET /api/registry/modules/search?q=...&limit=...
	// Proxies to the registry's search so the frontend doesn't need to
	// embed any network credentials or CORS exceptions.
	mux.HandleFunc("GET /api/registry/modules/search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		limit := 20
		if n := r.URL.Query().Get("limit"); n != "" {
			// Don't validate beyond "positive int" — registry errors
			// surface through anyway, and users get what they ask for.
			var parsed int
			_, _ = fmt.Sscanf(n, "%d", &parsed)
			if parsed > 0 {
				limit = parsed
			}
		}
		result, err := reg.Search(r.Context(), q, limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		_ = json.NewEncoder(w).Encode(result)
	})

	// POST /api/projects/{name}/promote-to-module
	// Extract the selected resources into a new sibling module.
	// Body: {"module_name": "networking", "resource_ids": ["aws_vpc.main", ...]}
	mux.HandleFunc("POST /api/projects/{name}/promote-to-module", func(w http.ResponseWriter, r *http.Request) {
		limitBody(w, r)
		name := r.PathValue("name")
		projectPath, err := safeProjectPath(projectsDir, name)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		var req struct {
			ModuleName  string   `json:"module_name"`
			ResourceIDs []string `json:"resource_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body: "+err.Error(), 400)
			return
		}
		result, err := refactor.PromoteToModule(refactor.PromoteRequest{
			ProjectDir:  projectPath,
			ModuleName:  req.ModuleName,
			ResourceIDs: req.ResourceIDs,
		})
		if err != nil {
			// Validation failures (bad name, missing resources, existing
			// target dir) are 400. I/O failures inside the refactor fall
			// through here too; we don't bother distinguishing until the
			// UI asks for finer-grained codes.
			http.Error(w, err.Error(), 400)
			return
		}
		_ = json.NewEncoder(w).Encode(result)
	})

	// GET /api/registry/modules/{namespace}/{name}/{provider}
	// Proxies to the registry's module-detail endpoint so the UI can
	// show version history / declared inputs / downloads without going
	// across origins.
	mux.HandleFunc("GET /api/registry/modules/{namespace}/{name}/{provider}", func(w http.ResponseWriter, r *http.Request) {
		ns := r.PathValue("namespace")
		name := r.PathValue("name")
		prov := r.PathValue("provider")
		mod, err := reg.Get(r.Context(), ns, name, prov)
		if err != nil {
			// A 404 from the upstream registry is a client-facing 404.
			// Other failures (timeout, DNS, etc.) are bad-gateway.
			status := http.StatusBadGateway
			if strings.Contains(err.Error(), "404") {
				status = http.StatusNotFound
			}
			http.Error(w, err.Error(), status)
			return
		}
		_ = json.NewEncoder(w).Encode(mod)
	})
}

// listTFFiles walks projectPath recursively and returns every .tf file,
// skipping the modules/ subtree so a local module's own .tf files don't
// shadow the root project's module list. Mirrors HCLParser.ParseDir's
// walk strategy so the module list matches what ParseDir produces.
func listTFFiles(projectPath string) ([]string, error) {
	var out []string
	err := filepath.Walk(projectPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			// Skip modules/ subdirectories: the module's own .tf files
			// belong to that module's InspectLocalModule call, not to
			// the root project's module list.
			if info.Name() == "modules" && path != projectPath {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".tf") {
			out = append(out, path)
		}
		return nil
	})
	return out, err
}
