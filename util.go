package main

import (
	"encoding/json"
	"os"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

func jsonUnmarshal(data []byte, destination any) error { return json.Unmarshal(data, destination) }

func getenv(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func normalizeName(value string) string {
	value = norm.NFKC.String(strings.ToLower(value))
	var result strings.Builder
	for _, r := range value {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			continue
		}
		result.WriteRune(r)
	}
	return result.String()
}

func nameSimilarity(left, right string) float64 {
	a, b := normalizeName(left), normalizeName(right)
	if a == "" || b == "" {
		return 0
	}
	if a == b || strings.Contains(a, b) || strings.Contains(b, a) {
		return 1
	}
	rightSet := make(map[rune]struct{})
	for _, r := range b {
		rightSet[r] = struct{}{}
	}
	shared := 0
	for _, r := range a {
		if _, exists := rightSet[r]; exists {
			shared++
		}
	}
	denominator := max(runeCount(a), len(rightSet))
	if denominator == 0 {
		return 0
	}
	return float64(shared) / float64(denominator)
}

func runeCount(value string) int {
	count := 0
	for range value {
		count++
	}
	return count
}

func unique(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
