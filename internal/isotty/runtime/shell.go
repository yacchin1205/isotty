package runtimecfg

import "strings"

// ShellQuote wraps a value in POSIX single quotes, the only form in which
// bash expands nothing.
func ShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func shellJoin(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, ShellQuote(value))
	}
	return strings.Join(quoted, " ")
}
