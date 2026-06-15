package envutil

import (
	"os"
	"strings"
	"time"
)

// StringFromEnv returns the value of the environment variable name, trimmed of
// whitespace. If the variable is unset or blank, fallback is returned.
func StringFromEnv(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

// DurationFromEnv parses the environment variable name as a time.Duration.
// If the variable is unset, blank, or unparseable, fallback is returned.
func DurationFromEnv(name string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}
