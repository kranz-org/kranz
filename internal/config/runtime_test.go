package config

import "testing"

func TestRuntimeName(t *testing.T) {
	tests := []struct{ project, explicit, want string }{
		{"Shop API", "", "shop-api"},
		{"  Démo / API  ", "", "d-mo-api"},
		{"Shop", "shop-dev", "shop-dev"},
	}
	for _, test := range tests {
		cfg := &Config{Project: test.project, Runtime: RuntimeConfig{Name: test.explicit}}
		if got := cfg.RuntimeName(); got != test.want {
			t.Errorf("RuntimeName(%q, %q) = %q, want %q", test.project, test.explicit, got, test.want)
		}
	}
}

func TestValidateRejectsInvalidRuntimeName(t *testing.T) {
	cfg := &Config{Project: "Shop", Runtime: RuntimeConfig{Name: "Shop Dev"}, Services: map[string]Service{"web": {Command: "true"}}}
	if err := Validate(cfg); err == nil {
		t.Fatal("Validate accepted an invalid runtime.name")
	}
}
