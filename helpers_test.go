package rweb_test

import (
	"testing"

	"github.com/rohanthewiz/rweb"
)

func TestGenerateRandString(t *testing.T) {
	tests := []struct {
		name         string
		n            int
		groupByFours bool
		expectedLen  int
	}{
		{"Length 10, no grouping", 10, false, 10},
		{"Length 10, with grouping", 10, true, 10 + (10-1)/4}, // 2 dashes added
		{"Length 16, no grouping", 16, false, 16},
		{"Length 16, with grouping", 16, true, 16 + (16-1)/4}, //  dashes added
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := rweb.genRandString(tt.n, tt.groupByFours)
			if len(result) != tt.expectedLen {
				t.Errorf("expected length %d, got %d", tt.expectedLen, len(result))
			}

			if tt.groupByFours {
				t.Log("result", result)
				for i := 4; i < len(result); i += 5 {
					if result[i] != '-' {
						t.Errorf("expected dash at position %d, got %c", i, result[i])
					}
				}
			}
		})
	}
}
