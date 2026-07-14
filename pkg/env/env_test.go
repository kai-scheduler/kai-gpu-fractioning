/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package env

import (
	"testing"
	"time"
)

func TestString(t *testing.T) {
	tests := []struct {
		name     string
		envVal   *string // nil = unset
		fallback string
		want     string
	}{
		{"set", ptr("hello"), "default", "hello"},
		{"empty returns fallback", ptr(""), "default", "default"},
		{"unset returns fallback", nil, "fallback", "fallback"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const key = "TEST_STRING"
			if tt.envVal != nil {
				t.Setenv(key, *tt.envVal)
			}
			if got := String(key, tt.fallback); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBool(t *testing.T) {
	tests := []struct {
		name     string
		envVal   *string
		fallback bool
		want     bool
	}{
		{"true", ptr("true"), false, true},
		{"false", ptr("false"), true, false},
		{"1", ptr("1"), false, true},
		{"0", ptr("0"), true, false},
		{"unset returns fallback", nil, true, true},
		{"invalid returns fallback", ptr("notabool"), true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const key = "TEST_BOOL"
			if tt.envVal != nil {
				t.Setenv(key, *tt.envVal)
			}
			if got := Bool(key, tt.fallback); got != tt.want {
				t.Errorf("Bool() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestInt(t *testing.T) {
	tests := []struct {
		name     string
		envVal   *string
		fallback int
		want     int
	}{
		{"valid", ptr("42"), 0, 42},
		{"negative", ptr("-5"), 0, -5},
		{"unset returns fallback", nil, 99, 99},
		{"float returns fallback", ptr("3.14"), 7, 7},
		{"non-numeric returns fallback", ptr("abc"), 7, 7},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const key = "TEST_INT"
			if tt.envVal != nil {
				t.Setenv(key, *tt.envVal)
			}
			if got := Int(key, tt.fallback); got != tt.want {
				t.Errorf("Int() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestDuration(t *testing.T) {
	tests := []struct {
		name     string
		envVal   *string
		fallback time.Duration
		want     time.Duration
	}{
		{"seconds", ptr("30s"), time.Second, 30 * time.Second},
		{"minutes", ptr("2m"), time.Second, 2 * time.Minute},
		{"composite", ptr("1m30s"), time.Second, 90 * time.Second},
		{"unset returns fallback", nil, 5 * time.Second, 5 * time.Second},
		{"missing unit returns fallback", ptr("30"), 5 * time.Second, 5 * time.Second},
		{"invalid returns fallback", ptr("notaduration"), 10 * time.Second, 10 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const key = "TEST_DUR"
			if tt.envVal != nil {
				t.Setenv(key, *tt.envVal)
			}
			if got := Duration(key, tt.fallback); got != tt.want {
				t.Errorf("Duration() = %v, want %v", got, tt.want)
			}
		})
	}
}

func ptr(s string) *string { return &s }
