package v1alpha1

import (
	"testing"
)

func TestImageSpec_MergeWith(t *testing.T) {
	tests := []struct {
		name     string
		base     ImageSpec
		override ImageSpec
		want     ImageSpec
	}{
		{
			name:     "override all fields",
			base:     ImageSpec{Repository: "base/img", Tag: "v1", ImagePullPolicy: "IfNotPresent"},
			override: ImageSpec{Repository: "new/img", Tag: "v2", ImagePullPolicy: "Always"},
			want:     ImageSpec{Repository: "new/img", Tag: "v2", ImagePullPolicy: "Always"},
		},
		{
			name:     "override partial",
			base:     ImageSpec{Repository: "base/img", Tag: "v1", ImagePullPolicy: "IfNotPresent"},
			override: ImageSpec{Tag: "v3"},
			want:     ImageSpec{Repository: "base/img", Tag: "v3", ImagePullPolicy: "IfNotPresent"},
		},
		{
			name:     "empty override keeps base",
			base:     ImageSpec{Repository: "base/img", Tag: "v1"},
			override: ImageSpec{},
			want:     ImageSpec{Repository: "base/img", Tag: "v1"},
		},
		{
			name:     "does not mutate receiver",
			base:     ImageSpec{Repository: "base/img", Tag: "v1"},
			override: ImageSpec{Repository: "new/img"},
			want:     ImageSpec{Repository: "new/img", Tag: "v1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			original := tt.base
			got := tt.base.MergeWith(tt.override)
			if got != tt.want {
				t.Errorf("MergeWith() = %+v, want %+v", got, tt.want)
			}
			if original != tt.base {
				t.Errorf("MergeWith() mutated the receiver: was %+v, now %+v", original, tt.base)
			}
		})
	}
}

func TestImageSpec_FullImage(t *testing.T) {
	tests := []struct {
		name string
		spec ImageSpec
		want string
	}{
		{
			name: "repository and tag",
			spec: ImageSpec{Repository: "fake.io/org/sharingd", Tag: "v0.1.0"},
			want: "fake.io/org/sharingd:v0.1.0",
		},
		{
			name: "no tag defaults to repository only",
			spec: ImageSpec{Repository: "fake.io/org/sharingd"},
			want: "fake.io/org/sharingd",
		},
		{
			name: "nested repository path",
			spec: ImageSpec{Repository: "fake.io/org/mpsd", Tag: "latest"},
			want: "fake.io/org/mpsd:latest",
		},
		{
			name: "empty repository returns empty string",
			spec: ImageSpec{Tag: "v1.0.0"},
			want: "",
		},
		{
			name: "completely empty returns empty string",
			spec: ImageSpec{},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.spec.FullImage()
			if got != tt.want {
				t.Errorf("FullImage() = %q, want %q", got, tt.want)
			}
		})
	}
}
