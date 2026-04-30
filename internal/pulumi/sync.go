package pulumi

import (
	"regexp"
	"strings"
)

const (
	managedStartMarker = "// IaC Studio resources start"
	managedEndMarker   = "// IaC Studio resources end"
)

// RenderProgram exposes the TypeScript program renderer for sync code and
// tests. GenerateProject still owns the full Pulumi project layout.
func RenderProgram(cfg ProjectConfig) string {
	return renderProgram(cfg)
}

// SyncProgram rewrites only the generated resource section of an existing
// Pulumi program when possible. Imports may be augmented when the new graph
// introduces a provider SDK that was not already imported.
func SyncProgram(existing string, cfg ProjectConfig) (string, error) {
	generated := RenderProgram(cfg)
	section, err := managedSectionFromGenerated(generated)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(existing) == "" {
		prefix := generatedPrefix(generated)
		return strings.TrimRight(prefix, "\n") + "\n\n" + section, nil
	}

	if start := strings.Index(existing, managedStartMarker); start >= 0 {
		if endRel := strings.Index(existing[start:], managedEndMarker); endRel >= 0 {
			end := start + endRel + len(managedEndMarker)
			if strings.HasPrefix(strings.TrimLeft(existing[end:], "\n\r\t "), "// Exports") {
				end = len(existing)
			}
			prefix := mergeMissingImports(existing[:start], generated)
			return strings.TrimRight(prefix, "\n") + "\n" + section + existing[end:], nil
		}
	}

	decls, err := parseResourceDeclarations("index.ts", existing)
	if err != nil {
		return "", err
	}
	if len(decls) == 0 {
		prefix := mergeMissingImports(existing, generated)
		return strings.TrimRight(prefix, "\n") + "\n\n" + section, nil
	}

	start := decls[0].Start
	end := decls[len(decls)-1].End
	end = generatedExportsEnd(existing, end)

	prefix := mergeMissingImports(existing[:start], generated)
	return strings.TrimRight(prefix, "\n") + "\n\n" + section + existing[end:], nil
}

func generatedExportsEnd(src string, bodyEnd int) int {
	tail := src[bodyEnd:]
	if exportIdx := strings.Index(tail, "\n// Exports"); exportIdx >= 0 {
		return scanGeneratedExportsEnd(src, bodyEnd+exportIdx)
	}
	trimmedStart := bodyEnd + len(tail) - len(strings.TrimLeft(tail, "\n\r\t "))
	if strings.HasPrefix(src[trimmedStart:], "export const ") {
		return scanGeneratedExportsEnd(src, trimmedStart)
	}
	return bodyEnd
}

func scanGeneratedExportsEnd(src string, start int) int {
	i := start
	seenExports := false
	seenMarker := false
	for i < len(src) {
		lineStart := i
		lineEnd := strings.IndexByte(src[i:], '\n')
		if lineEnd < 0 {
			lineEnd = len(src)
			i = len(src)
		} else {
			lineEnd = i + lineEnd
			i = lineEnd + 1
		}
		trimmed := strings.TrimSpace(src[lineStart:lineEnd])
		switch {
		case trimmed == "":
			if seenExports {
				return lineStart
			}
			continue
		case strings.HasPrefix(trimmed, "// Exports"):
			seenMarker = true
			continue
		case strings.HasPrefix(trimmed, "export const "):
			seenExports = true
			continue
		case seenMarker || seenExports:
			return lineStart
		default:
			return start
		}
	}
	return len(src)
}

func managedSectionFromGenerated(generated string) (string, error) {
	decls, err := parseResourceDeclarations("generated-index.ts", generated)
	if err != nil {
		return "", err
	}
	if len(decls) == 0 {
		exports := generatedExports(generated)
		return managedStartMarker + "\n" + managedEndMarker + "\n\n" + exports, nil
	}
	bodyStart := decls[0].Start
	bodyEnd := decls[len(decls)-1].End
	resources := strings.TrimRight(generated[bodyStart:bodyEnd], "\n")
	exports := strings.TrimLeft(generated[bodyEnd:], "\n")
	return managedStartMarker + "\n" + resources + "\n" + managedEndMarker + "\n\n" + exports, nil
}

func generatedPrefix(generated string) string {
	decls, err := parseResourceDeclarations("generated-index.ts", generated)
	if err != nil || len(decls) == 0 {
		if idx := strings.Index(generated, "// Exports"); idx >= 0 {
			return generated[:idx]
		}
		return generated
	}
	return generated[:decls[0].Start]
}

func generatedExports(generated string) string {
	if idx := strings.Index(generated, "// Exports"); idx >= 0 {
		return generated[idx:]
	}
	return "// Exports — consumed by stack references + CI assertions.\n"
}

func mergeMissingImports(prefix, generated string) string {
	imports := importLines(generated)
	if len(imports) == 0 {
		return prefix
	}
	existing := importKeys(prefix)
	var missing []string
	for _, imp := range imports {
		key := importKey(imp)
		if key == "" || !existing[key] {
			missing = append(missing, imp)
		}
	}
	if len(missing) == 0 {
		return prefix
	}

	lines := strings.Split(prefix, "\n")
	insertAt := -1
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "import ") {
			insertAt = i + 1
		}
	}
	if insertAt < 0 {
		return strings.Join(missing, "\n") + "\n" + prefix
	}

	next := make([]string, 0, len(lines)+len(missing))
	next = append(next, lines[:insertAt]...)
	next = append(next, missing...)
	next = append(next, lines[insertAt:]...)
	return strings.Join(next, "\n")
}

var importLineRE = regexp.MustCompile(`^\s*import\s+(.+?)\s+from\s+['"]([^'"]+)['"]\s*;?\s*$`)

func importKeys(src string) map[string]bool {
	keys := make(map[string]bool)
	for _, line := range strings.Split(src, "\n") {
		if key := importKey(line); key != "" {
			keys[key] = true
		}
	}
	return keys
}

func importKey(line string) string {
	match := importLineRE.FindStringSubmatch(line)
	if len(match) != 3 {
		return ""
	}
	binding := strings.Join(strings.Fields(match[1]), " ")
	return binding + "|" + match[2]
}

func importLines(src string) []string {
	var imports []string
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			if len(imports) > 0 {
				break
			}
			continue
		}
		if !strings.HasPrefix(trimmed, "import ") {
			if len(imports) > 0 {
				break
			}
			continue
		}
		imports = append(imports, trimmed)
	}
	return imports
}
