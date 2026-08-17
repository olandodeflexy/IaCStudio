package mcpairlock

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/mod/semver"
)

const maxVersionProbeBytes = 4096

var (
	errVersionNotFound  = errors.New("semantic version not found in probe output")
	errVersionAmbiguous = errors.New("multiple semantic versions found in probe output")
	strictSemVer        = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)
	semVerCandidate     = regexp.MustCompile(`v?[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?`)
)

type versionPolicy struct {
	operator string
	required string
}

type versionPolicyEvaluation struct {
	Observed  string
	Required  string
	Operator  string
	Satisfied bool
}

func parseVersionConstraint(value string) (versionPolicy, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return versionPolicy{}, errors.New("version constraint is required")
	}

	operator := "="
	switch {
	case strings.HasPrefix(value, ">="):
		operator = ">="
		value = strings.TrimSpace(strings.TrimPrefix(value, ">="))
	case strings.HasPrefix(value, "="):
		value = strings.TrimSpace(strings.TrimPrefix(value, "="))
	case strings.ContainsAny(value[:1], "<>!~^"):
		return versionPolicy{}, errors.New("version constraint must be an exact version or use >=")
	}

	version, err := normalizeSemanticVersion(value)
	if err != nil {
		return versionPolicy{}, fmt.Errorf("invalid required version: %w", err)
	}
	return versionPolicy{operator: operator, required: version}, nil
}

func evaluateVersionConstraint(output, constraint string) (versionPolicyEvaluation, error) {
	policy, err := parseVersionConstraint(constraint)
	if err != nil {
		return versionPolicyEvaluation{}, err
	}
	observed, err := extractSemanticVersion(output)
	if err != nil {
		return versionPolicyEvaluation{}, err
	}

	comparison := semver.Compare("v"+observed, "v"+policy.required)
	satisfied := comparison == 0
	if policy.operator == "=" && strings.Contains(policy.required, "+") {
		satisfied = observed == policy.required
	} else if policy.operator == ">=" {
		satisfied = comparison >= 0
	}
	return versionPolicyEvaluation{
		Observed:  observed,
		Required:  policy.required,
		Operator:  policy.operator,
		Satisfied: satisfied,
	}, nil
}

func extractSemanticVersion(output string) (string, error) {
	if len(output) > maxVersionProbeBytes {
		output = output[:maxVersionProbeBytes]
	}
	observed := ""
	for _, bounds := range semVerCandidate.FindAllStringIndex(output, -1) {
		if !hasSemanticVersionBoundaries(output, bounds[0], bounds[1]) {
			continue
		}
		version, err := normalizeSemanticVersion(output[bounds[0]:bounds[1]])
		if err != nil {
			continue
		}
		if observed == "" {
			observed = version
			continue
		}
		if version != observed {
			return "", errVersionAmbiguous
		}
	}
	if observed != "" {
		return observed, nil
	}
	return "", errVersionNotFound
}

func normalizeSemanticVersion(value string) (string, error) {
	value = strings.TrimSpace(value)
	if !strictSemVer.MatchString(value) {
		return "", errors.New("version must use semantic version format MAJOR.MINOR.PATCH")
	}
	value = strings.TrimPrefix(value, "v")
	if !semver.IsValid("v" + value) {
		return "", errors.New("version is not valid semantic versioning")
	}
	return value, nil
}

func hasSemanticVersionBoundaries(output string, start, end int) bool {
	if start > 0 && isSemanticVersionByte(output[start-1]) {
		return false
	}
	return end >= len(output) || !isSemanticVersionByte(output[end])
}

func isSemanticVersionByte(value byte) bool {
	return isASCIIAlpha(value) || (value >= '0' && value <= '9') || strings.ContainsRune("._-+", rune(value))
}
