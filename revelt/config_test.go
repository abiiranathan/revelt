package revelt

import (
	"testing"
	"testing/fstest"
)

func TestExtractMode(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		wantMode string
		wantOk   bool
	}{
		{
			name:     "standard ssr",
			line:     "// @mode ssr",
			wantMode: ModeSSR,
			wantOk:   true,
		},
		{
			name:     "svelte style client",
			line:     "<!-- @mode client -->",
			wantMode: ModeClient,
			wantOk:   true,
		},
		{
			name:     "lazy-client check before client",
			line:     "// @mode lazy-client",
			wantMode: ModeLazyClient,
			wantOk:   true,
		},
		{
			name:     "hydrate with spaces",
			line:     "  *  @mode   hydrate  ",
			wantMode: ModeHydrate,
			wantOk:   true,
		},
		{
			name:     "no annotation",
			line:     "const x = 42;",
			wantMode: "",
			wantOk:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotMode, gotOk := extractMode(tt.line)
			if gotOk != tt.wantOk {
				t.Errorf("extractMode() ok = %v, want %v", gotOk, tt.wantOk)
			}
			if gotMode != tt.wantMode {
				t.Errorf("extractMode() mode = %q, want %q", gotMode, tt.wantMode)
			}
		})
	}
}

func TestReadModeAnnotation(t *testing.T) {
	mockFS := fstest.MapFS{
		"components/SSRComponent.tsx": &fstest.MapFile{
			Data: []byte("// @mode ssr\nexport default function SSR() {}"),
		},
		"components/DefaultComponent.tsx": &fstest.MapFile{
			Data: []byte("export default function Default() {}"),
		},
		"components/DeepComment.tsx": &fstest.MapFile{
			Data: []byte("\n\n// @mode client\nexport default function Deep() {}"),
		},
	}

	tests := []struct {
		path     string
		wantMode string
	}{
		{"components/SSRComponent.tsx", ModeSSR},
		{"components/DefaultComponent.tsx", ModeHydrate},
		{"components/DeepComment.tsx", ModeClient},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got, err := readModeAnnotation(mockFS, tt.path)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.wantMode {
				t.Errorf("readModeAnnotation() = %q, want %q", got, tt.wantMode)
			}
		})
	}
}

func TestDiscoverComponentModes_MissingDir(t *testing.T) {
	emptyFS := fstest.MapFS{}
	modes, err := discoverComponentModes(emptyFS, "nonexistent")
	if err != nil {
		t.Fatalf("expected no error on missing directory, got: %v", err)
	}
	if len(modes) != 0 {
		t.Errorf("expected empty modes map, got %v", modes)
	}
}
