package v1alpha1

import "fmt"

// FullImage returns the complete image reference: <Repository>:<Tag>.
// If Tag is empty, returns just <Repository> (uses the registry default, typically "latest").
// If Repository is empty, returns an empty string.
func (s ImageSpec) FullImage() string {
	if s.Repository == "" {
		return ""
	}
	if s.Tag != "" {
		return fmt.Sprintf("%s:%s", s.Repository, s.Tag)
	}
	return s.Repository
}

// MergeWith returns a copy of s with any non-empty fields from override applied.
// Used to layer CRD-level image overrides on top of Helm-injected defaults.
func (s ImageSpec) MergeWith(override ImageSpec) ImageSpec {
	if override.Repository != "" {
		s.Repository = override.Repository
	}
	if override.Tag != "" {
		s.Tag = override.Tag
	}
	if override.ImagePullPolicy != "" {
		s.ImagePullPolicy = override.ImagePullPolicy
	}
	return s
}
