package pulumi

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/iac-studio/iac-studio/internal/parser"
)

// TSParser parses Pulumi TypeScript programs into the same Resource shape
// used by the Terraform canvas. It intentionally targets the generated
// constructor form:
//
//	const name = new aws.ec2.Vpc("logical-name", { ... });
//
// That keeps /resources and /sync dependency-free while still covering the
// layered-pulumi scaffold and canvas-generated programs.
type TSParser struct{}

func (p *TSParser) ParseDir(dir string) ([]parser.Resource, error) {
	entrypoint := filepath.Join(dir, "index.ts")
	info, err := os.Stat(entrypoint)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("pulumi typescript entrypoint not found: %s", entrypoint)
		}
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("pulumi typescript entrypoint is a directory: %s", entrypoint)
	}

	resources, parseErr := p.ParseFile(entrypoint)
	if parseErr != nil {
		return resources, fmt.Errorf("pulumi typescript parse errors:\n  %s: %w", entrypoint, parseErr)
	}
	return resources, nil
}

func (p *TSParser) ParseFile(path string) ([]parser.Resource, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseProgram(path, src)
}

// ParseProgram parses one Pulumi TypeScript source buffer.
func ParseProgram(path string, src []byte) ([]parser.Resource, error) {
	decls, err := parseResourceDeclarations(path, string(src))
	if err != nil {
		return nil, err
	}
	resources := make([]parser.Resource, 0, len(decls))
	for _, decl := range decls {
		resources = append(resources, decl.Resource)
	}
	return resources, nil
}

type resourceDecl struct {
	Resource parser.Resource
	Start    int
	End      int
}

var resourceDeclRE = regexp.MustCompile(`\b(?:const|let|var)\s+([A-Za-z_$][A-Za-z0-9_$]*)\s*=\s*new\s+`)

func parseResourceDeclarations(path, src string) ([]resourceDecl, error) {
	matches := resourceDeclRE.FindAllStringSubmatchIndex(src, -1)
	decls := make([]resourceDecl, 0, len(matches))
	var parseErrors []string

	for _, match := range matches {
		start := match[0]
		varName := src[match[2]:match[3]]
		ctorStart := match[1]
		callOpen := findConstructorCallOpen(src, ctorStart)
		if callOpen < 0 {
			continue
		}
		callClose := findMatchingDelimited(src, callOpen, '(', ')')
		if callClose < 0 {
			parseErrors = append(parseErrors, fmt.Sprintf("line %d: unterminated constructor call", lineAt(src, start)))
			continue
		}

		constructor := strings.TrimSpace(src[ctorStart:callOpen])
		tfType := pulumiToTerraform(constructor)
		if tfType == "" {
			continue
		}

		args := splitTopLevel(src[callOpen+1:callClose], ',')
		if len(args) < 2 {
			parseErrors = append(parseErrors, fmt.Sprintf("line %d: resource constructor missing args", lineAt(src, start)))
			continue
		}
		name, ok := parseTSStringLiteral(strings.TrimSpace(args[0]))
		if !ok || strings.TrimSpace(name) == "" {
			name = varName
		}
		props := map[string]interface{}{}
		if parsed, ok := parseTSObjectLiteral(strings.TrimSpace(args[1]), ""); ok {
			props = parsed
		}

		end := callClose + 1
		for end < len(src) && unicode.IsSpace(rune(src[end])) {
			end++
		}
		if end < len(src) && src[end] == ';' {
			end++
		}

		decls = append(decls, resourceDecl{
			Resource: parser.Resource{
				ID:         fmt.Sprintf("%s.%s", tfType, name),
				Type:       tfType,
				Name:       name,
				Properties: props,
				File:       path,
				Line:       lineAt(src, start),
				BlockType:  "resource",
			},
			Start: start,
			End:   end,
		})
	}

	if len(parseErrors) > 0 {
		return decls, fmt.Errorf("%s", strings.Join(parseErrors, "; "))
	}
	return decls, nil
}

func findConstructorCallOpen(src string, pos int) int {
	i := skipSpace(src, pos)
	if i < len(src) && src[i] == '(' {
		if close := findMatchingDelimited(src, i, '(', ')'); close >= 0 {
			j := skipSpace(src, close+1)
			if j < len(src) && src[j] == '.' {
				i = j + 1
			}
		}
	}
	for i < len(src) {
		if src[i] == '(' {
			return i
		}
		i++
	}
	return -1
}

func findMatchingDelimited(src string, open int, left, right byte) int {
	depth := 0
	for i := open; i < len(src); i++ {
		switch src[i] {
		case '\'', '"', '`':
			i = skipTSString(src, i)
		case '/':
			if next := skipTSComment(src, i); next != i {
				i = next
			}
		case left:
			depth++
		case right:
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func splitTopLevel(src string, sep byte) []string {
	var parts []string
	depthParen, depthBrace, depthBracket := 0, 0, 0
	start := 0
	for i := 0; i < len(src); i++ {
		switch src[i] {
		case '\'', '"', '`':
			i = skipTSString(src, i)
		case '/':
			if next := skipTSComment(src, i); next != i {
				i = next
			}
		case '(':
			depthParen++
		case ')':
			if depthParen > 0 {
				depthParen--
			}
		case '{':
			depthBrace++
		case '}':
			if depthBrace > 0 {
				depthBrace--
			}
		case '[':
			depthBracket++
		case ']':
			if depthBracket > 0 {
				depthBracket--
			}
		default:
			if src[i] == sep && depthParen == 0 && depthBrace == 0 && depthBracket == 0 {
				parts = append(parts, src[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, src[start:])
	return parts
}

func splitKeyValue(src string) (string, string, bool) {
	depthParen, depthBrace, depthBracket := 0, 0, 0
	for i := 0; i < len(src); i++ {
		switch src[i] {
		case '\'', '"', '`':
			i = skipTSString(src, i)
		case '/':
			if next := skipTSComment(src, i); next != i {
				i = next
			}
		case '(':
			depthParen++
		case ')':
			if depthParen > 0 {
				depthParen--
			}
		case '{':
			depthBrace++
		case '}':
			if depthBrace > 0 {
				depthBrace--
			}
		case '[':
			depthBracket++
		case ']':
			if depthBracket > 0 {
				depthBracket--
			}
		case ':':
			if depthParen == 0 && depthBrace == 0 && depthBracket == 0 {
				return src[:i], src[i+1:], true
			}
		}
	}
	return "", "", false
}

func parseTSObjectLiteral(src, parentKey string) (map[string]interface{}, bool) {
	src = strings.TrimSpace(src)
	if !strings.HasPrefix(src, "{") {
		return nil, false
	}
	close := findMatchingDelimited(src, 0, '{', '}')
	if close != len(src)-1 {
		return nil, false
	}
	out := map[string]interface{}{}
	for _, part := range splitTopLevel(src[1:close], ',') {
		part = strings.TrimSpace(part)
		if part == "" || strings.HasPrefix(part, "...") {
			continue
		}
		rawKey, rawVal, ok := splitKeyValue(part)
		if !ok {
			continue
		}
		key := strings.TrimSpace(rawKey)
		if unquoted, ok := parseTSStringLiteral(key); ok {
			key = unquoted
		}
		if !preservesLiteralKeys(parentKey) {
			key = camelToSnake(key)
		}
		out[key] = parseTSValue(rawVal, key)
	}
	return out, true
}

func parseTSValue(src, parentKey string) interface{} {
	src = strings.TrimSpace(src)
	if src == "" {
		return ""
	}
	if s, ok := parseTSStringLiteral(src); ok {
		return s
	}
	if obj, ok := parseTSObjectLiteral(src, parentKey); ok {
		return obj
	}
	if strings.HasPrefix(src, "[") {
		if close := findMatchingDelimited(src, 0, '[', ']'); close == len(src)-1 {
			items := []interface{}{}
			for _, part := range splitTopLevel(src[1:close], ',') {
				part = strings.TrimSpace(part)
				if part != "" {
					items = append(items, parseTSValue(part, parentKey))
				}
			}
			return items
		}
	}
	switch src {
	case "true":
		return true
	case "false":
		return false
	case "null", "undefined":
		return nil
	}
	if n, err := strconv.ParseInt(src, 10, 64); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(src, 64); err == nil {
		return f
	}
	// Preserve references such as vpc.id or environment as a string. The
	// generator can only safely round-trip literal JSON-compatible values today.
	return src
}

func parseTSStringLiteral(src string) (string, bool) {
	src = strings.TrimSpace(src)
	if len(src) < 2 {
		return "", false
	}
	quote := src[0]
	if src[len(src)-1] != quote {
		return "", false
	}
	switch quote {
	case '"':
		unquoted, err := strconv.Unquote(src)
		return unquoted, err == nil
	case '\'':
		return unescapeSingleQuotedString(src[1 : len(src)-1]), true
	case '`':
		inner := src[1 : len(src)-1]
		if strings.Contains(inner, "${") {
			return "", false
		}
		return inner, true
	default:
		return "", false
	}
}

func unescapeSingleQuotedString(inner string) string {
	var b strings.Builder
	escaped := false
	for _, r := range inner {
		if !escaped {
			if r == '\\' {
				escaped = true
				continue
			}
			b.WriteRune(r)
			continue
		}
		switch r {
		case '\\':
			b.WriteRune('\\')
		case '\'':
			b.WriteRune('\'')
		case 'n':
			b.WriteRune('\n')
		case 'r':
			b.WriteRune('\r')
		case 't':
			b.WriteRune('\t')
		case 'b':
			b.WriteRune('\b')
		case 'f':
			b.WriteRune('\f')
		case 'v':
			b.WriteRune('\v')
		case '0':
			b.WriteRune(0)
		default:
			b.WriteRune(r)
		}
		escaped = false
	}
	if escaped {
		b.WriteRune('\\')
	}
	return b.String()
}

func skipSpace(src string, pos int) int {
	for pos < len(src) && unicode.IsSpace(rune(src[pos])) {
		pos++
	}
	return pos
}

func skipTSString(src string, pos int) int {
	quote := src[pos]
	for i := pos + 1; i < len(src); i++ {
		if src[i] == '\\' {
			i++
			continue
		}
		if src[i] == quote {
			return i
		}
	}
	return len(src) - 1
}

func skipTSComment(src string, pos int) int {
	if pos+1 >= len(src) || src[pos] != '/' {
		return pos
	}
	switch src[pos+1] {
	case '/':
		for i := pos + 2; i < len(src); i++ {
			if src[i] == '\n' {
				return i
			}
		}
		return len(src) - 1
	case '*':
		if end := strings.Index(src[pos+2:], "*/"); end >= 0 {
			return pos + 2 + end + 1
		}
		return len(src) - 1
	default:
		return pos
	}
}

func lineAt(src string, pos int) int {
	if pos <= 0 {
		return 1
	}
	if pos > len(src) {
		pos = len(src)
	}
	return strings.Count(src[:pos], "\n") + 1
}

func pulumiToTerraform(constructor string) string {
	normalized := normalizePulumiConstructor(constructor)
	if tfType, ok := terraformTypeByPulumi()[normalized]; ok {
		return tfType
	}
	parts := strings.Split(normalized, ".")
	if len(parts) < 2 {
		return ""
	}
	leaf := camelToSnake(parts[len(parts)-1])
	switch parts[0] {
	case "aws":
		return "aws_" + leaf
	case "gcp":
		return "google_" + leaf
	case "azure":
		return "azurerm_" + leaf
	default:
		return ""
	}
}

func normalizePulumiConstructor(constructor string) string {
	normalized := strings.Join(strings.Fields(strings.TrimSpace(constructor)), "")
	normalized = strings.ReplaceAll(normalized, "(awsasany)", "aws")
	normalized = strings.ReplaceAll(normalized, "(gcpasany)", "gcp")
	normalized = strings.ReplaceAll(normalized, "(azureasany)", "azure")
	return normalized
}

var terraformTypeByPulumiCache = buildTerraformTypeByPulumi()

func buildTerraformTypeByPulumi() map[string]string {
	out := make(map[string]string, len(pulumiTypeOverrides))
	for tfType, pulumiType := range pulumiTypeOverrides {
		out[normalizePulumiConstructor(pulumiType)] = tfType
	}
	return out
}

func terraformTypeByPulumi() map[string]string {
	return terraformTypeByPulumiCache
}

func camelToSnake(s string) string {
	if s == "" {
		return s
	}
	var b strings.Builder
	var prev rune
	for i, r := range s {
		if r == '-' || r == ' ' {
			b.WriteRune('_')
			prev = '_'
			continue
		}
		if unicode.IsUpper(r) {
			if i > 0 && prev != '_' {
				b.WriteRune('_')
			}
			b.WriteRune(unicode.ToLower(r))
		} else {
			b.WriteRune(r)
		}
		prev = r
	}
	return b.String()
}
