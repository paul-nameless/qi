package app

import "testing"

func TestBannerVersion(t *testing.T) {
	for in, want := range map[string]string{"1.0.0": "1.0.0", "dev-55430f1": "dev", "dev-55430f1-dirty": "dev", "dev": "dev"} {
		if got := bannerVersion(in); got != want {
			t.Errorf("bannerVersion(%q) = %q, want %q", in, got, want)
		}
	}
}
