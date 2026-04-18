package opa

import (
	"encoding/json"
	"fmt"
	"regexp"
)

// jsonUnmarshal is a tiny indirection so the main file doesn't need to
// import encoding/json directly — keeps the public surface small and lets
// us swap in a streaming decoder later if plan JSON sizes ever bite.
func jsonUnmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

// packageRE matches the first `package <name>` line in a Rego module.
// Rego packages can contain dots ("terraform.tags") so the capture allows
// the dot character.
var packageRE = regexp.MustCompile(`(?m)^\s*package\s+([A-Za-z_][A-Za-z0-9_.]*)`)

// readPackageName extracts the Rego package declaration from source bytes.
// Rego files MUST declare a package — if the regex misses, the file is
// malformed and we return a clear error rather than silently substituting
// "main".
func readPackageName(src []byte) (string, error) {
	m := packageRE.FindSubmatch(src)
	if len(m) < 2 {
		return "", fmt.Errorf("no `package` declaration found in Rego source")
	}
	return string(m[1]), nil
}
