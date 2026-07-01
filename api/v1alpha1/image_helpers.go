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
