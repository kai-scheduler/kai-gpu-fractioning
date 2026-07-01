package v1alpha1

import "testing"

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
